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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	runtimepprof "runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/notedit/janus-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	// Buffer sizes when reading/writing websocket connections.
	websocketReadBufferSize  = 4096
	websocketWriteBufferSize = 4096

	initialMcuRetry = time.Second
	maxMcuRetry     = time.Second * 16

	// MCU requests will be cancelled if they take too long.
	defaultMcuTimeoutSeconds = 10

	updateLoadInterval     = time.Second
	expireSessionsInterval = 10 * time.Second

	// Maximum age a token may have to prevent reuse of old tokens.
	maxTokenAge = 5 * time.Minute

	// Allow time differences of up to one minute between server and proxy.
	tokenLeeway = time.Minute

	remotePublisherTimeout = 5 * time.Second

	ProxyFeatureRemoteStreams = "remote-streams"
)

var (
	defaultProxyFeatures = []string{
		ProxyFeatureRemoteStreams,
	}
)

type ContextKey string

var (
	ContextKeySession = ContextKey("session")

	TimeoutCreatingPublisher      = signaling.NewError("timeout", "Timeout creating publisher.")
	TimeoutCreatingSubscriber     = signaling.NewError("timeout", "Timeout creating subscriber.")
	TokenAuthFailed               = signaling.NewError("auth_failed", "The token could not be authenticated.")
	TokenExpired                  = signaling.NewError("token_expired", "The token is expired.")
	TokenNotValidYet              = signaling.NewError("token_not_valid_yet", "The token is not valid yet.")
	UnknownClient                 = signaling.NewError("unknown_client", "Unknown client id given.")
	UnsupportedCommand            = signaling.NewError("bad_request", "Unsupported command received.")
	UnsupportedMessage            = signaling.NewError("bad_request", "Unsupported message received.")
	UnsupportedPayload            = signaling.NewError("unsupported_payload", "Unsupported payload type.")
	ShutdownScheduled             = signaling.NewError("shutdown_scheduled", "The server is scheduled to shutdown.")
	RemoteSubscribersNotSupported = signaling.NewError("unsupported_subscriber", "Remote subscribers are not supported.")
)

type ProxyServer struct {
	version        string
	country        string
	welcomeMessage string
	welcomeMsg     *signaling.WelcomeServerMessage
	config         *goconf.ConfigFile
	mcuTimeout     time.Duration
	logger         signaling.Logger

	url     string
	mcu     signaling.Mcu
	stopped atomic.Bool
	load    atomic.Uint64

	maxIncoming     api.AtomicBandwidth
	currentIncoming api.AtomicBandwidth
	maxOutgoing     api.AtomicBandwidth
	currentOutgoing api.AtomicBandwidth

	shutdownChannel   chan struct{}
	shutdownScheduled atomic.Bool

	upgrader websocket.Upgrader

	tokens          ProxyTokens
	statsAllowedIps atomic.Pointer[signaling.AllowedIps]
	trustedProxies  atomic.Pointer[signaling.AllowedIps]

	sid          atomic.Uint64
	cookie       *signaling.SessionIdCodec
	sessionsLock sync.RWMutex
	// +checklocks:sessionsLock
	sessions map[uint64]*ProxySession

	clientsLock sync.RWMutex
	// +checklocks:clientsLock
	clients map[string]signaling.McuClient
	// +checklocks:clientsLock
	clientIds map[string]string

	tokenId               string
	tokenKey              *rsa.PrivateKey
	remoteTlsConfig       *tls.Config // +checklocksignore: Only written to from constructor.
	remoteHostname        string
	remoteConnectionsLock sync.Mutex
	// +checklocks:remoteConnectionsLock
	remoteConnections map[string]*RemoteConnection
	// +checklocks:remoteConnectionsLock
	remotePublishers map[string]map[*proxyRemotePublisher]bool
}

func IsPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := IP.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}
	return false
}

func GetLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && IsPublicIP(ipnet.IP) {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", nil
}

func getTargetBandwidths(logger signaling.Logger, config *goconf.ConfigFile) (api.Bandwidth, api.Bandwidth) {
	maxIncomingValue, _ := config.GetInt("bandwidth", "incoming")
	if maxIncomingValue < 0 {
		maxIncomingValue = 0
	}
	maxIncoming := api.BandwidthFromMegabits(uint64(maxIncomingValue))
	if maxIncoming > 0 {
		logger.Printf("Target bandwidth for incoming streams: %s", maxIncoming)
	} else {
		logger.Printf("Target bandwidth for incoming streams: unlimited")
	}

	maxOutgoingValue, _ := config.GetInt("bandwidth", "outgoing")
	if maxOutgoingValue < 0 {
		maxOutgoingValue = 0
	}
	maxOutgoing := api.BandwidthFromMegabits(uint64(maxOutgoingValue))
	if maxOutgoing > 0 {
		logger.Printf("Target bandwidth for outgoing streams: %s", maxOutgoing)
	} else {
		logger.Printf("Target bandwidth for outgoing streams: unlimited")
	}

	return maxIncoming, maxOutgoing
}

func NewProxyServer(ctx context.Context, r *mux.Router, version string, config *goconf.ConfigFile) (*ProxyServer, error) {
	logger := signaling.LoggerFromContext(ctx)
	hashKey := make([]byte, 64)
	if _, err := rand.Read(hashKey); err != nil {
		return nil, fmt.Errorf("could not generate random hash key: %s", err)
	}

	blockKey := make([]byte, 32)
	if _, err := rand.Read(blockKey); err != nil {
		return nil, fmt.Errorf("could not generate random block key: %s", err)
	}

	sessionIds, err := signaling.NewSessionIdCodec(hashKey, blockKey)
	if err != nil {
		return nil, fmt.Errorf("error creating session id codec: %w", err)
	}

	var tokens ProxyTokens
	tokenType, _ := config.GetString("app", "tokentype")
	if tokenType == "" {
		tokenType = TokenTypeDefault
	}

	switch tokenType {
	case TokenTypeEtcd:
		tokens, err = NewProxyTokensEtcd(logger, config)
	case TokenTypeStatic:
		tokens, err = NewProxyTokensStatic(logger, config)
	default:
		return nil, fmt.Errorf("unsupported token type configured: %s", tokenType)
	}
	if err != nil {
		return nil, err
	}

	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	statsAllowedIps, err := signaling.ParseAllowedIps(statsAllowed)
	if err != nil {
		return nil, err
	}

	if !statsAllowedIps.Empty() {
		logger.Printf("Only allowing access to the stats endpoint from %s", statsAllowed)
	} else {
		statsAllowedIps = signaling.DefaultAllowedIps()
		logger.Printf("No IPs configured for the stats endpoint, only allowing access from %s", statsAllowedIps)
	}

	trustedProxies, _ := config.GetString("app", "trustedproxies")
	trustedProxiesIps, err := signaling.ParseAllowedIps(trustedProxies)
	if err != nil {
		return nil, err
	}

	if !trustedProxiesIps.Empty() {
		logger.Printf("Trusted proxies: %s", trustedProxiesIps)
	} else {
		trustedProxiesIps = signaling.DefaultTrustedProxies
		logger.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
	}

	country, _ := config.GetString("app", "country")
	country = strings.ToUpper(country)
	if signaling.IsValidCountry(country) {
		logger.Printf("Sending %s as country information", country)
	} else if country != "" {
		return nil, fmt.Errorf("invalid country: %s", country)
	} else {
		logger.Printf("Not sending country information")
	}

	welcome := map[string]string{
		"nextcloud-spreed-signaling-proxy": "Welcome",
		"version":                          version,
	}
	welcomeMessage, err := json.Marshal(welcome)
	if err != nil {
		// Should never happen.
		return nil, err
	}

	tokenId, _ := config.GetString("app", "token_id")
	var tokenKey *rsa.PrivateKey
	var remoteHostname string
	var remoteTlsConfig *tls.Config
	if tokenId != "" {
		tokenKeyFilename, _ := config.GetString("app", "token_key")
		if tokenKeyFilename == "" {
			return nil, fmt.Errorf("no token key configured")
		}
		tokenKeyData, err := os.ReadFile(tokenKeyFilename)
		if err != nil {
			return nil, fmt.Errorf("could not read private key from %s: %s", tokenKeyFilename, err)
		}
		tokenKey, err = jwt.ParseRSAPrivateKeyFromPEM(tokenKeyData)
		if err != nil {
			return nil, fmt.Errorf("could not parse private key from %s: %s", tokenKeyFilename, err)
		}
		logger.Printf("Using \"%s\" as token id for remote streams", tokenId)

		remoteHostname, _ = config.GetString("app", "hostname")
		if remoteHostname == "" {
			remoteHostname, err = GetLocalIP()
			if err != nil {
				return nil, fmt.Errorf("could not get local ip: %w", err)
			}
		}
		if remoteHostname == "" {
			logger.Printf("WARNING: Could not determine hostname for remote streams, will be disabled. Please configure manually.")
		} else {
			logger.Printf("Using \"%s\" as hostname for remote streams", remoteHostname)
		}

		skipverify, _ := config.GetBool("backend", "skipverify")
		if skipverify {
			logger.Println("WARNING: Remote stream requests verification is disabled!")
			remoteTlsConfig = &tls.Config{
				InsecureSkipVerify: skipverify,
			}
		}
	} else {
		logger.Printf("No token id configured, remote streams will be disabled")
	}

	maxIncoming, maxOutgoing := getTargetBandwidths(logger, config)

	mcuTimeoutSeconds, _ := config.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second

	result := &ProxyServer{
		version:        version,
		country:        country,
		welcomeMessage: string(welcomeMessage) + "\n",
		welcomeMsg: &signaling.WelcomeServerMessage{
			Version:  version,
			Country:  country,
			Features: defaultProxyFeatures,
		},
		config:     config,
		mcuTimeout: mcuTimeout,
		logger:     logger,

		shutdownChannel: make(chan struct{}),

		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
			Subprotocols: []string{
				signaling.JanusEventsSubprotocol,
			},
		},

		tokens: tokens,

		cookie:   sessionIds,
		sessions: make(map[uint64]*ProxySession),

		clients:   make(map[string]signaling.McuClient),
		clientIds: make(map[string]string),

		tokenId:           tokenId,
		tokenKey:          tokenKey,
		remoteTlsConfig:   remoteTlsConfig,
		remoteHostname:    remoteHostname,
		remoteConnections: make(map[string]*RemoteConnection),
		remotePublishers:  make(map[string]map[*proxyRemotePublisher]bool),
	}

	result.maxIncoming.Store(maxIncoming)
	result.maxOutgoing.Store(maxOutgoing)
	result.statsAllowedIps.Store(statsAllowedIps)
	result.trustedProxies.Store(trustedProxiesIps)
	result.upgrader.CheckOrigin = result.checkOrigin

	statsLoadCurrent.Set(0)

	if debug, _ := config.GetBool("app", "debug"); debug {
		logger.Println("Installing debug handlers in \"/debug/pprof\"")
		s := r.PathPrefix("/debug/pprof").Subrouter()
		s.HandleFunc("", result.setCommonHeaders(result.validateStatsRequest(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/debug/pprof/", http.StatusTemporaryRedirect)
		})))
		s.HandleFunc("/", result.setCommonHeaders(result.validateStatsRequest(pprof.Index)))
		s.HandleFunc("/cmdline", result.setCommonHeaders(result.validateStatsRequest(pprof.Cmdline)))
		s.HandleFunc("/profile", result.setCommonHeaders(result.validateStatsRequest(pprof.Profile)))
		s.HandleFunc("/symbol", result.setCommonHeaders(result.validateStatsRequest(pprof.Symbol)))
		s.HandleFunc("/trace", result.setCommonHeaders(result.validateStatsRequest(pprof.Trace)))
		for _, profile := range runtimepprof.Profiles() {
			name := profile.Name()
			handler := pprof.Handler(name)
			s.HandleFunc("/"+name, result.setCommonHeaders(result.validateStatsRequest(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
			})))
		}
	}

	r.HandleFunc("/welcome", result.setCommonHeaders(result.welcomeHandler)).Methods("GET")
	r.HandleFunc("/proxy", result.setCommonHeaders(result.proxyHandler)).Methods("GET")
	r.HandleFunc("/stats", result.setCommonHeaders(result.validateStatsRequest(result.statsHandler))).Methods("GET")
	r.HandleFunc("/metrics", result.setCommonHeaders(result.validateStatsRequest(result.metricsHandler))).Methods("GET")
	return result, nil
}

func (s *ProxyServer) checkOrigin(r *http.Request) bool {
	// We allow any Origin to connect to the service.
	return true
}

func (s *ProxyServer) Start(config *goconf.ConfigFile) error {
	s.url, _ = signaling.GetStringOptionWithEnv(config, "mcu", "url")
	if s.url == "" {
		return fmt.Errorf("no MCU server url configured")
	}

	mcuType, _ := config.GetString("mcu", "type")
	if mcuType == "" {
		mcuType = signaling.McuTypeDefault
	}

	backoff, err := signaling.NewExponentialBackoff(initialMcuRetry, maxMcuRetry)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var mcu signaling.Mcu
	for {
		switch mcuType {
		case signaling.McuTypeJanus:
			mcu, err = signaling.NewMcuJanus(ctx, s.url, config)
			if err == nil {
				signaling.RegisterJanusMcuStats()
			}
		default:
			return fmt.Errorf("unsupported MCU type: %s", mcuType)
		}
		if err == nil {
			mcu.SetOnConnected(s.onMcuConnected)
			mcu.SetOnDisconnected(s.onMcuDisconnected)
			err = mcu.Start(ctx)
			if err != nil {
				s.logger.Printf("Could not create %s MCU at %s: %s", mcuType, s.url, err)
			}
		}
		if err == nil {
			break
		}

		s.logger.Printf("Could not initialize %s MCU at %s (%s) will retry in %s", mcuType, s.url, err, backoff.NextWait())
		backoff.Wait(ctx)
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled")
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
			if s.stopped.Load() {
				break loop
			}
			s.updateLoad()
		case <-expireSessionsTicker.C:
			if s.stopped.Load() {
				break loop
			}
			s.expireSessions()
		}
	}
}

func (s *ProxyServer) newLoadEvent(load uint64, incoming api.Bandwidth, outgoing api.Bandwidth) *signaling.ProxyServerMessage {
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "update-load",
			Load: load,
		},
	}
	maxIncoming := s.maxIncoming.Load()
	maxOutgoing := s.maxOutgoing.Load()
	if maxIncoming > 0 || maxOutgoing > 0 || incoming != 0 || outgoing != 0 {
		msg.Event.Bandwidth = &signaling.EventProxyServerBandwidth{
			Received: incoming,
			Sent:     outgoing,
		}
		if maxIncoming > 0 {
			value := float64(incoming) / float64(maxIncoming) * 100
			msg.Event.Bandwidth.Incoming = &value
		}
		if maxOutgoing > 0 {
			value := float64(outgoing) / float64(maxOutgoing) * 100
			msg.Event.Bandwidth.Outgoing = &value
		}
	}
	return msg
}

func (s *ProxyServer) updateLoad() {
	load, incoming, outgoing := s.GetClientsLoad()
	oldLoad := s.load.Swap(load)
	oldIncoming := s.currentIncoming.Swap(incoming)
	oldOutgoing := s.currentOutgoing.Swap(outgoing)
	if oldLoad == load && oldIncoming == incoming && oldOutgoing == outgoing {
		return
	}

	statsLoadCurrent.Set(float64(load))
	s.sendLoadToAll(load, incoming, outgoing)
}

func (s *ProxyServer) sendLoadToAll(load uint64, incoming api.Bandwidth, outgoing api.Bandwidth) {
	if s.shutdownScheduled.Load() {
		// Server is scheduled to shutdown, no need to update clients with current load.
		return
	}

	msg := s.newLoadEvent(load, incoming, outgoing)
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

		s.logger.Printf("Delete expired session %s", session.PublicId())
		s.deleteSessionLocked(session.Sid())
	}
}

func (s *ProxyServer) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}

	if s.mcu != nil {
		s.mcu.Stop()
	}
	s.tokens.Close()
}

func (s *ProxyServer) ShutdownChannel() <-chan struct{} {
	return s.shutdownChannel
}

func (s *ProxyServer) ScheduleShutdown() {
	if !s.shutdownScheduled.CompareAndSwap(false, true) {
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

	if !s.HasClients() {
		go close(s.shutdownChannel)
	}
}

func (s *ProxyServer) Reload(config *goconf.ConfigFile) {
	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	if statsAllowedIps, err := signaling.ParseAllowedIps(statsAllowed); err == nil {
		if !statsAllowedIps.Empty() {
			s.logger.Printf("Only allowing access to the stats endpoint from %s", statsAllowed)
		} else {
			statsAllowedIps = signaling.DefaultAllowedIps()
			s.logger.Printf("No IPs configured for the stats endpoint, only allowing access from %s", statsAllowedIps)
		}
		s.statsAllowedIps.Store(statsAllowedIps)
	} else {
		s.logger.Printf("Error parsing allowed stats ips from \"%s\": %s", statsAllowedIps, err)
	}

	trustedProxies, _ := config.GetString("app", "trustedproxies")
	if trustedProxiesIps, err := signaling.ParseAllowedIps(trustedProxies); err == nil {
		if !trustedProxiesIps.Empty() {
			s.logger.Printf("Trusted proxies: %s", trustedProxiesIps)
		} else {
			trustedProxiesIps = signaling.DefaultTrustedProxies
			s.logger.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
		}
		s.trustedProxies.Store(trustedProxiesIps)
	} else {
		s.logger.Printf("Error parsing trusted proxies from \"%s\": %s", trustedProxies, err)
	}

	maxIncoming, maxOutgoing := getTargetBandwidths(s.logger, config)
	oldIncoming := s.maxIncoming.Swap(maxIncoming)
	oldOutgoing := s.maxOutgoing.Swap(maxOutgoing)
	if oldIncoming != maxIncoming || oldOutgoing != maxOutgoing {
		// Notify sessions about updated load / bandwidth usage.
		go s.sendLoadToAll(s.load.Load(), s.currentIncoming.Load(), s.currentOutgoing.Load())
	}

	s.tokens.Reload(config)
	s.mcu.Reload(config)
}

func (s *ProxyServer) setCommonHeaders(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nextcloud-spreed-signaling-proxy/"+s.version)
		w.Header().Set("X-Spreed-Signaling-Features", strings.Join(s.welcomeMsg.Features, ", "))
		f(w, r)
	}
}

func (s *ProxyServer) welcomeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, s.welcomeMessage) // nolint
}

func (s *ProxyServer) proxyHandler(w http.ResponseWriter, r *http.Request) {
	addr := signaling.GetRealUserIP(r, s.trustedProxies.Load())
	header := http.Header{}
	header.Set("Server", "nextcloud-spreed-signaling-proxy/"+s.version)
	header.Set("X-Spreed-Signaling-Features", strings.Join(s.welcomeMsg.Features, ", "))
	conn, err := s.upgrader.Upgrade(w, r, header)
	if err != nil {
		s.logger.Printf("Could not upgrade request from %s: %s", addr, err)
		return
	}

	ctx := signaling.NewLoggerContext(r.Context(), s.logger)
	if conn.Subprotocol() == signaling.JanusEventsSubprotocol {
		agent := r.Header.Get("User-Agent")
		signaling.RunJanusEventsHandler(ctx, s.mcu, conn, addr, agent)
		return
	}

	client, err := NewProxyClient(ctx, s, conn, addr)
	if err != nil {
		s.logger.Printf("Could not create client for %s: %s", addr, err)
		return
	}

	go client.WritePump()
	client.ReadPump()
}

func (s *ProxyServer) clientClosed(client *signaling.Client) {
	s.logger.Printf("Connection from %s closed", client.RemoteAddr())
}

func (s *ProxyServer) onMcuConnected() {
	s.logger.Printf("Connection to %s established", s.url)
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
	if s.stopped.Load() {
		// Shutting down, no need to notify.
		return
	}

	s.logger.Printf("Connection to %s lost", s.url)
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
	msg := s.newLoadEvent(s.load.Load(), s.currentIncoming.Load(), s.currentOutgoing.Load())
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
		s.logger.Printf("Message: %s", string(data))
	}
	var message signaling.ProxyClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		if session := client.GetSession(); session != nil {
			s.logger.Printf("Error decoding message from client %s: %v", session.PublicId(), err)
		} else {
			s.logger.Printf("Error decoding message from %s: %v", client.RemoteAddr(), err)
		}
		client.SendError(signaling.InvalidFormat)
		return
	}

	if err := message.CheckValid(); err != nil {
		if session := client.GetSession(); session != nil {
			s.logger.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
		} else {
			s.logger.Printf("Invalid message %+v from %s: %v", message, client.RemoteAddr(), err)
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
		if resumeId := signaling.PublicSessionId(message.Hello.ResumeId); resumeId != "" {
			if data, err := s.cookie.DecodePublic(resumeId); err == nil {
				session = s.GetSession(data.Sid)
			}

			if session == nil || resumeId != session.PublicId() {
				client.SendMessage(message.NewErrorServerMessage(signaling.NoSuchSession))
				return
			}

			s.logger.Printf("Resumed session %s", session.PublicId())
			session.MarkUsed()
			if s.shutdownScheduled.Load() {
				s.sendShutdownScheduled(session)
			} else {
				s.sendCurrentLoad(session)
			}
			statsSessionsResumedTotal.Inc()
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
				Version:   signaling.HelloVersionV1,
				SessionId: session.PublicId(),
				Server:    s.welcomeMsg,
			},
		}
		client.SendMessage(response)
		if s.shutdownScheduled.Load() {
			s.sendShutdownScheduled(session)
		} else {
			s.sendCurrentLoad(session)
		}
		return
	}

	ctx := context.WithValue(session.Context(), ContextKeySession, session)
	session.MarkUsed()

	switch message.Type {
	case "command":
		s.processCommand(ctx, client, session, &message)
	case "payload":
		s.processPayload(ctx, client, session, &message)
	case "bye":
		s.processBye(ctx, client, session, &message)
	default:
		session.sendMessage(message.NewErrorServerMessage(UnsupportedMessage))
	}
}

type emptyInitiator struct{}

func (i *emptyInitiator) Country() string {
	return ""
}

type proxyRemotePublisher struct {
	proxy     *ProxyServer
	remoteUrl string

	publisherId signaling.PublicSessionId
}

func (p *proxyRemotePublisher) PublisherId() signaling.PublicSessionId {
	return p.publisherId
}

func (p *proxyRemotePublisher) StartPublishing(ctx context.Context, publisher signaling.McuRemotePublisherProperties) error {
	conn, err := p.proxy.getRemoteConnection(p.remoteUrl)
	if err != nil {
		return err
	}

	if _, err := conn.RequestMessage(ctx, &signaling.ProxyClientMessage{
		Type: "command",
		Command: &signaling.CommandProxyClientMessage{
			Type:     "publish-remote",
			ClientId: string(p.publisherId),
			Hostname: p.proxy.remoteHostname,
			Port:     publisher.Port(),
			RtcpPort: publisher.RtcpPort(),
		},
	}); err != nil {
		return err
	}

	return nil
}

func (p *proxyRemotePublisher) StopPublishing(ctx context.Context, publisher signaling.McuRemotePublisherProperties) error {
	defer p.proxy.removeRemotePublisher(p)

	conn, err := p.proxy.getRemoteConnection(p.remoteUrl)
	if err != nil {
		return err
	}

	if _, err := conn.RequestMessage(ctx, &signaling.ProxyClientMessage{
		Type: "command",
		Command: &signaling.CommandProxyClientMessage{
			Type:     "unpublish-remote",
			ClientId: string(p.publisherId),
			Hostname: p.proxy.remoteHostname,
			Port:     publisher.Port(),
			RtcpPort: publisher.RtcpPort(),
		},
	}); err != nil {
		return err
	}

	return nil
}

func (p *proxyRemotePublisher) GetStreams(ctx context.Context) ([]signaling.PublisherStream, error) {
	conn, err := p.proxy.getRemoteConnection(p.remoteUrl)
	if err != nil {
		return nil, err
	}

	response, err := conn.RequestMessage(ctx, &signaling.ProxyClientMessage{
		Type: "command",
		Command: &signaling.CommandProxyClientMessage{
			Type:     "get-publisher-streams",
			ClientId: string(p.publisherId),
		},
	})
	if err != nil {
		return nil, err
	}

	return response.Command.Streams, nil
}

func (s *ProxyServer) addRemotePublisher(publisher *proxyRemotePublisher) {
	s.remoteConnectionsLock.Lock()
	defer s.remoteConnectionsLock.Unlock()

	publishers, found := s.remotePublishers[publisher.remoteUrl]
	if !found {
		publishers = make(map[*proxyRemotePublisher]bool)
		s.remotePublishers[publisher.remoteUrl] = publishers
	}

	publishers[publisher] = true
	s.logger.Printf("Add remote publisher to %s", publisher.remoteUrl)
}

func (s *ProxyServer) hasRemotePublishers() bool {
	s.remoteConnectionsLock.Lock()
	defer s.remoteConnectionsLock.Unlock()

	return len(s.remotePublishers) > 0
}

func (s *ProxyServer) removeRemotePublisher(publisher *proxyRemotePublisher) {
	s.remoteConnectionsLock.Lock()
	defer s.remoteConnectionsLock.Unlock()

	s.logger.Printf("Removing remote publisher to %s", publisher.remoteUrl)
	publishers, found := s.remotePublishers[publisher.remoteUrl]
	if !found {
		return
	}

	delete(publishers, publisher)
	if len(publishers) > 0 {
		return
	}

	delete(s.remotePublishers, publisher.remoteUrl)
	if conn, found := s.remoteConnections[publisher.remoteUrl]; found {
		delete(s.remoteConnections, publisher.remoteUrl)
		if err := conn.Close(); err != nil {
			s.logger.Printf("Error closing remote connection to %s: %s", publisher.remoteUrl, err)
		} else {
			s.logger.Printf("Remote connection to %s closed", publisher.remoteUrl)
		}
	}
}

func (s *ProxyServer) processCommand(ctx context.Context, client *ProxyClient, session *ProxySession, message *signaling.ProxyClientMessage) {
	cmd := message.Command

	statsCommandMessagesTotal.WithLabelValues(cmd.Type).Inc()

	switch cmd.Type {
	case "create-publisher":
		if s.shutdownScheduled.Load() {
			session.sendMessage(message.NewErrorServerMessage(ShutdownScheduled))
			return
		}

		ctx2, cancel := context.WithTimeout(ctx, s.mcuTimeout)
		defer cancel()

		id := uuid.New().String()
		settings := cmd.PublisherSettings
		if settings == nil {
			settings = &signaling.NewPublisherSettings{
				Bitrate:    cmd.Bitrate,    // nolint
				MediaTypes: cmd.MediaTypes, // nolint
			}
		}
		publisher, err := s.mcu.NewPublisher(ctx2, session, signaling.PublicSessionId(id), cmd.Sid, cmd.StreamType, *settings, &emptyInitiator{})
		if err == context.DeadlineExceeded {
			s.logger.Printf("Timeout while creating %s publisher %s for %s", cmd.StreamType, id, session.PublicId())
			session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingPublisher))
			return
		} else if err != nil {
			s.logger.Printf("Error while creating %s publisher %s for %s: %s", cmd.StreamType, id, session.PublicId(), err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		s.logger.Printf("Created %s publisher %s as %s for %s", cmd.StreamType, publisher.Id(), id, session.PublicId())
		session.StorePublisher(ctx, id, publisher)
		s.StoreClient(id, publisher)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id:      id,
				Bitrate: publisher.MaxBitrate(),
			},
		}
		session.sendMessage(response)
		statsPublishersCurrent.WithLabelValues(string(cmd.StreamType)).Inc()
		statsPublishersTotal.WithLabelValues(string(cmd.StreamType)).Inc()
	case "create-subscriber":
		id := uuid.New().String()
		publisherId := cmd.PublisherId
		var subscriber signaling.McuSubscriber
		var err error

		handleCreateError := func(err error) {
			if err == context.DeadlineExceeded {
				s.logger.Printf("Timeout while creating %s subscriber on %s for %s", cmd.StreamType, publisherId, session.PublicId())
				session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingSubscriber))
				return
			} else if errors.Is(err, signaling.ErrRemoteStreamsNotSupported) {
				session.sendMessage(message.NewErrorServerMessage(RemoteSubscribersNotSupported))
				return
			}

			s.logger.Printf("Error while creating %s subscriber on %s for %s: %s", cmd.StreamType, publisherId, session.PublicId(), err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
		}

		if cmd.RemoteUrl != "" {
			if s.tokenId == "" || s.tokenKey == nil || s.remoteHostname == "" {
				session.sendMessage(message.NewErrorServerMessage(RemoteSubscribersNotSupported))
				return
			}

			remoteMcu, ok := s.mcu.(signaling.RemoteMcu)
			if !ok {
				session.sendMessage(message.NewErrorServerMessage(RemoteSubscribersNotSupported))
				return
			}

			claims, _, err := s.parseToken(cmd.RemoteToken)
			if err != nil {
				if e, ok := err.(*signaling.Error); ok {
					client.SendMessage(message.NewErrorServerMessage(e))
				} else {
					client.SendMessage(message.NewWrappedErrorServerMessage(err))
				}
				return
			}

			if claims.Subject != string(publisherId) {
				session.sendMessage(message.NewErrorServerMessage(TokenAuthFailed))
				return
			}

			subCtx, cancel := context.WithTimeout(ctx, remotePublisherTimeout)
			defer cancel()

			s.logger.Printf("Creating remote subscriber for %s on %s", publisherId, cmd.RemoteUrl)

			controller := &proxyRemotePublisher{
				proxy:       s,
				remoteUrl:   cmd.RemoteUrl,
				publisherId: publisherId,
			}

			var publisher signaling.McuRemotePublisher
			publisher, err = remoteMcu.NewRemotePublisher(subCtx, session, controller, cmd.StreamType)
			if err != nil {
				handleCreateError(err)
				return
			}

			defer func() {
				go publisher.Close(context.Background())
			}()

			s.addRemotePublisher(controller)

			subscriber, err = remoteMcu.NewRemoteSubscriber(subCtx, session, publisher)
			if err != nil {
				handleCreateError(err)
				return
			}

			s.logger.Printf("Created remote %s subscriber %s as %s for %s on %s", cmd.StreamType, subscriber.Id(), id, session.PublicId(), cmd.RemoteUrl)
		} else {
			ctx2, cancel := context.WithTimeout(ctx, s.mcuTimeout)
			defer cancel()

			subscriber, err = s.mcu.NewSubscriber(ctx2, session, publisherId, cmd.StreamType, &emptyInitiator{})
			if err != nil {
				handleCreateError(err)
				return
			}

			s.logger.Printf("Created %s subscriber %s as %s for %s", cmd.StreamType, subscriber.Id(), id, session.PublicId())
		}

		session.StoreSubscriber(ctx, id, subscriber)
		s.StoreClient(id, subscriber)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id:  id,
				Sid: subscriber.Sid(),
			},
		}
		session.sendMessage(response)
		statsSubscribersCurrent.WithLabelValues(string(cmd.StreamType)).Inc()
		statsSubscribersTotal.WithLabelValues(string(cmd.StreamType)).Inc()
	case "delete-publisher":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		publisher, ok := client.(signaling.McuPublisher)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		if session.DeletePublisher(publisher) == "" {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		if s.DeleteClient(cmd.ClientId, client) {
			statsPublishersCurrent.WithLabelValues(string(client.StreamType())).Dec()
		}

		go func() {
			s.logger.Printf("Closing %s publisher %s as %s", client.StreamType(), client.Id(), cmd.ClientId)
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

		if s.DeleteClient(cmd.ClientId, client) {
			statsSubscribersCurrent.WithLabelValues(string(client.StreamType())).Dec()
		}

		go func() {
			s.logger.Printf("Closing %s subscriber %s as %s", client.StreamType(), client.Id(), cmd.ClientId)
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
	case "publish-remote":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		publisher, ok := client.(signaling.McuRemoteAwarePublisher)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		ctx2, cancel := context.WithTimeout(ctx, s.mcuTimeout)
		defer cancel()

		if err := publisher.PublishRemote(ctx2, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
			var je *janus.ErrorMsg
			if !errors.As(err, &je) || je.Err.Code != signaling.JANUS_VIDEOROOM_ERROR_ID_EXISTS {
				s.logger.Printf("Error publishing %s %s to remote %s (port=%d, rtcpPort=%d): %s", publisher.StreamType(), cmd.ClientId, cmd.Hostname, cmd.Port, cmd.RtcpPort, err)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}

			ctx2, cancel = context.WithTimeout(ctx, s.mcuTimeout)
			defer cancel()

			if err := publisher.UnpublishRemote(ctx2, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
				s.logger.Printf("Error unpublishing old %s %s to remote %s (port=%d, rtcpPort=%d): %s", publisher.StreamType(), cmd.ClientId, cmd.Hostname, cmd.Port, cmd.RtcpPort, err)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}

			ctx2, cancel = context.WithTimeout(ctx, s.mcuTimeout)
			defer cancel()

			if err := publisher.PublishRemote(ctx2, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
				s.logger.Printf("Error publishing %s %s to remote %s (port=%d, rtcpPort=%d): %s", publisher.StreamType(), cmd.ClientId, cmd.Hostname, cmd.Port, cmd.RtcpPort, err)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}
		}

		session.AddRemotePublisher(publisher, cmd.Hostname, cmd.Port, cmd.RtcpPort)
		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: cmd.ClientId,
			},
		}
		session.sendMessage(response)
	case "unpublish-remote":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		publisher, ok := client.(signaling.McuRemoteAwarePublisher)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		ctx2, cancel := context.WithTimeout(ctx, s.mcuTimeout)
		defer cancel()

		if err := publisher.UnpublishRemote(ctx2, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
			s.logger.Printf("Error unpublishing %s %s from remote %s: %s", publisher.StreamType(), cmd.ClientId, cmd.Hostname, err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		session.RemoveRemotePublisher(publisher, cmd.Hostname, cmd.Port, cmd.RtcpPort)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id: cmd.ClientId,
			},
		}
		session.sendMessage(response)
	case "get-publisher-streams":
		client := s.GetClient(cmd.ClientId)
		if client == nil {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		publisher, ok := client.(signaling.McuPublisherWithStreams)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		streams, err := publisher.GetStreams(ctx)
		if err != nil {
			s.logger.Printf("Could not get streams of publisher %s: %s", publisher.Id(), err)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id:      cmd.ClientId,
				Streams: streams,
			},
		}
		session.sendMessage(response)
	default:
		s.logger.Printf("Unsupported command %+v", message.Command)
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

	statsPayloadMessagesTotal.WithLabelValues(payload.Type).Inc()

	var mcuData *signaling.MessageClientMessageData
	switch payload.Type {
	case "offer":
		fallthrough
	case "answer":
		fallthrough
	case "selectStream":
		fallthrough
	case "candidate":
		mcuData = &signaling.MessageClientMessageData{
			RoomType: string(mcuClient.StreamType()),
			Type:     payload.Type,
			Sid:      payload.Sid,
			Payload:  payload.Payload,
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
			RoomType: string(mcuClient.StreamType()),
			Type:     payload.Type,
			Sid:      payload.Sid,
		}
	default:
		session.sendMessage(message.NewErrorServerMessage(UnsupportedPayload))
		return
	}

	if err := mcuData.CheckValid(); err != nil {
		s.logger.Printf("Received invalid payload %+v for %s client %s: %s", mcuData, mcuClient.StreamType(), payload.ClientId, err)
		session.sendMessage(message.NewErrorServerMessage(UnsupportedPayload))
		return
	}

	ctx2, cancel := context.WithTimeout(ctx, s.mcuTimeout)
	defer cancel()

	mcuClient.SendMessage(ctx2, nil, mcuData, func(err error, response api.StringMap) {
		var responseMsg *signaling.ProxyServerMessage
		if errors.Is(err, signaling.ErrCandidateFiltered) {
			// Silently ignore filtered candidates.
			err = nil
		}

		if err != nil {
			s.logger.Printf("Error sending %+v to %s client %s: %s", mcuData, mcuClient.StreamType(), payload.ClientId, err)
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

func (s *ProxyServer) processBye(ctx context.Context, client *ProxyClient, session *ProxySession, message *signaling.ProxyClientMessage) {
	s.logger.Printf("Closing session %s", session.PublicId())
	s.DeleteSession(session.Sid())
}

func (s *ProxyServer) parseToken(tokenValue string) (*signaling.TokenClaims, string, error) {
	reason := "auth-failed"
	token, err := jwt.ParseWithClaims(tokenValue, &signaling.TokenClaims{}, func(token *jwt.Token) (any, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			s.logger.Printf("Unexpected signing method: %v", token.Header["alg"])
			reason = "unsupported-signing-method"
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		claims, ok := token.Claims.(*signaling.TokenClaims)
		if !ok {
			s.logger.Printf("Unsupported claims type: %+v", token.Claims)
			reason = "unsupported-claims"
			return nil, fmt.Errorf("unsupported claims type")
		}

		tokenKey, err := s.tokens.Get(claims.Issuer)
		if err != nil {
			s.logger.Printf("Could not get token for %s: %s", claims.Issuer, err)
			reason = "missing-issuer"
			return nil, err
		}

		if tokenKey == nil || tokenKey.key == nil {
			s.logger.Printf("Issuer %s is not supported", claims.Issuer)
			reason = "unsupported-issuer"
			return nil, fmt.Errorf("no key found for issuer")
		}

		return tokenKey.key, nil
	}, jwt.WithValidMethods([]string{
		jwt.SigningMethodRS256.Alg(),
		jwt.SigningMethodRS384.Alg(),
		jwt.SigningMethodRS512.Alg(),
	}), jwt.WithIssuedAt(), jwt.WithLeeway(tokenLeeway))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenNotValidYet) || errors.Is(err, jwt.ErrTokenUsedBeforeIssued) {
			return nil, "not-valid-yet", TokenNotValidYet
		} else if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, "expired", TokenExpired
		}

		return nil, reason, TokenAuthFailed
	}

	claims, ok := token.Claims.(*signaling.TokenClaims)
	if !ok || !token.Valid {
		return nil, "auth-failed", TokenAuthFailed
	}

	now := time.Now()
	minIssuedAt := now.Add(-(maxTokenAge + tokenLeeway))
	if issuedAt := claims.IssuedAt; issuedAt == nil || issuedAt.Before(minIssuedAt) {
		return nil, "expired", TokenExpired
	}

	return claims, "", nil
}

func (s *ProxyServer) NewSession(hello *signaling.HelloProxyClientMessage) (*ProxySession, error) {
	if proxyDebugMessages {
		s.logger.Printf("Hello: %+v", hello)
	}

	claims, reason, err := s.parseToken(hello.Token)
	if err != nil {
		statsTokenErrorsTotal.WithLabelValues(reason).Inc()
		return nil, err
	}

	sid := s.sid.Add(1)
	for sid == 0 {
		sid = s.sid.Add(1)
	}

	sessionIdData := &signaling.SessionIdData{
		Sid:     sid,
		Created: time.Now().UnixMicro(),
	}

	encoded, err := s.cookie.EncodePublic(sessionIdData)
	if err != nil {
		return nil, err
	}

	s.logger.Printf("Created session %s for %+v", encoded, claims)
	session := NewProxySession(s, sid, encoded)
	s.StoreSession(sid, session)
	statsSessionsCurrent.Inc()
	statsSessionsTotal.Inc()
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

// +checklocks:s.sessionsLock
func (s *ProxyServer) deleteSessionLocked(id uint64) {
	if session, found := s.sessions[id]; found {
		delete(s.sessions, id)
		s.sessionsLock.Unlock()
		defer s.sessionsLock.Lock()
		session.Close()
		statsSessionsCurrent.Dec()
	}
}

func (s *ProxyServer) StoreClient(id string, client signaling.McuClient) {
	s.clientsLock.Lock()
	defer s.clientsLock.Unlock()
	s.clients[id] = client
	s.clientIds[client.Id()] = id
}

func (s *ProxyServer) DeleteClient(id string, client signaling.McuClient) bool {
	s.clientsLock.Lock()
	defer s.clientsLock.Unlock()
	if _, found := s.clients[id]; !found {
		return false
	}

	delete(s.clients, id)
	delete(s.clientIds, client.Id())

	if len(s.clients) == 0 && s.shutdownScheduled.Load() {
		go close(s.shutdownChannel)
	}
	return true
}

func (s *ProxyServer) HasClients() bool {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return len(s.clients) > 0
}

func (s *ProxyServer) GetClientsLoad() (load uint64, incoming api.Bandwidth, outgoing api.Bandwidth) {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()

	for _, c := range s.clients {
		// Use "current" bandwidth usage if supported.
		if bw, ok := c.(signaling.McuClientWithBandwidth); ok {
			if bandwidth := bw.Bandwidth(); bandwidth != nil {
				incoming += bandwidth.Received
				outgoing += bandwidth.Sent
				continue
			}
		}

		bitrate := c.MaxBitrate()
		if _, ok := c.(signaling.McuPublisher); ok {
			incoming += bitrate
		} else if _, ok := c.(signaling.McuSubscriber); ok {
			outgoing += bitrate
		}
	}
	load = incoming.Bits() + outgoing.Bits()
	load = min(uint64(len(s.clients)), load/1024)
	return
}

func (s *ProxyServer) GetClient(id string) signaling.McuClient {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return s.clients[id]
}

func (s *ProxyServer) GetPublisher(publisherId string) signaling.McuPublisher {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	for _, c := range s.clients {
		pub, ok := c.(signaling.McuPublisher)
		if !ok {
			continue
		}

		if pub.Id() == publisherId {
			return pub
		}
	}
	return nil
}

func (s *ProxyServer) GetClientId(client signaling.McuClient) string {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()
	return s.clientIds[client.Id()]
}

func (s *ProxyServer) getStats() api.StringMap {
	result := api.StringMap{
		"sessions": s.GetSessionsCount(),
		"load":     s.load.Load(),
		"mcu":      s.mcu.GetStats(),
	}
	return result
}

func (s *ProxyServer) allowStatsAccess(r *http.Request) bool {
	addr := signaling.GetRealUserIP(r, s.trustedProxies.Load())
	ip := net.ParseIP(addr)
	if len(ip) == 0 {
		return false
	}

	allowed := s.statsAllowedIps.Load()
	return allowed != nil && allowed.Allowed(ip)
}

func (s *ProxyServer) validateStatsRequest(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowStatsAccess(r) {
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
		s.logger.Printf("Could not serialize stats %+v: %s", stats, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(statsData) // nolint
}

func (s *ProxyServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	// Expose prometheus metrics at "/metrics".
	promhttp.Handler().ServeHTTP(w, r)
}

func (s *ProxyServer) getRemoteConnection(url string) (*RemoteConnection, error) {
	s.remoteConnectionsLock.Lock()
	defer s.remoteConnectionsLock.Unlock()

	conn, found := s.remoteConnections[url]
	if found {
		return conn, nil
	}

	conn, err := NewRemoteConnection(s, url, s.tokenId, s.tokenKey, s.remoteTlsConfig)
	if err != nil {
		return nil, err
	}

	s.remoteConnections[url] = conn
	return conn, nil
}

func (s *ProxyServer) PublisherDeleted(publisher signaling.McuPublisher) {
	s.sessionsLock.RLock()
	defer s.sessionsLock.RUnlock()

	for _, session := range s.sessions {
		session.OnPublisherDeleted(publisher)
	}
}

func (s *ProxyServer) RemotePublisherDeleted(publisherId signaling.PublicSessionId) {
	s.sessionsLock.RLock()
	defer s.sessionsLock.RUnlock()

	for _, session := range s.sessions {
		session.OnRemotePublisherDeleted(publisherId)
	}
}
