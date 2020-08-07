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
	"crypto/tls"
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
	"time"

	"github.com/dlintw/goconf"
	_ "github.com/gorilla/context"
	"github.com/gorilla/mux"
	"github.com/nats-io/go-nats"

	"signaling"
)

var (
	version = "unreleased"

	config = flag.String("config", "server.conf", "config file to use")

	cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

	memprofile = flag.String("memprofile", "", "write memory profile to file")

	showVersion = flag.Bool("version", false, "show version and quit")
)

const (
	defaultReadTimeout  = 15
	defaultWriteTimeout = 15

	initialMcuRetry = time.Second
	maxMcuRetry     = time.Second * 16
)

func createListener(addr string) (net.Listener, error) {
	if addr[0] == '/' {
		os.Remove(addr)
		return net.Listen("unix", addr)
	} else {
		return net.Listen("tcp", addr)
	}
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
	} else {
		return tls.Listen("tcp", addr, &config)
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	flag.Parse()

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}

		log.Printf("Writing CPU profile to %s ...\n", *cpuprofile)
		runtimepprof.StartCPUProfile(f)
		defer runtimepprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal(err)
		}

		defer func() {
			log.Printf("Writing Memory profile to %s ...\n", *memprofile)
			runtime.GC()
			runtimepprof.WriteHeapProfile(f)
		}()
	}

	log.Printf("Starting up version %s/%s as pid %d", version, runtime.Version(), os.Getpid())

	config, err := goconf.ReadConfigFile(*config)
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
	hub, err := signaling.NewHub(config, nats, r, version)
	if err != nil {
		log.Fatal("Could not create hub: ", err)
	}

	mcuUrl, _ := config.GetString("mcu", "url")
	if mcuUrl != "" {
		mcuType, _ := config.GetString("mcu", "type")
		if mcuType == "" {
			mcuType = signaling.McuTypeDefault
		}

		var mcu signaling.Mcu
		mcuRetry := initialMcuRetry
		mcuRetryTimer := time.NewTimer(mcuRetry)
		for {
			switch mcuType {
			case signaling.McuTypeJanus:
				mcu, err = signaling.NewMcuJanus(mcuUrl, config, nats)
			case signaling.McuTypeProxy:
				mcu, err = signaling.NewMcuProxy(mcuUrl, config)
			default:
				log.Fatal("Unsupported MCU type: ", mcuType)
			}
			if err == nil {
				err = mcu.Start()
				if err != nil {
					log.Printf("Could not create %s MCU at %s: %s", mcuType, mcuUrl, err)
				}
			}
			if err == nil {
				break
			}

			log.Printf("Could not initialize %s MCU at %s (%s) will retry in %s", mcuType, mcuUrl, err, mcuRetry)
			mcuRetryTimer.Reset(mcuRetry)
			select {
			case <-interrupt:
				log.Fatalf("Cancelled")
			case <-mcuRetryTimer.C:
				// Retry connection
				mcuRetry = mcuRetry * 2
				if mcuRetry > maxMcuRetry {
					mcuRetry = maxMcuRetry
				}
			}
		}
		defer mcu.Stop()

		log.Printf("Using MCU %s at %s\n", mcuType, mcuUrl)
		hub.SetMcu(mcu)
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

	if saddr, _ := config.GetString("https", "listen"); saddr != "" {
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
				if err := srv.Serve(listener); err != nil {
					log.Fatal("Could not start server: ", err)
				}
			}(address)
		}
	}

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
				if err := srv.Serve(listener); err != nil {
					log.Fatal("Could not start server: ", err)
				}
			}(address)
		}
	}

	<-interrupt
	log.Println("Interrupted")
}
