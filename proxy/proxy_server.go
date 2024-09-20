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
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/websocket"
	"github.com/notedit/janus-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
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

	remotePublisherTimeout = 5 * time.Second

	ProxyFeatureRemoteStreams = "remote-streams"
)

var (
	defaultProxyFeatures = []string{
		ProxyFeatureRemoteStreams,
	}

	ErrCancelled = errors.New("cancelled")
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
	log            *zap.Logger
	version        string
	country        string
	welcomeMessage string
	welcomeMsg     *signaling.WelcomeServerMessage
	config         *goconf.ConfigFile

	url     string
	mcu     signaling.Mcu
	stopped atomic.Bool
	load    atomic.Int64

	maxIncoming     atomic.Int64
	currentIncoming atomic.Int64
	maxOutgoing     atomic.Int64
	currentOutgoing atomic.Int64

	shutdownChannel   chan struct{}
	shutdownScheduled atomic.Bool

	upgrader websocket.Upgrader

	tokens          ProxyTokens
	statsAllowedIps atomic.Pointer[signaling.AllowedIps]
	trustedProxies  atomic.Pointer[signaling.AllowedIps]

	sid          atomic.Uint64
	cookie       *securecookie.SecureCookie
	sessions     map[uint64]*ProxySession
	sessionsLock sync.RWMutex

	clients     map[string]signaling.McuClient
	clientIds   map[string]string
	clientsLock sync.RWMutex

	tokenId               string
	tokenKey              *rsa.PrivateKey
	remoteTlsConfig       *tls.Config
	remoteHostname        string
	remoteConnections     map[string]*RemoteConnection
	remoteConnectionsLock sync.Mutex
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

func getTargetBandwidths(log *zap.Logger, config *goconf.ConfigFile) (int, int) {
	maxIncoming, _ := config.GetInt("bandwidth", "incoming")
	if maxIncoming < 0 {
		maxIncoming = 0
	}
	if maxIncoming > 0 {
		log.Info("Target bandwidth for incoming streams",
			zap.Int("mbits", maxIncoming),
		)
	} else {
		log.Info("Target bandwidth for incoming streams",
			zap.String("mbits", "unlimited"),
		)
	}
	maxOutgoing, _ := config.GetInt("bandwidth", "outgoing")
	if maxOutgoing < 0 {
		maxOutgoing = 0
	}
	if maxIncoming > 0 {
		log.Info("Target bandwidth for outgoing streams",
			zap.Int("mbits", maxOutgoing),
		)
	} else {
		log.Info("Target bandwidth for outgoing streams",
			zap.String("mbits", "unlimited"),
		)
	}

	return maxIncoming, maxOutgoing
}

func NewProxyServer(log *zap.Logger, r *mux.Router, version string, config *goconf.ConfigFile) (*ProxyServer, error) {
	hashKey := make([]byte, 64)
	if _, err := rand.Read(hashKey); err != nil {
		return nil, fmt.Errorf("Could not generate random hash key: %s", err)
	}

	blockKey := make([]byte, 32)
	if _, err := rand.Read(blockKey); err != nil {
		return nil, fmt.Errorf("Could not generate random block key: %s", err)
	}

	var tokens ProxyTokens
	var err error
	tokenType, _ := config.GetString("app", "tokentype")
	if tokenType == "" {
		tokenType = TokenTypeDefault
	}

	switch tokenType {
	case TokenTypeEtcd:
		tokens, err = NewProxyTokensEtcd(log, config)
	case TokenTypeStatic:
		tokens, err = NewProxyTokensStatic(log, config)
	default:
		return nil, fmt.Errorf("Unsupported token type configured: %s", tokenType)
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
		log.Info("Access to the stats endpoint only allowed from ips",
			zap.Stringer("ips", statsAllowedIps),
		)
	} else {
		statsAllowedIps = signaling.DefaultAllowedIps()
		log.Info("No IPs configured for the stats endpoint, only allowing access from default ips",
			zap.Stringer("ips", statsAllowedIps),
		)
	}

	trustedProxies, _ := config.GetString("app", "trustedproxies")
	trustedProxiesIps, err := signaling.ParseAllowedIps(trustedProxies)
	if err != nil {
		return nil, err
	}

	if !trustedProxiesIps.Empty() {
		log.Info("Trusted proxies",
			zap.Any("proxies", trustedProxiesIps),
		)
	} else {
		trustedProxiesIps = signaling.DefaultTrustedProxies
		log.Info("No trusted proxies configured, allowing for default list",
			zap.Any("proxies", trustedProxiesIps),
		)
	}

	country, _ := config.GetString("app", "country")
	country = strings.ToUpper(country)
	if signaling.IsValidCountry(country) {
		log.Info("Sending country information",
			zap.String("country", country),
		)
	} else if country != "" {
		return nil, fmt.Errorf("Invalid country: %s", country)
	} else {
		log.Info("Not sending country information")
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
			return nil, fmt.Errorf("No token key configured")
		}
		tokenKeyData, err := os.ReadFile(tokenKeyFilename)
		if err != nil {
			return nil, fmt.Errorf("Could not read private key from %s: %s", tokenKeyFilename, err)
		}
		tokenKey, err = jwt.ParseRSAPrivateKeyFromPEM(tokenKeyData)
		if err != nil {
			return nil, fmt.Errorf("Could not parse private key from %s: %s", tokenKeyFilename, err)
		}
		log.Info("Using token id for remote streams",
			zap.String("tokenid", tokenId),
		)

		remoteHostname, _ = config.GetString("app", "hostname")
		if remoteHostname == "" {
			remoteHostname, err = GetLocalIP()
			if err != nil {
				return nil, fmt.Errorf("could not get local ip: %w", err)
			}
		}
		if remoteHostname == "" {
			log.Warn("Could not determine hostname for remote streams, will be disabled. Please configure manually.")
		} else {
			log.Info("Using hostname for remote streams",
				zap.String("hostname", remoteHostname),
			)
		}

		skipverify, _ := config.GetBool("backend", "skipverify")
		if skipverify {
			log.Warn("Remote stream requests verification is disabled!")
			remoteTlsConfig = &tls.Config{
				InsecureSkipVerify: skipverify,
			}
		}
	} else {
		log.Warn("No token id configured, remote streams will be disabled")
	}

	maxIncoming, maxOutgoing := getTargetBandwidths(log, config)

	result := &ProxyServer{
		log:            log,
		version:        version,
		country:        country,
		welcomeMessage: string(welcomeMessage) + "\n",
		welcomeMsg: &signaling.WelcomeServerMessage{
			Version:  version,
			Country:  country,
			Features: defaultProxyFeatures,
		},
		config: config,

		shutdownChannel: make(chan struct{}),

		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
		},

		tokens: tokens,

		cookie:   securecookie.New(hashKey, blockKey).MaxAge(0),
		sessions: make(map[uint64]*ProxySession),

		clients:   make(map[string]signaling.McuClient),
		clientIds: make(map[string]string),

		tokenId:           tokenId,
		tokenKey:          tokenKey,
		remoteTlsConfig:   remoteTlsConfig,
		remoteHostname:    remoteHostname,
		remoteConnections: make(map[string]*RemoteConnection),
	}

	result.maxIncoming.Store(int64(maxIncoming) * 1024 * 1024)
	result.maxOutgoing.Store(int64(maxOutgoing) * 1024 * 1024)
	result.statsAllowedIps.Store(statsAllowedIps)
	result.trustedProxies.Store(trustedProxiesIps)
	result.upgrader.CheckOrigin = result.checkOrigin

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
		return fmt.Errorf("No MCU server url configured")
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
			mcu, err = signaling.NewMcuJanus(ctx, s.log, s.url, config)
			if err == nil {
				signaling.RegisterJanusMcuStats()
			}
		default:
			return fmt.Errorf("Unsupported MCU type: %s", mcuType)
		}
		if err == nil {
			mcu.SetOnConnected(s.onMcuConnected)
			mcu.SetOnDisconnected(s.onMcuDisconnected)
			err = mcu.Start(ctx)
			if err != nil {
				s.log.Error("Could not create MCU",
					zap.String("type", mcuType),
					zap.String("url", s.url),
					zap.Error(err),
				)
			}
		}
		if err == nil {
			break
		}

		s.log.Error("Could not initialize MCU, will retry",
			zap.String("type", mcuType),
			zap.String("url", s.url),
			zap.Duration("wait", backoff.NextWait()),
			zap.Error(err),
		)
		backoff.Wait(ctx)
		if ctx.Err() != nil {
			return ErrCancelled
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

func (s *ProxyServer) newLoadEvent(load int64, incoming int64, outgoing int64) *signaling.ProxyServerMessage {
	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type: "update-load",
			Load: load,
		},
	}
	maxIncoming := s.maxIncoming.Load()
	maxOutgoing := s.maxOutgoing.Load()
	if maxIncoming > 0 || maxOutgoing > 0 {
		msg.Event.Bandwidth = &signaling.EventProxyServerBandwidth{}
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

	s.sendLoadToAll(load, incoming, outgoing)
}

func (s *ProxyServer) sendLoadToAll(load int64, incoming int64, outgoing int64) {
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

		s.log.Info("Delete expired session",
			zap.String("sessionid", session.PublicId()),
		)
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
			s.log.Info("Access to the stats endpoint only allowed from ips",
				zap.Stringer("ips", statsAllowedIps),
			)
		} else {
			statsAllowedIps = signaling.DefaultAllowedIps()
			s.log.Info("No IPs configured for the stats endpoint, only allowing access from default ips",
				zap.Stringer("ips", statsAllowedIps),
			)
		}
		s.statsAllowedIps.Store(statsAllowedIps)
	} else {
		s.log.Error("Error parsing allowed stats ips",
			zap.String("allowed", statsAllowed),
			zap.Error(err),
		)
	}

	trustedProxies, _ := config.GetString("app", "trustedproxies")
	if trustedProxiesIps, err := signaling.ParseAllowedIps(trustedProxies); err == nil {
		if !trustedProxiesIps.Empty() {
			s.log.Info("Trusted proxies",
				zap.Any("proxies", trustedProxiesIps),
			)
		} else {
			trustedProxiesIps = signaling.DefaultTrustedProxies
			s.log.Info("No trusted proxies configured, allowing for default list",
				zap.Any("proxies", trustedProxiesIps),
			)
		}
		s.trustedProxies.Store(trustedProxiesIps)
	} else {
		s.log.Error("Error parsing trusted proxies",
			zap.String("trusted", trustedProxies),
			zap.Error(err),
		)
	}

	maxIncoming, maxOutgoing := getTargetBandwidths(s.log, config)
	oldIncoming := s.maxIncoming.Swap(int64(maxIncoming))
	oldOutgoing := s.maxOutgoing.Swap(int64(maxOutgoing))
	if oldIncoming != int64(maxIncoming) || oldOutgoing != int64(maxOutgoing) {
		// Notify sessions about updated load / bandwidth usage.
		go s.sendLoadToAll(s.load.Load(), s.currentIncoming.Load(), s.currentOutgoing.Load())
	}

	s.tokens.Reload(config)
	s.mcu.Reload(config)
}

func (s *ProxyServer) setCommonHeaders(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nextcloud-spreed-signaling-proxy/"+s.version)
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
	log := s.log.With(
		zap.String("addr", addr),
	)
	header := http.Header{}
	header.Set("Server", "nextcloud-spreed-signaling-proxy/"+s.version)
	header.Set("X-Spreed-Signaling-Features", strings.Join(s.welcomeMsg.Features, ", "))
	conn, err := s.upgrader.Upgrade(w, r, header)
	if err != nil {
		log.Error("Could not upgrade request",
			zap.String("addr", addr),
			zap.Error(err),
		)
		return
	}

	client, err := NewProxyClient(s.log, s, conn, addr)
	if err != nil {
		log.Error("Could not create client",
			zap.String("addr", addr),
			zap.Error(err),
		)
		return
	}

	go client.WritePump()
	go client.ReadPump()
}

func (s *ProxyServer) clientClosed(client *signaling.Client) {
	s.log.Info("Connection closed",
		zap.String("addr", client.RemoteAddr()),
	)
}

func (s *ProxyServer) onMcuConnected() {
	s.log.Info("Connection established",
		zap.String("url", s.url),
	)
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

	s.log.Info("Connection lost",
		zap.String("url", s.url),
	)
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
	log := s.log.With(
		zap.String("addr", client.RemoteAddr()),
	)
	session := client.GetSession()
	if session != nil {
		log = log.With(
			zap.String("sessionid", session.PublicId()),
		)
	}
	if proxyDebugMessages {
		log.Debug("Message received",
			zap.ByteString("message", data),
		)
	}
	var message signaling.ProxyClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		log.Error("Error decoding message",
			zap.Error(err),
		)
		client.SendError(signaling.InvalidFormat)
		return
	}

	if err := message.CheckValid(); err != nil {
		log.Error("Invalid message",
			zap.Any("message", message),
			zap.Error(err),
		)
		client.SendMessage(message.NewErrorServerMessage(signaling.InvalidFormat))
		return
	}

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

			if session == nil || resumeId != session.PublicId() {
				client.SendMessage(message.NewErrorServerMessage(signaling.NoSuchSession))
				return
			}

			log = log.With(
				zap.String("sessionid", session.PublicId()),
			)
			log.Debug("Resumed session")
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

type proxyRemotePublisher struct {
	proxy     *ProxyServer
	remoteUrl string

	publisherId string
}

func (p *proxyRemotePublisher) PublisherId() string {
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
			ClientId: p.publisherId,
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
			ClientId: p.publisherId,
		},
	})
	if err != nil {
		return nil, err
	}

	return response.Command.Streams, nil
}

func (s *ProxyServer) processCommand(ctx context.Context, client *ProxyClient, session *ProxySession, message *signaling.ProxyClientMessage) {
	cmd := message.Command

	statsCommandMessagesTotal.WithLabelValues(cmd.Type).Inc()

	log := s.log.With(
		zap.String("sessionid", session.PublicId()),
	)

	switch cmd.Type {
	case "create-publisher":
		if s.shutdownScheduled.Load() {
			session.sendMessage(message.NewErrorServerMessage(ShutdownScheduled))
			return
		}

		id := uuid.New().String()
		log := log.With(
			zap.Any("streamtype", cmd.StreamType),
			zap.String("id", id),
		)
		publisher, err := s.mcu.NewPublisher(ctx, session, id, cmd.Sid, cmd.StreamType, cmd.Bitrate, cmd.MediaTypes, &emptyInitiator{})
		if err == context.DeadlineExceeded {
			log.Warn("Timeout creating publisher")
			session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingPublisher))
			return
		} else if err != nil {
			log.Error("Error creating publisher",
				zap.Error(err),
			)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		log.Info("Created publisher",
			zap.String("publisherid", publisher.Id()),
		)
		session.StorePublisher(ctx, id, publisher)
		s.StoreClient(id, publisher)

		response := &signaling.ProxyServerMessage{
			Id:   message.Id,
			Type: "command",
			Command: &signaling.CommandProxyServerMessage{
				Id:      id,
				Bitrate: int(publisher.MaxBitrate()),
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

		log := log.With(
			zap.Any("streamtype", cmd.StreamType),
			zap.String("id", id),
			zap.String("publisherid", publisherId),
		)

		handleCreateError := func(log *zap.Logger, err error) {
			if err == context.DeadlineExceeded {
				log.Warn("Timeout creating subscriber")
				session.sendMessage(message.NewErrorServerMessage(TimeoutCreatingSubscriber))
				return
			} else if errors.Is(err, signaling.ErrRemoteStreamsNotSupported) {
				session.sendMessage(message.NewErrorServerMessage(RemoteSubscribersNotSupported))
				return
			}

			log.Error("Error creating subscriber",
				zap.Error(err),
			)
			session.sendMessage(message.NewWrappedErrorServerMessage(err))
		}

		if cmd.RemoteUrl != "" {
			log = log.With(
				zap.String("remoteurl", cmd.RemoteUrl),
			)
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

			if claims.Subject != publisherId {
				session.sendMessage(message.NewErrorServerMessage(TokenAuthFailed))
				return
			}

			subCtx, cancel := context.WithTimeout(ctx, remotePublisherTimeout)
			defer cancel()

			log.Info("Creating remote subscriber")

			controller := &proxyRemotePublisher{
				proxy:       s,
				remoteUrl:   cmd.RemoteUrl,
				publisherId: publisherId,
			}

			var publisher signaling.McuRemotePublisher
			publisher, err = remoteMcu.NewRemotePublisher(subCtx, session, controller, cmd.StreamType)
			if err != nil {
				handleCreateError(log, err)
				return
			}

			defer func() {
				go publisher.Close(context.Background())
			}()

			subscriber, err = remoteMcu.NewRemoteSubscriber(subCtx, session, publisher)
			if err != nil {
				handleCreateError(log, err)
				return
			}

			log.Info("Created remote subscriber",
				zap.String("subscriberid", subscriber.Id()),
			)
		} else {
			subscriber, err = s.mcu.NewSubscriber(ctx, session, publisherId, cmd.StreamType, &emptyInitiator{})
			if err != nil {
				handleCreateError(log, err)
				return
			}

			log.Info("Created subscriber",
				zap.String("subscriberid", subscriber.Id()),
			)
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
			s.log.Info("Closing publisher",
				zap.Any("streamtype", cmd.StreamType),
				zap.String("id", client.Id()),
				zap.String("clientid", cmd.ClientId),
			)
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
			s.log.Info("Closing subscriber",
				zap.Any("streamtype", cmd.StreamType),
				zap.String("id", client.Id()),
				zap.String("clientid", cmd.ClientId),
			)
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

		publisher, ok := client.(signaling.McuPublisher)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		log := s.log.With(
			zap.Any("streamtype", publisher.StreamType()),
			zap.String("clientid", cmd.ClientId),
			zap.String("hostname", cmd.Hostname),
			zap.Int("port", cmd.Port),
			zap.Int("rtcpport", cmd.RtcpPort),
		)

		if err := publisher.PublishRemote(ctx, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
			var je *janus.ErrorMsg
			if !errors.As(err, &je) || je.Err.Code != signaling.JANUS_VIDEOROOM_ERROR_ID_EXISTS {
				log.Error("Error publishing to remote",
					zap.Error(err),
				)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}

			if err := publisher.UnpublishRemote(ctx, session.PublicId()); err != nil {
				log.Error("Error unpublishing old to remote",
					zap.Error(err),
				)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}

			if err := publisher.PublishRemote(ctx, session.PublicId(), cmd.Hostname, cmd.Port, cmd.RtcpPort); err != nil {
				log.Error("Error publishing to remote",
					zap.Error(err),
				)
				session.sendMessage(message.NewWrappedErrorServerMessage(err))
				return
			}
		}

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

		publisher, ok := client.(signaling.McuPublisher)
		if !ok {
			session.sendMessage(message.NewErrorServerMessage(UnknownClient))
			return
		}

		streams, err := publisher.GetStreams(ctx)
		if err != nil {
			s.log.Error("Could not get streams of publisher",
				zap.String("publisherid", publisher.Id()),
				zap.Error(err),
			)
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
		s.log.Warn("Unsupported command",
			zap.Any("command", message.Command),
		)
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

	log := s.log.With(
		zap.Any("streamtype", mcuClient.StreamType()),
		zap.String("clientid", payload.ClientId),
	)

	if err := mcuData.CheckValid(); err != nil {
		log.Error("Received invalid payload",
			zap.Any("payload", mcuData),
			zap.Error(err),
		)
		session.sendMessage(message.NewErrorServerMessage(UnsupportedPayload))
		return
	}

	mcuClient.SendMessage(ctx, nil, mcuData, func(err error, response map[string]interface{}) {
		var responseMsg *signaling.ProxyServerMessage
		if err != nil {
			log.Error("Error processing request for client",
				zap.Any("request", mcuData),
				zap.Error(err),
			)
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

func (s *ProxyServer) parseToken(tokenValue string) (*signaling.TokenClaims, string, error) {
	reason := "auth-failed"
	token, err := jwt.ParseWithClaims(tokenValue, &signaling.TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			s.log.Warn("Unexpected signing method",
				zap.Any("method", token.Header["alg"]),
			)
			reason = "unsupported-signing-method"
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}

		claims, ok := token.Claims.(*signaling.TokenClaims)
		if !ok {
			s.log.Warn("Unsupported claims type",
				zap.Any("claims", token.Claims),
			)
			reason = "unsupported-claims"
			return nil, fmt.Errorf("Unsupported claims type")
		}

		log := s.log.With(
			zap.String("issuer", claims.Issuer),
		)
		tokenKey, err := s.tokens.Get(claims.Issuer)
		if err != nil {
			s.log.Error("Could not get token",
				zap.Error(err),
			)
			reason = "missing-issuer"
			return nil, err
		}

		if tokenKey == nil || tokenKey.key == nil {
			log.Warn("Issuer is not supported")
			reason = "unsupported-issuer"
			return nil, fmt.Errorf("No key found for issuer")
		}

		return tokenKey.key, nil
	})
	if err, ok := err.(*jwt.ValidationError); ok {
		if err.Errors&jwt.ValidationErrorIssuedAt == jwt.ValidationErrorIssuedAt {
			return nil, "not-valid-yet", TokenNotValidYet
		}
	}
	if err != nil {
		return nil, reason, TokenAuthFailed
	}

	claims, ok := token.Claims.(*signaling.TokenClaims)
	if !ok || !token.Valid {
		return nil, "auth-failed", TokenAuthFailed
	}

	minIssuedAt := time.Now().Add(-maxTokenAge)
	if issuedAt := claims.IssuedAt; issuedAt != nil && issuedAt.Before(minIssuedAt) {
		return nil, "expired", TokenExpired
	}

	return claims, "", nil
}

func (s *ProxyServer) NewSession(hello *signaling.HelloProxyClientMessage) (*ProxySession, error) {
	if proxyDebugMessages {
		s.log.Debug("Hello received",
			zap.Any("hello", hello),
		)
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
		Created: time.Now(),
	}

	encoded, err := s.cookie.Encode("session", sessionIdData)
	if err != nil {
		return nil, err
	}

	s.log.Info("Created session",
		zap.String("sessionid", encoded),
		zap.Any("claims", claims),
	)
	session := NewProxySession(s.log, s, sid, encoded)
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

func (s *ProxyServer) deleteSessionLocked(id uint64) {
	if session, found := s.sessions[id]; found {
		delete(s.sessions, id)
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

func (s *ProxyServer) GetClientsLoad() (load int64, incoming int64, outgoing int64) {
	s.clientsLock.RLock()
	defer s.clientsLock.RUnlock()

	for _, c := range s.clients {
		bitrate := int64(c.MaxBitrate())
		load += bitrate
		if _, ok := c.(signaling.McuPublisher); ok {
			incoming += bitrate
		} else if _, ok := c.(signaling.McuSubscriber); ok {
			outgoing += bitrate
		}
	}
	load = load / 1024
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

func (s *ProxyServer) getStats() map[string]interface{} {
	result := map[string]interface{}{
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
		s.log.Error("Could not serialize stats",
			zap.Any("stats", stats),
			zap.Error(err),
		)
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

	conn, err := NewRemoteConnection(s.log, url, s.tokenId, s.tokenKey, s.remoteTlsConfig)
	if err != nil {
		return nil, err
	}

	s.remoteConnections[url] = conn
	return conn, nil
}
