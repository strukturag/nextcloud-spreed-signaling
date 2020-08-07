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
	"flag"
	"fmt"
	"log"
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
	"github.com/nats-io/go-nats"

	"signaling"
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
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	flag.Parse()

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling-proxy version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	signal.Notify(sigChan, syscall.SIGHUP)

	log.Printf("Starting up version %s/%s as pid %d", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		log.Fatal("Could not read configuration: ", err)
	}

	cpus := runtime.NumCPU()
	runtime.GOMAXPROCS(cpus)
	log.Printf("Using a maximum of %d CPUs\n", cpus)

	natsUrl, _ := config.GetString("nats", "url")
	if natsUrl == "" {
		natsUrl = nats.DefaultURL
	}

	nats, err := signaling.NewNatsClient(natsUrl)
	if err != nil {
		log.Fatal("Could not create NATS client: ", err)
	}

	r := mux.NewRouter()

	proxy, err := NewProxyServer(r, version, config, nats)
	if err != nil {
		log.Fatal(err)
	}

	if err := proxy.Start(config); err != nil {
		log.Fatal(err)
	}
	defer proxy.Stop()

	if addr, _ := config.GetString("http", "listen"); addr != "" {
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
				listener, err := net.Listen("tcp", address)
				if err != nil {
					log.Fatal("Could not start listening: ", err)
				}
				srv := &http.Server{
					Handler: r,
					Addr:    addr,

					ReadTimeout:  time.Duration(readTimeout) * time.Second,
					WriteTimeout: time.Duration(writeTimeout) * time.Second,
				}
				if err := srv.Serve(listener); err != nil {
					log.Fatal("Could not start server: ", err)
				}
			}(address)
		}
	}

loop:
	for {
		switch sig := <-sigChan; sig {
		case os.Interrupt:
			log.Println("Interrupted")
			break loop
		case syscall.SIGHUP:
			log.Printf("Received SIGHUP, reloading %s", *configFlag)
			config, err := goconf.ReadConfigFile(*configFlag)
			if err != nil {
				log.Printf("Could not read configuration from %s: %s", *configFlag, err)
				continue
			}

			proxy.Reload(config)
		}
	}
}
