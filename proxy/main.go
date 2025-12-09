/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
	signalinglog "github.com/strukturag/nextcloud-spreed-signaling/log"
)

var (
	version = "unreleased"

	configFlag = flag.String("config", "proxy.conf", "config file to use")

	showVersion = flag.Bool("version", false, "show version and quit")
)

const (
	defaultReadTimeout  = 15
	defaultWriteTimeout = 15

	proxyDebugMessages = false
)

func main() {
	log.SetFlags(log.Lshortfile)
	flag.Parse()

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling-proxy version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)
	signal.Notify(sigChan, syscall.SIGUSR1)

	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger := log.Default()
	stopCtx = signalinglog.NewLoggerContext(stopCtx, logger)

	logger.Printf("Starting up version %s/%s as pid %d", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		logger.Fatal("Could not read configuration: ", err)
	}

	logger.Printf("Using a maximum of %d CPUs", runtime.GOMAXPROCS(0))

	r := mux.NewRouter()

	proxy, err := NewProxyServer(stopCtx, r, version, config)
	if err != nil {
		logger.Fatal(err)
	}

	if err := proxy.Start(config); err != nil {
		logger.Fatal(err)
	}
	defer proxy.Stop()

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
				listener, err := net.Listen("tcp", address)
				if err != nil {
					logger.Fatal("Could not start listening: ", err)
				}
				srv := &http.Server{
					Handler: r,
					Addr:    addr,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				if err := srv.Serve(listener); err != nil {
					logger.Fatal("Could not start server: ", err)
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
					proxy.Reload(config)
				}
			case syscall.SIGUSR1:
				logger.Printf("Received SIGUSR1, scheduling server to shutdown")
				proxy.ScheduleShutdown()
			}
		case <-proxy.ShutdownChannel():
			logger.Printf("All clients disconnected, shutting down")
			break loop
		}
	}
}
