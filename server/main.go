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
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

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
	log       *zap.Logger
	mu        sync.Mutex
	listeners []net.Listener
}

func newListeners(log *zap.Logger) *Listeners {
	return &Listeners{
		log: log,
	}
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
			l.log.Error("Error closing listener",
				zap.Any("addr", listener.Addr()),
				zap.Error(err),
			)
		}
	}
}

func main() {
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
			fmt.Printf("%s\n", err)
			os.Exit(1)
		}

		if err := runtimepprof.StartCPUProfile(f); err != nil {
			fmt.Printf("Error writing CPU profile to %s: %s\n", *cpuprofile, err)
			os.Exit(1)
		}
		fmt.Printf("Writing CPU profile to %s ...\n", *cpuprofile)
		defer runtimepprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			fmt.Printf("%s\n", err)
			os.Exit(1)
		}

		defer func() {
			fmt.Printf("Writing Memory profile to %s ...\n", *memprofile)
			runtime.GC()
			if err := runtimepprof.WriteHeapProfile(f); err != nil {
				fmt.Printf("Error writing Memory profile to %s: %s\n", *memprofile, err)
				os.Exit(1)
			}
		}()
	}

	fmt.Printf("Starting up version %s/%s as pid %d\n", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		fmt.Printf("Could not read configuration: %s\n", err)
		os.Exit(1)
	}

	var logConfig zap.Config
	if debug, _ := config.GetBool("app", "debug"); debug {
		logConfig = zap.NewDevelopmentConfig()
	} else {
		logConfig = zap.NewProductionConfig()
		logConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}
	log, err := logConfig.Build(
		// Only log stack traces when panicing.
		zap.AddStacktrace(zap.DPanicLevel),
	)
	if err != nil {
		fmt.Printf("Could not create logger: %s\n", err)
		os.Exit(1)
	}

	restoreGlobalLogs := zap.ReplaceGlobals(log)
	defer restoreGlobalLogs()

	cpus := runtime.NumCPU()
	runtime.GOMAXPROCS(cpus)
	log.Debug("Using number of CPUs",
		zap.Int("cpus", cpus),
	)

	signaling.RegisterStats()

	natsUrl, _ := signaling.GetStringOptionWithEnv(config, "nats", "url")
	if natsUrl == "" {
		natsUrl = nats.DefaultURL
	}

	events, err := signaling.NewAsyncEvents(log, natsUrl)
	if err != nil {
		log.Fatal("Could not create async events client",
			zap.Error(err),
		)
	}
	defer events.Close()

	dnsMonitor, err := signaling.NewDnsMonitor(log, dnsMonitorInterval)
	if err != nil {
		log.Fatal("Could not create DNS monitor",
			zap.Error(err),
		)
	}
	if err := dnsMonitor.Start(); err != nil {
		log.Fatal("Could not start DNS monitor",
			zap.Error(err),
		)
	}
	defer dnsMonitor.Stop()

	etcdClient, err := signaling.NewEtcdClient(log, config, "mcu")
	if err != nil {
		log.Fatal("Could not create etcd client",
			zap.Error(err),
		)
	}
	defer func() {
		if err := etcdClient.Close(); err != nil {
			log.Error("Error while closing etcd client",
				zap.Error(err),
			)
		}
	}()

	rpcServer, err := signaling.NewGrpcServer(log, config)
	if err != nil {
		log.Fatal("Could not create RPC server",
			zap.Error(err),
		)
	}
	go func() {
		if err := rpcServer.Run(); err != nil {
			log.Fatal("Could not start RPC server",
				zap.Error(err),
			)
		}
	}()
	defer rpcServer.Close()

	rpcClients, err := signaling.NewGrpcClients(log, config, etcdClient, dnsMonitor)
	if err != nil {
		log.Fatal("Could not create RPC clients",
			zap.Error(err),
		)
	}
	defer rpcClients.Close()

	r := mux.NewRouter()
	hub, err := signaling.NewHub(log, config, events, rpcServer, rpcClients, etcdClient, r, version)
	if err != nil {
		log.Fatal("Could not create hub",
			zap.Error(err),
		)
	}

	mcuUrl, _ := signaling.GetStringOptionWithEnv(config, "mcu", "url")
	mcuType, _ := config.GetString("mcu", "type")
	if mcuType == "" && mcuUrl != "" {
		log.Warn("Old-style MCU configuration detected with url but no type, using defaulting type",
			zap.String("type", signaling.McuTypeJanus),
		)
		mcuType = signaling.McuTypeJanus
	} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
		log.Warn("Old-style MCU configuration detected with type but no url, disabling")
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
				mcu, err = signaling.NewMcuJanus(ctx, log, mcuUrl, config)
				signaling.UnregisterProxyMcuStats()
				signaling.RegisterJanusMcuStats()
			case signaling.McuTypeProxy:
				mcu, err = signaling.NewMcuProxy(log, config, etcdClient, rpcClients, dnsMonitor)
				signaling.UnregisterJanusMcuStats()
				signaling.RegisterProxyMcuStats()
			default:
				log.Fatal("Unsupported MCU type",
					zap.String("type", mcuType),
				)
			}
			if err == nil {
				err = mcu.Start(ctx)
				if err != nil {
					log.Error("Could not create MCU",
						zap.String("type", mcuType),
						zap.Error(err),
					)
				}
			}
			if err == nil {
				break
			}

			log.Error("Could not initialize MCU, will retry",
				zap.String("type", mcuType),
				zap.Duration("wait", mcuRetry),
				zap.Error(err),
			)

			mcuRetryTimer.Reset(mcuRetry)
			select {
			case sig := <-sigChan:
				switch sig {
				case os.Interrupt:
					log.Fatal("Cancelled")
				case syscall.SIGHUP:
					log.Info("Received SIGHUP, reloading",
						zap.String("filename", *configFlag),
					)
					if config, err = goconf.ReadConfigFile(*configFlag); err != nil {
						log.Error("Could not read configuration",
							zap.String("filename", *configFlag),
							zap.Error(err),
						)
					} else {
						mcuUrl, _ = signaling.GetStringOptionWithEnv(config, "mcu", "url")
						mcuType, _ = config.GetString("mcu", "type")
						if mcuType == "" && mcuUrl != "" {
							log.Warn("Old-style MCU configuration detected with url but no type, using defaulting type",
								zap.String("type", signaling.McuTypeJanus),
							)
							mcuType = signaling.McuTypeJanus
						} else if mcuType == signaling.McuTypeJanus && mcuUrl == "" {
							log.Warn("Old-style MCU configuration detected with type but no url, disabling")
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

			log.Info("Using MCU",
				zap.String("type", mcuType),
			)
			hub.SetMcu(mcu)
		}
	}

	go hub.Run()
	defer hub.Stop()

	server, err := signaling.NewBackendServer(log, config, hub, version)
	if err != nil {
		log.Fatal("Could not create backend server",
			zap.Error(err),
		)
	}
	if err := server.Start(r); err != nil {
		log.Fatal("Could not start backend server",
			zap.Error(err),
		)
	}

	if debug, _ := config.GetBool("app", "debug"); debug {
		log.Debug("Installing debug handlers in \"/debug/pprof\"")
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

	listeners := newListeners(log)
	defer listeners.Close()

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
				log := log.With(zap.String("addr", address))
				log.Debug("Listening")
				listener, err := createTLSListener(address, cert, key)
				if err != nil {
					log.Fatal("Could not start listening",
						zap.Error(err),
					)
				}
				srv := &http.Server{
					Handler: r,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				listeners.Add(listener)
				if err := srv.Serve(listener); err != nil {
					if !hub.IsShutdownScheduled() || !errors.Is(err, net.ErrClosed) {
						log.Fatal("Could not start server",
							zap.Error(err),
						)
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
				log := log.With(zap.String("addr", address))
				log.Debug("Listening")
				listener, err := createListener(address)
				if err != nil {
					log.Fatal("Could not start listening",
						zap.Error(err),
					)
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
						log.Fatal("Could not start server",
							zap.Error(err),
						)
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
				log.Debug("Interrupted")
				break loop
			case syscall.SIGHUP:
				log.Info("Received SIGHUP, reloading",
					zap.String("filename", *configFlag),
				)
				if config, err := goconf.ReadConfigFile(*configFlag); err != nil {
					log.Error("Could not read configuration",
						zap.String("filename", *configFlag),
						zap.Error(err),
					)
				} else {
					hub.Reload(config)
					server.Reload(config)
				}
			case syscall.SIGUSR1:
				log.Info("Received SIGUSR1, scheduling server to shutdown")
				hub.ScheduleShutdown()
				listeners.Close()
			}
		case <-hub.ShutdownChannel():
			log.Info("All clients disconnected, shutting down")
			break loop
		}
	}
}
