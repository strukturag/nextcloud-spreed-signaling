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
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
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
	flag.Parse()

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling-proxy version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	signal.Notify(sigChan, syscall.SIGHUP)
	signal.Notify(sigChan, syscall.SIGUSR1)

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

	r := mux.NewRouter()

	proxy, err := NewProxyServer(log, r, version, config)
	if err != nil {
		log.Fatal("Error creating proxy server",
			zap.Error(err),
		)
	}

	if err := proxy.Start(config); err != nil {
		if !errors.Is(err, ErrCancelled) {
			log.Fatal("Error starting proxy server",
				zap.Error(err),
			)
		}
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

		for _, address := range strings.Split(addr, " ") {
			go func(address string) {
				log := log.With(zap.String("addr", address))
				log.Debug("Listening")
				listener, err := net.Listen("tcp", address)
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
				if err := srv.Serve(listener); err != nil {
					log.Fatal("Could not start server",
						zap.Error(err),
					)
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
					proxy.Reload(config)
				}
			case syscall.SIGUSR1:
				log.Info("Received SIGUSR1, scheduling server to shutdown")
				proxy.ScheduleShutdown()
			}
		case <-proxy.ShutdownChannel():
			log.Info("All clients disconnected, shutting down")
			break loop
		}
	}
}
