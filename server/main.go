/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
 *
 * @author Joachim Bauch <bauch@struktur.de>
 *
 * @license GNU AGPL version 3 or any later version
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	runtimepprof "runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/nats-io/nats.go"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

var (
	version = "unreleased"

	configFlag = flag.String("config", "server.conf", "config file to use")

	cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

	memprofile = flag.String("memprofile", "", "write memory profile to file")

	showVersion = flag.Bool("version", false, "show version and quit")
)

const (
	defaultReadTimeout  = 15
	defaultWriteTimeout = 30

	initialMcuRetry = time.Second
	maxMcuRetry     = time.Second * 16

	dnsMonitorInterval = time.Second
)

func createListener(addr string) (net.Listener, error) {
	if addr[0] == '/' {
		os.Remove(addr)
		return net.Listen("unix", addr)
	}

	return net.Listen("tcp", addr)
}

func createTLSListener(addr string, certFile, keyFile string) (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	config := tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	if addr[0] == '/' {
		os.Remove(addr)
		return tls.Listen("unix", addr, &config)
	}

	return tls.Listen("tcp", addr, &config)
}

type Listeners struct {
	mu        sync.Mutex
	listeners []net.Listener
}

func (l *Listeners) Add(listener net.Listener) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.listeners = append(l.listeners, listener)
}

func (l *Listeners) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, listener := range l.listeners {
		if err := listener.Close(); err != nil {
			log.Printf("Error closing listener %s: %s", listener.Addr(), err)
		}
	}
}

func main() {
	log.SetFlags(log.Lshortfile)
	flag.Parse()

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	signal.Notify(sigChan, syscall.SIGHUP)
	signal.Notify(sigChan, syscall.SIGUSR1)

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}

		if err := runtimepprof.StartCPUProfile(f); err != nil {
			log.Fatalf("Error writing CPU profile to %s: %s", *cpuprofile, err)
		}
		log.Printf("Writing CPU profile to %s ...", *cpuprofile)
		defer runtimepprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal(err)
		}

		defer func() {
			log.Printf("Writing Memory profile to %s ...", *memprofile)
			runtime.GC()
			if err := runtimepprof.WriteHeapProfile(f); err != nil {
				log.Printf("Error writing Memory profile to %s: %s", *memprofile, err)
			}
		}()
	}

	log.Printf("Starting up version %s/%s as pid %d", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		log.Fatal("Could not read configuration: ", err)
	}

	log.Printf("Using a maximum of %d CPUs", runtime.GOMAXPROCS(0))

	signaling.RegisterStats()

	natsUrl, _ := signaling.GetStringOptionWithEnv(config, "nats", "url")
	if natsUrl == "" {
		natsUrl = nats.DefaultURL
	}

	events, err := signaling.NewAsyncEvents(natsUrl)
	if err != nil {
		log.Fatal("Could not create async events client: ", err)
	}
	defer events.Close()

	dnsMonitor, err := signaling.NewDnsMonitor(dnsMonitorInterval)
	if err != nil {
		log.Fatal("Could not create DNS monitor: ", err)
	}
	if err := dnsMonitor.Start(); err != nil {
		log.Fatal("Could not start DNS monitor: ", err)
	}
	defer dnsMonitor.Stop()

	etcdClient, err := signaling.NewEtcdClient(config, "mcu")
	if err != nil {
		log.Fatalf("Could not create etcd client: %s", err)
	}
	defer func() {
		if err := etcdClient.Close(); err != nil {
			log.Printf("Error while closing etcd client: %s", err)
		}
	}()

	rpcServer, err := signaling.NewGrpcServer(config, version)
	if err != nil {
		log.Fatalf("Could not create RPC server: %s", err)
	}
	go func() {
		if err := rpcServer.Run(); err != nil {
			log.Fatalf("Could not start RPC server: %s", err)
		}
	}()
	defer rpcServer.Close()

	rpcClients, err := signaling.NewGrpcClients(config, etcdClient, dnsMonitor, version)
	if err != nil {
		log.Fatalf("Could not create RPC clients: %s", err)
	}
	defer rpcClients.Close()

	r := mux.NewRouter()
	hub, err := signaling.NewHub(config, events, rpcServer, rpcClients, etcdClient, r, version)
	if err != nil {
		log.Fatal("Could not create hub: ", err)
	}

	mcuUrl, _ := signaling.GetStringOptionWithEnv(config, "mcu", "url")
	mcuType, _ := config.GetString("mcu", "type")
	if mcuType == "" && mcuUrl != "" {
		log.Printf("WARNING: Old-style MCU configuration detected with url but no type, defaulting to type %s", signaling.McuTypeJanus)
		mcuType = signaling.McuTypeJanus
	} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
		log.Printf("WARNING: Old-style MCU configuration detected with type but no url, disabling")
		mcuType = ""
	}

	if mcuType != "" {
		var mcu signaling.Mcu
		mcuRetry := initialMcuRetry
		mcuRetryTimer := time.NewTimer(mcuRetry)
	mcuTypeLoop:
		for {
			// Context should be cancelled on signals but need a way to differentiate later.
			ctx := context.TODO()
			switch mcuType {
			case signaling.McuTypeJanus:
				mcu, err = signaling.NewMcuJanus(ctx, mcuUrl, config)
				signaling.UnregisterProxyMcuStats()
				signaling.RegisterJanusMcuStats()
			case signaling.McuTypeProxy:
				mcu, err = signaling.NewMcuProxy(config, etcdClient, rpcClients, dnsMonitor)
				signaling.UnregisterJanusMcuStats()
				signaling.RegisterProxyMcuStats()
			default:
				log.Fatal("Unsupported MCU type: ", mcuType)
			}
			if err == nil {
				err = mcu.Start(ctx)
				if err != nil {
					log.Printf("Could not create %s MCU: %s", mcuType, err)
				}
			}
			if err == nil {
				break
			}

			log.Printf("Could not initialize %s MCU (%s) will retry in %s", mcuType, err, mcuRetry)
			mcuRetryTimer.Reset(mcuRetry)
			select {
			case sig := <-sigChan:
				switch sig {
				case os.Interrupt:
					log.Fatalf("Cancelled")
				case syscall.SIGHUP:
					log.Printf("Received SIGHUP, reloading %s", *configFlag)
					if config, err = goconf.ReadConfigFile(*configFlag); err != nil {
						log.Printf("Could not read configuration from %s: %s", *configFlag, err)
					} else {
						mcuUrl, _ = signaling.GetStringOptionWithEnv(config, "mcu", "url")
						mcuType, _ = config.GetString("mcu", "type")
						if mcuType == "" && mcuUrl != "" {
							log.Printf("WARNING: Old-style MCU configuration detected with url but no type, defaulting to type %s", signaling.McuTypeJanus)
							mcuType = signaling.McuTypeJanus
						} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
							log.Printf("WARNING: Old-style MCU configuration detected with type but no url, disabling")
							mcuType = ""
							break mcuTypeLoop
						}
					}
				}
			case <-mcuRetryTimer.C:
				// Retry connection
				mcuRetry = mcuRetry * 2
				if mcuRetry > maxMcuRetry {
					mcuRetry = maxMcuRetry
				}
			}
		}
		if mcu != nil {
			defer mcu.Stop()

			log.Printf("Using %s MCU", mcuType)
			hub.SetMcu(mcu)
		}
	}

	go hub.Run()
	defer hub.Stop()

	server, err := signaling.NewBackendServer(config, hub, version)
	if err != nil {
		log.Fatal("Could not create backend server: ", err)
	}
	if err := server.Start(r); err != nil {
		log.Fatal("Could not start backend server: ", err)
	}

	if debug, _ := config.GetBool("app", "debug"); debug {
		log.Println("Installing debug handlers in \"/debug/pprof\"")
		r.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
		r.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		r.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		r.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		r.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
		for _, profile := range runtimepprof.Profiles() {
			name := profile.Name()
			r.Handle("/debug/pprof/"+name, pprof.Handler(name))
		}
	}

	var listeners Listeners

	if saddr, _ := signaling.GetStringOptionWithEnv(config, "https", "listen"); saddr != "" {
		cert, _ := config.GetString("https", "certificate")
		key, _ := config.GetString("https", "key")
		if cert == "" || key == "" {
			log.Fatal("Need a certificate and key for the HTTPS listener")
		}

		readTimeout, _ := config.GetInt("https", "readtimeout")
		if readTimeout <= 0 {
			readTimeout = defaultReadTimeout
		}
		writeTimeout, _ := config.GetInt("https", "writetimeout")
		if writeTimeout <= 0 {
			writeTimeout = defaultWriteTimeout
		}
		for _, address := range strings.Split(saddr, " ") {
			go func(address string) {
				log.Println("Listening on", address)
				listener, err := createTLSListener(address, cert, key)
				if err != nil {
					log.Fatal("Could not start listening: ", err)
				}
				srv := &http.Server{
					Handler: r,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				listeners.Add(listener)
				if err := srv.Serve(listener); err != nil {
					if !hub.IsShutdownScheduled() || !errors.Is(err, net.ErrClosed) {
						log.Fatal("Could not start server: ", err)
					}
				}
			}(address)
		}
	}

	if addr, _ := signaling.GetStringOptionWithEnv(config, "http", "listen"); addr != "" {
		readTimeout, _ := config.GetInt("http", "readtimeout")
		if readTimeout <= 0 {
			readTimeout = defaultReadTimeout
		}
		writeTimeout, _ := config.GetInt("http", "writetimeout")
		if writeTimeout <= 0 {
			writeTimeout = defaultWriteTimeout
		}

		for _, address := range strings.Split(addr, " ") {
			go func(address string) {
				log.Println("Listening on", address)
				listener, err := createListener(address)
				if err != nil {
					log.Fatal("Could not start listening: ", err)
				}
				srv := &http.Server{
					Handler: r,
					Addr:    addr,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				listeners.Add(listener)
				if err := srv.Serve(listener); err != nil {
					if !hub.IsShutdownScheduled() || !errors.Is(err, net.ErrClosed) {
						log.Fatal("Could not start server: ", err)
					}
				}
			}(address)
		}
	}

loop:
	for {
		select {
		case sig := <-sigChan:
			switch sig {
			case os.Interrupt:
				log.Println("Interrupted")
				break loop
			case syscall.SIGHUP:
				log.Printf("Received SIGHUP, reloading %s", *configFlag)
				if config, err := goconf.ReadConfigFile(*configFlag); err != nil {
					log.Printf("Could not read configuration from %s: %s", *configFlag, err)
				} else {
					hub.Reload(config)
					server.Reload(config)
				}
			case syscall.SIGUSR1:
				log.Printf("Received SIGUSR1, scheduling server to shutdown")
				hub.ScheduleShutdown()
				listeners.Close()
			}
		case <-hub.ShutdownChannel():
			log.Printf("All clients disconnected, shutting down")
			break loop
		}
	}
}
