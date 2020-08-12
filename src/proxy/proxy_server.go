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
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	runtimepprof "runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/websocket"

	"golang.org/x/net/context"

	"gopkg.in/dgrijalva/jwt-go.v3"

	"signaling"
)

const (
	// Buffer sizes when reading/writing websocket connections.
	websocketReadBufferSize  = 4096
	websocketWriteBufferSize = 4096

	initialMcuRetry = time.Second
	maxMcuRetry     = time.Second * 16

	updateLoadInterval     = time.Second
	expireSessionsInterval = 10 * time.Second

	// Maximum age a token may have to prevent reuse of old tokens.
	maxTokenAge = 5 * time.Minute
)

type ContextKey string

var (
	ContextKeySession = ContextKey("session")

	TimeoutCreatingPublisher  = signaling.NewError("timeout", "Timeout creating publisher.")
	TimeoutCreatingSubscriber = signaling.NewError("timeout", "Timeout creating subscriber.")
	TokenAuthFailed           = signaling.NewError("auth_failed", "The token could not be authenticated.")
	TokenExpired              = signaling.NewError("token_expired", "The token is expired.")
	UnknownClient             = signaling.NewError("unknown_client", "Unknown client id given.")
	UnsupportedCommand        = signaling.NewError("bad_request", "Unsupported command received.")
	UnsupportedMessage        = signaling.NewError("bad_request", "Unsupported message received.")
	UnsupportedPayload        = signaling.NewError("unsupported_payload", "Unsupported payload type.")
	ShutdownScheduled         = signaling.NewError("shutdown_scheduled", "The server is scheduled to shutdown.")
)

type ProxyServer struct {
	version string
	country string

	url     string
	nats    signaling.NatsClient
	mcu     signaling.Mcu
	stopped uint32
	load    int64

	shutdownChannel   chan bool
	shutdownScheduled uint32

	upgrader websocket.Upgrader

	tokenKeys       atomic.Value
	statsAllowedIps map[string]bool

	sid          uint64
	cookie       *securecookie.SecureCookie
	sessions     map[uint64]*ProxySession
	sessionsLock sync.RWMutex

	clients     map[string]signaling.McuClient
	clientIds   map[string]string
	clientsLock sync.RWMutex
}

func NewProxyServer(r *mux.Router, version string, config *goconf.ConfigFile, nats signaling.NatsClient) (*ProxyServer, error) {
	hashKey, _ := config.GetString("sessions", "hashkey")
	switch len(hashKey) {
	case 32:
	case 64:
	default:
		log.Printf("WARNING: The sessions hash key should be 32 or 64 bytes but is %d bytes", len(hashKey))
	}

	blockKey, _ := config.GetString("sessions", "blockkey")
	blockBytes := []byte(blockKey)
	switch len(blockKey) {
	case 0:
		blockBytes = nil
	case 16:
	case 24:
	case 32:
	default:
		return nil, fmt.Errorf("The sessions block key must be 16, 24 or 32 bytes but is %d bytes", len(blockKey))
	}

	tokenKeys := make(map[string]*rsa.PublicKey)
	options, _ := config.GetOptions("tokens")
	for _, id := range options {
		filename, _ := config.GetString("tokens", id)
		if filename == "" {
			return nil, fmt.Errorf("No filename given for token %s", id)
		}

		keyData, err := ioutil.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("Could not read public key from %s: %s", filename, err)
		}
		key, err := jwt.ParseRSAPublicKeyFromPEM(keyData)
		if err != nil {
			return nil, fmt.Errorf("Could not parse public key from %s: %s", filename, err)
		}

		tokenKeys[id] = key
	}

	var keyIds []string
	for k, _ := range tokenKeys {
		keyIds = append(keyIds, k)
	}
	sort.Strings(keyIds)
	log.Printf("Enabled token keys: %v", keyIds)

	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	var statsAllowedIps map[string]bool
	if statsAllowed == "" {
		log.Printf("No IPs configured for the stats endpoint, only allowing access from 127.0.0.1")
		statsAllowedIps = map[string]bool{
			"127.0.0.1": true,
		}
	} else {
		log.Printf("Only allowing access to the stats endpoing from %s", statsAllowed)
		statsAllowedIps = make(map[string]bool)
		for _, ip := range strings.Split(statsAllowed, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				statsAllowedIps[ip] = true
			}
		}
	}

	country, _ := config.GetString("app", "country")
	country = strings.ToUpper(country)
	if signaling.IsValidCountry(country) {
		log.Printf("Sending %s as country information", country)
	} else if country != "" {
		return nil, fmt.Errorf("Invalid country: %s", country)
	} else {
		log.Printf("Not sending country information")
	}

	result := &ProxyServer{
		version: version,
		country: country,

		nats: nats,

		shutdownChannel: make(chan bool, 1),

		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
		},

		statsAllowedIps: statsAllowedIps,

		cookie:   securecookie.New([]byte(hashKey), blockBytes).MaxAge(0),
		sessions: make(map[uint64]*ProxySession),

		clients:   make(map[string]signaling.McuClient),
		clientIds: make(map[string]string),
	}

	result.setTokenKeys(tokenKeys)
	result.upgrader.CheckOrigin = result.checkOrigin

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

	r.HandleFunc("/proxy", result.setCommonHeaders(result.proxyHandler)).Methods("GET")
	r.HandleFunc("/stats", result.setCommonHeaders(result.validateStatsRequest(result.statsHandler))).Methods("GET")
	return result, nil
}

func (s *ProxyServer) checkOrigin(r *http.Request) bool {
	// We allow any Origin to connect to the service.
	return true
}

func (s *ProxyServer) setTokenKeys(keys map[string]*rsa.PublicKey) {
	s.tokenKeys.Store(keys)
}

func (s *ProxyServer) getTokenKeys() map[string]*rsa.PublicKey {
	return s.tokenKeys.Load().(map[string]*rsa.PublicKey)
}

func (s *ProxyServer) Start(config *goconf.ConfigFile) error {
	s.url, _ = config.GetString("mcu", "url")
	if s.url == "" {
		return fmt.Errorf("No MCU server url configured")
	}

	mcuType, _ := config.GetString("mcu", "type")
	if mcuType == "" {
		mcuType = signaling.McuTypeDefault
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	var err error
	var mcu signaling.Mcu
	mcuRetry := initialMcuRetry
	mcuRetryTimer := time.NewTimer(mcuRetry)
	for {
		switch mcuType {
		case signaling.McuTypeJanus:
			mcu, err = signaling.NewMcuJanus(s.url, config, s.nats)
		default:
			return fmt.Errorf("Unsupported MCU type: %s", mcuType)
		}
		if err == nil {
			mcu.SetOnConnected(s.onMcuConnected)
			mcu.SetOnDisconnected(s.onMcuDisconnected)
			err = mcu.Start()
			if err != nil {
				log.Printf("Could not create %s MCU at %s: %s", mcuType, s.url, err)
			}
		}
		if err == nil {
			break
		}

		log.Printf("Could not initialize %s MCU at %s (%s) will retry in %s", mcuType, s.url, err, mcuRetry)
		mcuRetryTimer.Reset(mcuRetry)
		select {
		case <-interrupt:
			return fmt.Errorf("Cancelled")
		case <-mcuRetryTimer.C:
			// Retry connection
			mcuRetry = mcuRetry * 2
			if mcuRetry > maxMcuRetry {
				mcuRetry = maxMcuRetry
			}
		}
	}

	s.mcu = mcu

	go s.run()

	return nil
}

func (s *ProxyServer) run() {
	updateLoadTicker := time.NewTicker(updateLoadInterval)
	expireSessionsTicker := time.NewTicker(expireSessionsInterval)
loop:
	for {
		select {
		case <-updateLoadTicker.C:
			if atomic.LoadUint32(&s.stopped) != 0 {
				break loop
			}
			s.updateLoad()
		case <-expireSessionsTicker.C:
			if atomic.LoadUint32(&s.stopped) != 0 {
				break loop
			}
			s.expireSessions()
		}
	}
}

func (s *ProxyServer) updateLoad() {
	// TODO: Take maximum bandwidth of clients into account when calculating
	// load (screensharing requires more than regular audio/video).
	load := s.GetClientCount()
	if load == atomic.LoadInt64(&s.load) {
		return
	}

	atomic.StoreInt64(&s.load, load)
	if atomic.LoadUint32(&s.shutdownScheduled) != 0 {
		// Server is scheduled to shutdown, no need to update clients with current load.
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "update-load",
			Load: load,
		},
	}

	s.IterateSessions(func(session *ProxySession) {
		session.sendMessage(msg)
	})
}

func (s *ProxyServer) getExpiredSessions() []*ProxySession {
	var expired []*ProxySession
	s.IterateSessions(func(session *ProxySession) {
		if session.IsExpired() {
			expired = append(expired, session)
		}
	})
	return expired
}

func (s *ProxyServer) expireSessions() {
	expired := s.getExpiredSessions()
	if len(expired) == 0 {
		return
	}

	s.sessionsLock.Lock()
	defer s.sessionsLock.Unlock()
	for _, session := range expired {
		if !session.IsExpired() {
			// Session was used while waiting for the lock.
			continue
		}

		log.Printf("Delete expired session %s", session.PublicId())
		s.deleteSessionLocked(session.Sid())
	}
}

func (s *ProxyServer) Stop() {
	if !atomic.CompareAndSwapUint32(&s.stopped, 0, 1) {
		return
	}

	s.mcu.Stop()
}

func (s *ProxyServer) ShutdownChannel() chan bool {
	return s.shutdownChannel
}

func (s *ProxyServer) ScheduleShutdown() {
	if !atomic.CompareAndSwapUint32(&s.shutdownScheduled, 0, 1) {
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "shutdown-scheduled",
		},
	}
	s.IterateSessions(func(session *ProxySession) {
		session.sendMessage(msg)
	})

	if s.GetClientCount() == 0 {
		go func() {
			s.shutdownChannel <- true
		}()
	}
}

func (s *ProxyServer) Reload(config *goconf.ConfigFile) {
	tokenKeys := make(map[string]*rsa.PublicKey)
	options, _ := config.GetOptions("tokens")
	for _, id := range options {
		filename, _ := config.GetString("tokens", id)
		if filename == "" {
			log.Printf("No filename given for token %s, ignoring", id)
			continue
		}

		keyData, err := ioutil.ReadFile(filename)
		if err != nil {
			log.Printf("Could not read public key from %s, ignoring: %s", filename, err)
			continue
		}
		key, err := jwt.ParseRSAPublicKeyFromPEM(keyData)
		if err != nil {
			log.Printf("Could not parse public key from %s, ignoring: %s", filename, err)
			continue
		}

		tokenKeys[id] = key
	}

	if len(tokenKeys) == 0 {
		log.Printf("No token keys loaded")
	} else {
		var keyIds []string
		for k, _ := range tokenKeys {
			keyIds = append(keyIds, k)
		}
		sort.Strings(keyIds)
		log.Printf("Enabled token keys: %v", keyIds)
	}
	s.setTokenKeys(tokenKeys)
}

func (s *ProxyServer) setCommonHeaders(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nextcloud-spreed-signaling-proxy/"+s.version)
		f(w, r)
	}
}

func getRealUserIP(r *http.Request) string {
	// Note this function assumes it is running behind a trusted proxy, so
	// the headers can be trusted.
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// Result could be a list "clientip, proxy1, proxy2", so only use first element.
		if pos := strings.Index(ip, ","); pos >= 0 {
			ip = strings.TrimSpace(ip[:pos])
		}
		return ip
	}

	return r.RemoteAddr
}

func (s *ProxyServer) proxyHandler(w http.ResponseWriter, r *http.Request) {
	addr := getRealUserIP(r)
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Could not upgrade request from %s: %s", addr, err)
		return
	}

	client, err := NewProxyClient(s, conn, addr)
	if err != nil {
		log.Printf("Could not create client for %s: %s", addr, err)
		return
	}

	client.OnClosed = s.clientClosed
	client.OnMessageReceived = func(c *signaling.Client, data []byte) {
		s.processMessage(client, data)
	}
	client.OnRTTReceived = func(c *signaling.Client, rtt time.Duration) {
		if session := client.GetSession(); session != nil {
			session.MarkUsed()
		}
	}

	go client.WritePump()
	go client.ReadPump()
}

func (s *ProxyServer) clientClosed(client *signaling.Client) {
	log.Printf("Connection from %s closed", client.RemoteAddr())
}

func (s *ProxyServer) onMcuConnected() {
	log.Printf("Connection to %s established", s.url)
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "backend-connected",
		},
	}

	s.IterateSessions(func(session *ProxySession) {
		session.sendMessage(msg)
	})
}

func (s *ProxyServer) onMcuDisconnected() {
	if atomic.LoadUint32(&s.stopped) != 0 {
		// Shutting down, no need to notify.
		return
	}

	log.Printf("Connection to %s lost", s.url)
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "backend-disconnected",
		},
	}

	s.IterateSessions(func(session *ProxySession) {
		session.sendMessage(msg)
		session.NotifyDisconnected()
	})
}

func (s *ProxyServer) sendCurrentLoad(session *ProxySession) {
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "update-load",
			Load: atomic.LoadInt64(&s.load),
		},
	}
	session.sendMessage(msg)
}

func (s *ProxyServer) sendShutdownScheduled(session *ProxySession) {
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "shutdown-scheduled",
		},
	}
	session.sendMessage(msg)
}

func (s *ProxyServer) processMessage(client *ProxyClient, data []byte) {
	if proxyDebugMessages {
		log.Printf("Message: %s", string(data))
	}
	var message signaling.ProxyClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Error decoding message from client %s: %v", session.PublicId(), err)
		} else {
			log.Printf("Error decoding message from %s: %v", client.RemoteAddr(), err)
		}
		client.SendError(signaling.InvalidFormat)
		return
	}

	if err := message.CheckValid(); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
		} else {
			log.Printf("Invalid message %+v from %s: %v", message, client.RemoteAddr(), err)
		}
		client.SendMessage(message.NewErrorServerMessage(signaling.InvalidFormat))
		return
	}

	session := client.GetSession()
	if session == nil {
		if message.Type != "hello" {
			client.SendMessage(message.NewErrorServerMessage(signaling.HelloExpected))
			return
		}

		var session *ProxySession
		if resumeId := message.Hello.ResumeId; resumeId != "" {
			var data signaling.SessionIdData
			if s.cookie.Decode("session", resumeId, &data) == nil {
				session = s.GetSession(data.Sid)
			}

			if session == nil {
				client.SendMessage(message.NewErrorServerMessage(signaling.NoSuchSession))
				return
			}

			log.Printf("Resumed session %s", session.PublicId())
			if atomic.LoadUint32(&s.shutdownScheduled) != 0 {
				s.sendShutdownScheduled(session)
			} else {
				s.sendCurrentLoad(session)
			}
		} else {
			var err error
			if session, err = s.NewSession(message.Hello); err != nil {
				if e, ok := err.(*signaling.Error); ok {
					client.SendMessage(message.NewErrorServerMessage(e))
				} else {
					client.SendMessage(message.NewWrappedErrorServerMessage(err))
				}
				return
			}
		}

		prev := session.SetClient(client)
		if prev != nil {
			msg := &signaling.ProxyServerMessage{
				Type: "bye",
				Bye: &signaling.ByeProxyServerMessage{
					Reason: "session_resumed",
				},
			}
			prev.SendMessage(msg)
		}
		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "hello",
			Hello: &signaling.HelloProxyServerMessage{
				Version:   signaling.HelloVersion,
				SessionId: session.PublicId(),
				Server: &signaling.HelloServerMessageServer{
					Version: s.version,
					Country: s.country,
				},
			},
		}
		client.SendMessage(response)
		if atomic.LoadUint32(&s.shutdownScheduled) != 0 {
			s.sendShutdownScheduled(session)
		} else {
			s.sendCurrentLoad(session)
		}
		return
	}

	ctx := context.WithValue(context.Background(), ContextKeySession, session)
	session.MarkUsed()

	switch message.Type {
	case "command":
		s.processCommand(ctx, client, session, &message)
	case "payload":
		s.processPayload(ctx, client, session, &message)
	default:
		session.sendMessage(message.NewErrorServerMessage(UnsupportedMessage))
	}
}

type emptyInitiator struct{}

func (i *emptyInitiator) Country() string {
	return ""
}

func (s *ProxyServer) processCommand(ctx context.Context, client *ProxyClient, session *ProxySession, message *signaling.ProxyClientMessage) {
	cmd := message.Command
	switch cmd.Type {
	case "create-publisher":
		if atomic.LoadUint32(&s.shutdownScheduled) != 0 {
			session.sendMessage(message.NewErrorServerMessage(ShutdownScheduled))
			return
		}

		id := uuid.New().String()
		publisher, err := s.mcu.NewPublisher(ctx, session, id, cmd.StreamType, &emptyInitiator{})
		if err == context.DeadlineExceeded {
			log.Printf("Timeout while creating %s publisher %s for %s", cmd.StreamType, id, session.PublicId())
			session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingPublisher))
			return
		} else if err != nil {
			log.Printf("Error while creating %s publisher %s for %s: %s", cmd.StreamType, id, session.PublicId(), err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		log.Printf("Created %s publisher %s as %s", cmd.StreamType, publisher.Id(), id)
		session.StorePublisher(ctx, id, publisher)
		s.StoreClient(id, publisher)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: id,
			},
		}
		session.sendMessage(response)
	case "create-subscriber":
		id := uuid.New().String()
		publisherId := cmd.PublisherId
		subscriber, err := s.mcu.NewSubscriber(ctx, session, publisherId, cmd.StreamType)
		if err == context.DeadlineExceeded {
			log.Printf("Timeout while creating %s subscriber on %s for %s", cmd.StreamType, publisherId, session.PublicId())
			session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingSubscriber))
			return
		} else if err != nil {
			log.Printf("Error while creating %s subscriber on %s for %s: %s", cmd.StreamType, publisherId, session.PublicId(), err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		log.Printf("Created %s subscriber %s as %s", cmd.StreamType, subscriber.Id(), id)
		session.StoreSubscriber(ctx, id, subscriber)
		s.StoreClient(id, subscriber)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: id,
			},
		}
		session.sendMessage(response)
	case "delete-publisher":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		if session.DeletePublisher(client) == "" {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		s.DeleteClient(cmd.ClientId, client)

		go func() {
			log.Printf("Closing %s publisher %s as %s", client.StreamType(), client.Id(), cmd.ClientId)
			client.Close(context.Background())
		}()

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: cmd.ClientId,
			},
		}
		session.sendMessage(response)
	case "delete-subscriber":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		subscriber, ok := client.(signaling.McuSubscriber)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		if session.DeleteSubscriber(subscriber) == "" {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		s.DeleteClient(cmd.ClientId, client)

		go func() {
			log.Printf("Closing %s subscriber %s as %s", client.StreamType(), client.Id(), cmd.ClientId)
			client.Close(context.Background())
		}()

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: cmd.ClientId,
			},
		}
		session.sendMessage(response)
	default:
		log.Printf("Unsupported command %+v", message.Command)
		session.sendMessage(message.NewErrorServerMessage(UnsupportedCommand))
	}
}

func (s *ProxyServer) processPayload(ctx context.Context, client *ProxyClient, session *ProxySession, message *signaling.ProxyClientMessage) {
	payload := message.Payload
	mcuClient := s.GetClient(payload.ClientId)
	if mcuClient == nil {
		session.sendMessage(message.NewErrorServerMessage(UnknownClient))
		return
	}

	var mcuData *signaling.MessageClientMessageData
	switch payload.Type {
	case "offer":
		fallthrough
	case "answer":
		fallthrough
	case "candidate":
		mcuData = &signaling.MessageClientMessageData{
			Type:    payload.Type,
			Payload: payload.Payload,
		}
	case "endOfCandidates":
		// Ignore but confirm, not passed along to Janus anyway.
		session.sendMessage(&signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "payload",
			Payload: &signaling.PayloadProxyServerMessage{
				Type:     payload.Type,
				ClientId: payload.ClientId,
			},
		})
		return
	case "requestoffer":
		fallthrough
	case "sendoffer":
		mcuData = &signaling.MessageClientMessageData{
			Type: payload.Type,
		}
	default:
		session.sendMessage(message.NewErrorServerMessage(UnsupportedPayload))
		return
	}

	mcuClient.SendMessage(ctx, nil, mcuData, func(err error, response map[string]interface{}) {
		var responseMsg *signaling.ProxyServerMessage
		if err != nil {
			log.Printf("Error sending %s to %s client %s: %s", mcuData, mcuClient.StreamType(), payload.ClientId, err)
			responseMsg = message.NewWrappedErrorServerMessage(err)
		} else {
			responseMsg = &signaling.ProxyServerMessage{
				Id:   message.Id,
				Type: "payload",
				Payload: &signaling.PayloadProxyServerMessage{
					Type:     payload.Type,
					ClientId: payload.ClientId,
					Payload:  response,
				},
			}
		}

		session.sendMessage(responseMsg)
	})
}

func (s *ProxyServer) NewSession(hello *signaling.HelloProxyClientMessage) (*ProxySession, error) {
	if proxyDebugMessages {
		log.Printf("Hello: %+v", hello)
	}

	token, err := jwt.ParseWithClaims(hello.Token, &signaling.TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			log.Printf("Unexpected signing method: %v", token.Header["alg"])
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}

		claims, ok := token.Claims.(*signaling.TokenClaims)
		if !ok {
			log.Printf("Unsupported claims type: %+v", token.Claims)
			return nil, fmt.Errorf("Unsupported claims type")
		}

		tokenKeys := s.getTokenKeys()
		publicKey := tokenKeys[claims.Issuer]
		if publicKey == nil {
			log.Printf("Issuer %s is not supported", claims.Issuer)
			return nil, fmt.Errorf("No key found for issuer")
		}
		return publicKey, nil
	})
	if err != nil {
		return nil, TokenAuthFailed
	}

	claims, ok := token.Claims.(*signaling.TokenClaims)
	if !ok || !token.Valid {
		return nil, TokenAuthFailed
	}

	minIssuedAt := time.Now().Add(-maxTokenAge)
	if issuedAt := time.Unix(claims.IssuedAt, 0); issuedAt.Before(minIssuedAt) {
		return nil, TokenExpired
	}

	sid := atomic.AddUint64(&s.sid, 1)
	for sid == 0 {
		sid = atomic.AddUint64(&s.sid, 1)
	}

	sessionIdData := &signaling.SessionIdData{
		Sid:     sid,
		Created: time.Now(),
	}

	encoded, err := s.cookie.Encode("session", sessionIdData)
	if err != nil {
		return nil, err
	}

	log.Printf("Created session %s for %+v", encoded, claims)
	session := NewProxySession(s, sid, encoded)
	s.StoreSession(sid, session)
	return session, nil
}

func (s *ProxyServer) StoreSession(id uint64, session *ProxySession) {
	s.sessionsLock.Lock()
	defer s.sessionsLock.Unlock()
	s.sessions[id] = session
}

func (s *ProxyServer) GetSession(id uint64) *ProxySession {
	s.sessionsLock.RLock()
	defer s.sessionsLock.RUnlock()
	return s.sessions[id]
}

func (s *ProxyServer) GetSessionsCount() int64 {
	s.sessionsLock.RLock()
	defer s.sessionsLock.RUnlock()
	return int64(len(s.sessions))
}

func (s *ProxyServer) IterateSessions(f func(*ProxySession)) {
	s.sessionsLock.RLock()
	defer s.sessionsLock.RUnlock()

	for _, session := range s.sessions {
		f(session)
	}
}

func (s *ProxyServer) DeleteSession(id uint64) {
	s.sessionsLock.Lock()
	defer s.sessionsLock.Unlock()
	s.deleteSessionLocked(id)
}

func (s *ProxyServer) deleteSessionLocked(id uint64) {
	delete(s.sessions, id)
}

func (s *ProxyServer) StoreClient(id string, client signaling.McuClient) {
	s.clientsLock.Lock()
	defer s.clientsLock.Unlock()
	s.clients[id] = client
	s.clientIds[client.Id()] = id
}

func (s *ProxyServer) DeleteClient(id string, client signaling.McuClient) {
	s.clientsLock.Lock()
	defer s.clientsLock.Unlock()
	delete(s.clients, id)
	delete(s.clientIds, client.Id())

	if len(s.clients) == 0 && atomic.LoadUint32(&s.shutdownScheduled) != 0 {
		go func() {
			s.shutdownChannel <- true
		}()
	}
}

func (s *ProxyServer) GetClientCount() int64 {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return int64(len(s.clients))
}

func (s *ProxyServer) GetClient(id string) signaling.McuClient {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return s.clients[id]
}

func (s *ProxyServer) GetClientId(client signaling.McuClient) string {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return s.clientIds[client.Id()]
}

func (s *ProxyServer) getStats() map[string]interface{} {
	result := map[string]interface{}{
		"sessions": s.GetSessionsCount(),
		"mcu":      s.mcu.GetStats(),
	}
	return result
}

func (s *ProxyServer) validateStatsRequest(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := getRealUserIP(r)
		if strings.Contains(addr, ":") {
			if host, _, err := net.SplitHostPort(addr); err == nil {
				addr = host
			}
		}
		if !s.statsAllowedIps[addr] {
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}

		f(w, r)
	}
}

func (s *ProxyServer) statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := s.getStats()
	statsData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		log.Printf("Could not serialize stats %+v: %s", stats, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(statsData)
}
