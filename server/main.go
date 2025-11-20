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
	"os"
	"os/signal"
	"runtime"
	runtimepprof "runtime/pprof"
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
	logger signaling.Logger // +checklocksignore
	mu     sync.Mutex
	// +checklocks:mu
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
			l.logger.Printf("Error closing listener %s: %s", listener.Addr(), err)
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
	signal.Notify(sigChan, syscall.SIGHUP)
	signal.Notify(sigChan, syscall.SIGUSR1)

	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger := log.Default()
	stopCtx = signaling.NewLoggerContext(stopCtx, logger)

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			logger.Fatal(err)
		}

		if err := runtimepprof.StartCPUProfile(f); err != nil {
			logger.Fatalf("Error writing CPU profile to %s: %s", *cpuprofile, err)
		}
		logger.Printf("Writing CPU profile to %s ...", *cpuprofile)
		defer runtimepprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			logger.Fatal(err)
		}

		defer func() {
			logger.Printf("Writing Memory profile to %s ...", *memprofile)
			runtime.GC()
			if err := runtimepprof.WriteHeapProfile(f); err != nil {
				logger.Printf("Error writing Memory profile to %s: %s", *memprofile, err)
			}
		}()
	}

	logger.Printf("Starting up version %s/%s as pid %d", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		logger.Fatal("Could not read configuration: ", err)
	}

	logger.Printf("Using a maximum of %d CPUs", runtime.GOMAXPROCS(0))

	signaling.RegisterStats()

	natsUrl, _ := signaling.GetStringOptionWithEnv(config, "nats", "url")
	if natsUrl == "" {
		natsUrl = nats.DefaultURL
	}

	events, err := signaling.NewAsyncEvents(stopCtx, natsUrl)
	if err != nil {
		logger.Fatal("Could not create async events client: ", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := events.Close(ctx); err != nil {
			logger.Printf("Error closing events handler: %s", err)
		}
	}()

	dnsMonitor, err := signaling.NewDnsMonitor(logger, dnsMonitorInterval)
	if err != nil {
		logger.Fatal("Could not create DNS monitor: ", err)
	}
	if err := dnsMonitor.Start(); err != nil {
		logger.Fatal("Could not start DNS monitor: ", err)
	}
	defer dnsMonitor.Stop()

	etcdClient, err := signaling.NewEtcdClient(logger, config, "mcu")
	if err != nil {
		logger.Fatalf("Could not create etcd client: %s", err)
	}
	defer func() {
		if err := etcdClient.Close(); err != nil {
			logger.Printf("Error while closing etcd client: %s", err)
		}
	}()

	rpcServer, err := signaling.NewGrpcServer(stopCtx, config, version)
	if err != nil {
		logger.Fatalf("Could not create RPC server: %s", err)
	}
	go func() {
		if err := rpcServer.Run(); err != nil {
			logger.Fatalf("Could not start RPC server: %s", err)
		}
	}()
	defer rpcServer.Close()

	rpcClients, err := signaling.NewGrpcClients(stopCtx, config, etcdClient, dnsMonitor, version)
	if err != nil {
		logger.Fatalf("Could not create RPC clients: %s", err)
	}
	defer rpcClients.Close()

	r := mux.NewRouter()
	hub, err := signaling.NewHub(stopCtx, config, events, rpcServer, rpcClients, etcdClient, r, version)
	if err != nil {
		logger.Fatal("Could not create hub: ", err)
	}

	mcuUrl, _ := signaling.GetStringOptionWithEnv(config, "mcu", "url")
	mcuType, _ := config.GetString("mcu", "type")
	if mcuType == "" && mcuUrl != "" {
		logger.Printf("WARNING: Old-style MCU configuration detected with url but no type, defaulting to type %s", signaling.McuTypeJanus)
		mcuType = signaling.McuTypeJanus
	} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
		logger.Printf("WARNING: Old-style MCU configuration detected with type but no url, disabling")
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
				mcu, err = signaling.NewMcuProxy(ctx, config, etcdClient, rpcClients, dnsMonitor)
				signaling.UnregisterJanusMcuStats()
				signaling.RegisterProxyMcuStats()
			default:
				logger.Fatal("Unsupported MCU type: ", mcuType)
			}
			if err == nil {
				err = mcu.Start(ctx)
				if err != nil {
					logger.Printf("Could not create %s MCU: %s", mcuType, err)
				}
			}
			if err == nil {
				break
			}

			logger.Printf("Could not initialize %s MCU (%s) will retry in %s", mcuType, err, mcuRetry)
			mcuRetryTimer.Reset(mcuRetry)
			select {
			case <-stopCtx.Done():
				logger.Fatalf("Cancelled")
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					logger.Printf("Received SIGHUP, reloading %s", *configFlag)
					if config, err = goconf.ReadConfigFile(*configFlag); err != nil {
						logger.Printf("Could not read configuration from %s: %s", *configFlag, err)
					} else {
						mcuUrl, _ = signaling.GetStringOptionWithEnv(config, "mcu", "url")
						mcuType, _ = config.GetString("mcu", "type")
						if mcuType == "" && mcuUrl != "" {
							logger.Printf("WARNING: Old-style MCU configuration detected with url but no type, defaulting to type %s", signaling.McuTypeJanus)
							mcuType = signaling.McuTypeJanus
						} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
							logger.Printf("WARNING: Old-style MCU configuration detected with type but no url, disabling")
							mcuType = ""
							break mcuTypeLoop
						}
					}
				}
			case <-mcuRetryTimer.C:
				// Retry connection
				mcuRetry = min(mcuRetry*2, maxMcuRetry)
			}
		}
		if mcu != nil {
			defer mcu.Stop()

			logger.Printf("Using %s MCU", mcuType)
			hub.SetMcu(mcu)
		}
	}

	go hub.Run()
	defer hub.Stop()

	server, err := signaling.NewBackendServer(stopCtx, config, hub, version)
	if err != nil {
		logger.Fatal("Could not create backend server: ", err)
	}
	if err := server.Start(r); err != nil {
		logger.Fatal("Could not start backend server: ", err)
	}

	listeners := Listeners{
		logger: logger,
	}

	if saddr, _ := signaling.GetStringOptionWithEnv(config, "https", "listen"); saddr != "" {
		cert, _ := config.GetString("https", "certificate")
		key, _ := config.GetString("https", "key")
		if cert == "" || key == "" {
			logger.Fatal("Need a certificate and key for the HTTPS listener")
		}

		readTimeout, _ := config.GetInt("https", "readtimeout")
		if readTimeout <= 0 {
			readTimeout = defaultReadTimeout
		}
		writeTimeout, _ := config.GetInt("https", "writetimeout")
		if writeTimeout <= 0 {
			writeTimeout = defaultWriteTimeout
		}
		for address := range signaling.SplitEntries(saddr, " ") {
			go func(address string) {
				logger.Println("Listening on", address)
				listener, err := createTLSListener(address, cert, key)
				if err != nil {
					logger.Fatal("Could not start listening: ", err)
				}
				srv := &http.Server{
					Handler: r,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				listeners.Add(listener)
				if err := srv.Serve(listener); err != nil {
					if !hub.IsShutdownScheduled() || !errors.Is(err, net.ErrClosed) {
						logger.Fatal("Could not start server: ", err)
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

		for address := range signaling.SplitEntries(addr, " ") {
			go func(address string) {
				logger.Println("Listening on", address)
				listener, err := createListener(address)
				if err != nil {
					logger.Fatal("Could not start listening: ", err)
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
						logger.Fatal("Could not start server: ", err)
					}
				}
			}(address)
		}
	}

loop:
	for {
		select {
		case <-stopCtx.Done():
			logger.Println("Interrupted")
			break loop
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				logger.Printf("Received SIGHUP, reloading %s", *configFlag)
				if config, err := goconf.ReadConfigFile(*configFlag); err != nil {
					logger.Printf("Could not read configuration from %s: %s", *configFlag, err)
				} else {
					hub.Reload(stopCtx, config)
					server.Reload(config)
				}
			case syscall.SIGUSR1:
				logger.Printf("Received SIGUSR1, scheduling server to shutdown")
				hub.ScheduleShutdown()
				listeners.Close()
			}
		case <-hub.ShutdownChannel():
			logger.Printf("All clients disconnected, shutting down")
			break loop
		}
	}
}
