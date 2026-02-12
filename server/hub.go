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
package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	unsafe "unsafe"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/client"
	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/container"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/session"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

var (
	// HelloExpected is returned if a client sends a message before the "hello" request.
	HelloExpected = api.NewError("hello_expected", "Expected Hello request.")
	// UserAuthFailed is returned if the Talk response to a v1 hello is not an error but also not a valid auth response.
	UserAuthFailed = api.NewError("auth_failed", "The user could not be authenticated.")
	// RoomJoinFailed is returned if the Talk response to a room join request is not an error and not a valid join response.
	RoomJoinFailed = api.NewError("room_join_failed", "Could not join the room.")
	// InvalidClientType is returned if the client type in the "hello" request is not supported.
	InvalidClientType = api.NewError("invalid_client_type", "The client type is not supported.")
	// InvalidBackendUrl is returned if no backend is configured for URL in the "hello" request.
	InvalidBackendUrl = api.NewError("invalid_backend", "The backend URL is not supported.")
	// InvalidToken is returned if the token in a "hello" request could not be validated.
	InvalidToken = api.NewError("invalid_token", "The passed token is invalid.")
	// NoSuchSession is returned if the session to be resumed is unknown or expired.
	NoSuchSession = api.NewError("no_such_session", "The session to resume does not exist.")
	// TokenNotValidYet is returned if the token in a "hello" request could be authenticated but is not valid yet.
	// This hints to a mismatch in the time between the server running Talk and the server running the signaling server.
	TokenNotValidYet = api.NewError("token_not_valid_yet", "The token is not valid yet.")
	// TokenExpired is returned if the token in a "hello" request could be authenticated but is expired.
	// This hints to a mismatch in the time between the server running Talk and the server running the signaling server,
	// but could also be a client trying to connect with an old token.
	TokenExpired = api.NewError("token_expired", "The token is expired.")
	// TooManyRequests is returned if brute force detection reports too many failed "hello" requests.
	TooManyRequests = api.NewError("too_many_requests", "Too many requests.")

	ErrNoProxyTokenSupported = errors.New("proxy token generation not supported")

	// Maximum number of concurrent requests to a backend.
	defaultMaxConcurrentRequestsPerHost = 8

	// Backend requests will be cancelled if they take too long.
	defaultBackendTimeoutSeconds = 10

	// MCU requests will be cancelled if they take too long.
	defaultMcuTimeoutSeconds = 10

	// Federation requests will be cancelled if they take too long.
	defaultFederationTimeoutSeconds = 10

	// New connections have to send a "Hello" request after 2 seconds.
	initialHelloTimeout = 2 * time.Second

	// Anonymous clients have to join a room after 10 seconds.
	anonmyousJoinRoomTimeout = 10 * time.Second

	// Sessions expire 30 seconds after the connection closed.
	sessionExpireDuration = 30 * time.Second

	// Run housekeeping jobs once per second
	housekeepingInterval = time.Second

	// Number of decoded session ids to keep.
	decodeCacheSize = 8192

	// Minimum length of random data for tokens.
	minTokenRandomLength = 32

	// Number of caches to use for keeping decoded session ids. The cache will
	// be selected based on the cache key to avoid lock contention.
	numDecodeCaches = 32

	// Buffer sizes when reading/writing websocket connections.
	websocketReadBufferSize  = 4096
	websocketWriteBufferSize = 4096

	websocketWriteBufferPool = &sync.Pool{}

	// Delay after which a screen publisher should be cleaned up.
	cleanupScreenPublisherDelay = time.Second

	// Delay after which a "cleared" / "rejected" dialout status should be removed.
	removeCallStatusTTL = 5 * time.Second

	// Allow time differences of up to one minute between server and proxy.
	tokenLeeway = time.Minute
)

func init() {
	RegisterHubStats()
}

type ClientWithSession interface {
	client.HandlerClient

	IsAuthenticated() bool
	GetSession() Session
	SetSession(session Session)
}

type Hub struct {
	version      string
	logger       log.Logger
	events       events.AsyncEvents
	upgrader     websocket.Upgrader
	sessionIds   *session.SessionIdCodec
	info         *api.WelcomeServerMessage
	infoInternal *api.WelcomeServerMessage
	welcome      atomic.Value // *api.ServerMessage

	closer          *internal.Closer
	readPumpActive  atomic.Int32
	writePumpActive atomic.Int32

	shutdown          *internal.Closer
	shutdownScheduled atomic.Bool

	roomUpdated      chan *talk.BackendServerRoomRequest
	roomDeleted      chan *talk.BackendServerRoomRequest
	roomInCall       chan *talk.BackendServerRoomRequest
	roomParticipants chan *talk.BackendServerRoomRequest

	mu sync.RWMutex
	ru sync.RWMutex

	sid atomic.Uint64
	// +checklocks:mu
	clients map[uint64]ClientWithSession
	// +checklocks:mu
	sessions map[uint64]Session
	// +checklocks:ru
	rooms map[string]*Room

	roomSessions RoomSessions
	roomPing     *RoomPing
	// +checklocks:mu
	virtualSessions map[api.PublicSessionId]uint64

	decodeCaches []*container.LruCache[*session.SessionIdData]

	mcu                   sfu.SFU
	mcuTimeout            time.Duration
	internalClientsSecret []byte

	allowSubscribeAnyStream bool

	// +checklocks:mu
	expiredSessions map[Session]time.Time
	// +checklocks:mu
	anonymousSessions map[*ClientSession]time.Time
	// +checklocks:mu
	expectHelloClients map[ClientWithSession]time.Time
	// +checklocks:mu
	dialoutSessions map[*ClientSession]bool
	// +checklocks:mu
	remoteSessions map[*RemoteSession]bool
	// +checklocks:mu
	federatedSessions map[*ClientSession]bool
	// +checklocks:mu
	federationClients map[*FederationClient]bool

	backendTimeout time.Duration
	backend        *talk.BackendClient

	trustedProxies atomic.Pointer[container.IPList]
	geoip          *geoip.Lookup
	geoipOverrides geoip.AtomicOverrides
	geoipUpdating  atomic.Bool

	etcdClient etcd.Client
	rpcServer  *grpc.Server
	rpcClients *grpc.Clients

	throttler async.Throttler

	skipFederationVerify bool
	federationTimeout    time.Duration

	allowedCandidates atomic.Pointer[container.IPList]
	blockedCandidates atomic.Pointer[container.IPList]
}

func NewHub(ctx context.Context, cfg *goconf.ConfigFile, events events.AsyncEvents, rpcServer *grpc.Server, rpcClients *grpc.Clients, etcdClient etcd.Client, r *mux.Router, version string) (*Hub, error) {
	logger := log.LoggerFromContext(ctx)
	hashKey, _ := config.GetStringOptionWithEnv(cfg, "sessions", "hashkey")
	switch len(hashKey) {
	case 32:
	case 64:
	default:
		logger.Printf("WARNING: The sessions hash key should be 32 or 64 bytes but is %d bytes", len(hashKey))
	}

	blockKey, _ := config.GetStringOptionWithEnv(cfg, "sessions", "blockkey")
	blockBytes := []byte(blockKey)
	switch len(blockKey) {
	case 0:
		blockBytes = nil
	case 16:
	case 24:
	case 32:
	default:
		return nil, fmt.Errorf("the sessions block key must be 16, 24 or 32 bytes but is %d bytes", len(blockKey))
	}

	sessionIds, err := session.NewSessionIdCodec([]byte(hashKey), blockBytes)
	if err != nil {
		return nil, fmt.Errorf("error creating session id codec: %w", err)
	}

	internalClientsSecret, _ := config.GetStringOptionWithEnv(cfg, "clients", "internalsecret")
	if internalClientsSecret == "" {
		logger.Println("WARNING: No shared secret has been set for internal clients.")
	}

	maxConcurrentRequestsPerHost, _ := cfg.GetInt("backend", "connectionsperhost")
	if maxConcurrentRequestsPerHost <= 0 {
		maxConcurrentRequestsPerHost = defaultMaxConcurrentRequestsPerHost
	}

	backend, err := talk.NewBackendClient(ctx, cfg, maxConcurrentRequestsPerHost, version, etcdClient)
	if err != nil {
		return nil, err
	}
	logger.Printf("Using a maximum of %d concurrent backend connections per host", maxConcurrentRequestsPerHost)

	backendTimeoutSeconds, _ := cfg.GetInt("backend", "timeout")
	if backendTimeoutSeconds <= 0 {
		backendTimeoutSeconds = defaultBackendTimeoutSeconds
	}
	backendTimeout := time.Duration(backendTimeoutSeconds) * time.Second
	logger.Printf("Using a timeout of %s for backend connections", backendTimeout)

	mcuTimeoutSeconds, _ := cfg.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second

	allowSubscribeAnyStream, _ := cfg.GetBool("app", "allowsubscribeany")
	if allowSubscribeAnyStream {
		logger.Printf("WARNING: Allow subscribing any streams, this is insecure and should only be enabled for testing")
	}

	trustedProxies, _ := cfg.GetString("app", "trustedproxies")
	trustedProxiesIps, err := container.ParseIPList(trustedProxies)
	if err != nil {
		return nil, err
	}

	skipFederationVerify, _ := cfg.GetBool("federation", "skipverify")
	if skipFederationVerify {
		logger.Println("WARNING: Federation target verification is disabled!")
	}
	federationTimeoutSeconds, _ := cfg.GetInt("federation", "timeout")
	if federationTimeoutSeconds <= 0 {
		federationTimeoutSeconds = defaultFederationTimeoutSeconds
	}
	federationTimeout := time.Duration(federationTimeoutSeconds) * time.Second

	if !trustedProxiesIps.Empty() {
		logger.Printf("Trusted proxies: %s", trustedProxiesIps)
	} else {
		trustedProxiesIps = client.DefaultTrustedProxies
		logger.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
	}

	decodeCaches := make([]*container.LruCache[*session.SessionIdData], 0, numDecodeCaches)
	for range numDecodeCaches {
		decodeCaches = append(decodeCaches, container.NewLruCache[*session.SessionIdData](decodeCacheSize))
	}

	roomSessions, err := NewBuiltinRoomSessions(rpcClients)
	if err != nil {
		return nil, err
	}

	roomPing, err := NewRoomPing(backend)
	if err != nil {
		return nil, err
	}

	geoipUrl, _ := cfg.GetString("geoip", "url")
	if geoipUrl == "default" || geoipUrl == "none" {
		geoipUrl = ""
	}
	if geoipUrl == "" {
		if geoipLicense, _ := cfg.GetString("geoip", "license"); geoipLicense != "" {
			geoipUrl = geoip.GetMaxMindDownloadUrl(geoipLicense)
		}
	}

	var geoipLookup *geoip.Lookup
	if geoipUrl != "" {
		if geoipUrl, found := strings.CutPrefix(geoipUrl, "file://"); found {
			logger.Printf("Using GeoIP database from %s", geoipUrl)
			geoipLookup, err = geoip.NewLookupFromFile(logger, geoipUrl)
		} else {
			logger.Printf("Downloading GeoIP database from %s", geoipUrl)
			geoipLookup, err = geoip.NewLookupFromUrl(logger, geoipUrl)
		}
		if err != nil {
			return nil, err
		}
	} else {
		logger.Printf("Not using GeoIP database")
	}

	geoipOverrides, err := geoip.LoadOverrides(ctx, cfg, false)
	if err != nil {
		return nil, err
	}

	throttler, err := async.NewMemoryThrottler()
	if err != nil {
		return nil, err
	}

	hub := &Hub{
		version: version,
		logger:  logger,
		events:  events,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
			WriteBufferPool: websocketWriteBufferPool,
			Subprotocols: []string{
				janus.EventsSubprotocol,
			},
		},
		sessionIds:   sessionIds,
		info:         api.NewWelcomeServerMessage(version, api.DefaultFeatures...),
		infoInternal: api.NewWelcomeServerMessage(version, api.DefaultFeaturesInternal...),

		closer:   internal.NewCloser(),
		shutdown: internal.NewCloser(),

		roomUpdated:      make(chan *talk.BackendServerRoomRequest),
		roomDeleted:      make(chan *talk.BackendServerRoomRequest),
		roomInCall:       make(chan *talk.BackendServerRoomRequest),
		roomParticipants: make(chan *talk.BackendServerRoomRequest),

		clients:  make(map[uint64]ClientWithSession),
		sessions: make(map[uint64]Session),
		rooms:    make(map[string]*Room),

		roomSessions:    roomSessions,
		roomPing:        roomPing,
		virtualSessions: make(map[api.PublicSessionId]uint64),

		decodeCaches: decodeCaches,

		mcuTimeout:            mcuTimeout,
		internalClientsSecret: []byte(internalClientsSecret),

		allowSubscribeAnyStream: allowSubscribeAnyStream,

		expiredSessions:    make(map[Session]time.Time),
		anonymousSessions:  make(map[*ClientSession]time.Time),
		expectHelloClients: make(map[ClientWithSession]time.Time),
		dialoutSessions:    make(map[*ClientSession]bool),
		remoteSessions:     make(map[*RemoteSession]bool),
		federatedSessions:  make(map[*ClientSession]bool),
		federationClients:  make(map[*FederationClient]bool),

		backendTimeout: backendTimeout,
		backend:        backend,

		geoip: geoipLookup,

		etcdClient: etcdClient,
		rpcServer:  rpcServer,
		rpcClients: rpcClients,

		throttler: throttler,

		skipFederationVerify: skipFederationVerify,
		federationTimeout:    federationTimeout,
	}
	if value, _ := cfg.GetString("mcu", "allowedcandidates"); value != "" {
		allowed, err := container.ParseIPList(value)
		if err != nil {
			return nil, fmt.Errorf("invalid allowedcandidates: %w", err)
		}

		logger.Printf("Candidates allowlist: %s", allowed)
		hub.allowedCandidates.Store(allowed)
	} else {
		logger.Printf("No candidates allowlist")
	}
	if value, _ := cfg.GetString("mcu", "blockedcandidates"); value != "" {
		blocked, err := container.ParseIPList(value)
		if err != nil {
			return nil, fmt.Errorf("invalid blockedcandidates: %w", err)
		}

		logger.Printf("Candidates blocklist: %s", blocked)
		hub.blockedCandidates.Store(blocked)
	} else {
		logger.Printf("No candidates blocklist")
	}

	hub.trustedProxies.Store(trustedProxiesIps)
	hub.geoipOverrides.Store(geoipOverrides)

	hub.setWelcomeMessage(&api.ServerMessage{
		Type:    "welcome",
		Welcome: api.NewWelcomeServerMessage(version, api.DefaultWelcomeFeatures...),
	})
	backend.SetFeaturesFunc(func() []string {
		return hub.info.Features
	})
	roomPing.hub = hub
	if rpcServer != nil {
		rpcServer.SetHub(hub)
	}
	hub.upgrader.CheckOrigin = hub.checkOrigin
	r.HandleFunc("/spreed", func(w http.ResponseWriter, r *http.Request) {
		hub.serveWs(w, r)
	})

	return hub, nil
}

func (h *Hub) setWelcomeMessage(msg *api.ServerMessage) {
	h.welcome.Store(msg)
}

func (h *Hub) getWelcomeMessage() *api.ServerMessage {
	return h.welcome.Load().(*api.ServerMessage)
}

func (h *Hub) SetMcu(mcu sfu.SFU) {
	h.mcu = mcu
	// Create copy of message so it can be updated concurrently.
	welcome := *h.getWelcomeMessage()
	if mcu == nil {
		h.info.RemoveFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)
		h.infoInternal.RemoveFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)

		welcome.Welcome.RemoveFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)
	} else {
		h.logger.Printf("Using a timeout of %s for MCU requests", h.mcuTimeout)
		h.info.AddFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)
		h.infoInternal.AddFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)

		welcome.Welcome.AddFeature(api.ServerFeatureMcu, api.ServerFeatureSimulcast, api.ServerFeatureUpdateSdp)
	}
	h.setWelcomeMessage(&welcome)
}

func (h *Hub) checkOrigin(r *http.Request) bool {
	// We allow any Origin to connect to the service.
	return true
}

func (h *Hub) GetServerInfo(session Session) *api.WelcomeServerMessage {
	if session.ClientType() == api.HelloClientTypeInternal {
		return h.infoInternal
	}

	return h.info
}

func (h *Hub) updateGeoDatabase() {
	if h.geoip == nil {
		return
	}

	if !h.geoipUpdating.CompareAndSwap(false, true) {
		// Already updating
		return
	}

	defer h.geoipUpdating.Store(false)
	backoff, err := async.NewExponentialBackoff(time.Second, 5*time.Minute)
	if err != nil {
		h.logger.Printf("Could not create exponential backoff: %s", err)
		return
	}

	for !h.closer.IsClosed() {
		err := h.geoip.Update()
		if err == nil {
			break
		}

		h.logger.Printf("Could not update GeoIP database, will retry in %s (%s)", backoff.NextWait(), err)
		backoff.Wait(context.Background())
	}
}

func (h *Hub) Run() {
	go h.updateGeoDatabase()
	h.roomPing.Start()
	defer h.roomPing.Stop()
	defer h.backend.Close()

	housekeeping := time.NewTicker(housekeepingInterval)
	federationPing := time.NewTicker(updateActiveSessionsInterval)
	geoipUpdater := time.NewTicker(24 * time.Hour)

loop:
	for {
		select {
		// Backend notifications from Nextcloud.
		case message := <-h.roomUpdated:
			h.processRoomUpdated(message)
		case message := <-h.roomDeleted:
			h.processRoomDeleted(message)
		case message := <-h.roomInCall:
			h.processRoomInCallChanged(message)
		case message := <-h.roomParticipants:
			h.processRoomParticipants(message)
		// Periodic internal housekeeping.
		case now := <-housekeeping.C:
			h.performHousekeeping(now)
		case <-geoipUpdater.C:
			go h.updateGeoDatabase()
		case <-federationPing.C:
			go h.publishFederatedSessions()
		case <-h.closer.C:
			break loop
		}
	}
	if h.geoip != nil {
		h.geoip.Close()
	}
}

func (h *Hub) Stop() {
	h.closer.Close()
	h.throttler.Close()
}

func (h *Hub) Reload(ctx context.Context, config *goconf.ConfigFile) {
	trustedProxies, _ := config.GetString("app", "trustedproxies")
	if trustedProxiesIps, err := container.ParseIPList(trustedProxies); err == nil {
		if !trustedProxiesIps.Empty() {
			h.logger.Printf("Trusted proxies: %s", trustedProxiesIps)
		} else {
			trustedProxiesIps = client.DefaultTrustedProxies
			h.logger.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
		}
		h.trustedProxies.Store(trustedProxiesIps)
	} else {
		h.logger.Printf("Error parsing trusted proxies from \"%s\": %s", trustedProxies, err)
	}

	geoipOverrides, _ := geoip.LoadOverrides(ctx, config, true)
	h.geoipOverrides.Store(geoipOverrides)

	if value, _ := config.GetString("mcu", "allowedcandidates"); value != "" {
		if allowed, err := container.ParseIPList(value); err != nil {
			h.logger.Printf("invalid allowedcandidates: %s", err)
		} else {
			h.logger.Printf("Candidates allowlist: %s", allowed)
			h.allowedCandidates.Store(allowed)
		}
	} else {
		h.logger.Printf("No candidates allowlist")
		h.allowedCandidates.Store(nil)
	}
	if value, _ := config.GetString("mcu", "blockedcandidates"); value != "" {
		if blocked, err := container.ParseIPList(value); err != nil {
			h.logger.Printf("invalid blockedcandidates: %s", err)
		} else {
			h.logger.Printf("Candidates blocklist: %s", blocked)
			h.blockedCandidates.Store(blocked)
		}
	} else {
		h.logger.Printf("No candidates blocklist")
		h.blockedCandidates.Store(nil)
	}

	if h.mcu != nil {
		h.mcu.Reload(config)
	}
	h.backend.Reload(config)
	h.rpcClients.Reload(config)
}

func (h *Hub) getDecodeCache(cache_key string) *container.LruCache[*session.SessionIdData] {
	hash := fnv.New32a()
	// Make sure we don't have a temporary allocation for the string -> []byte conversion.
	hash.Write(unsafe.Slice(unsafe.StringData(cache_key), len(cache_key))) // nolint
	idx := hash.Sum32() % uint32(len(h.decodeCaches))
	return h.decodeCaches[idx]
}

func (h *Hub) invalidatePublicSessionId(id api.PublicSessionId) {
	h.invalidateSessionId(string(id))
}

func (h *Hub) invalidatePrivateSessionId(id api.PrivateSessionId) {
	h.invalidateSessionId(string(id))
}

func (h *Hub) invalidateSessionId(id string) {
	if len(id) == 0 {
		return
	}

	cache := h.getDecodeCache(id)
	cache.Remove(id)
}

func (h *Hub) setDecodedPublicSessionId(id api.PublicSessionId, data *session.SessionIdData) {
	h.setDecodedSessionId(string(id), data)
}

func (h *Hub) setDecodedPrivateSessionId(id api.PrivateSessionId, data *session.SessionIdData) {
	h.setDecodedSessionId(string(id), data)
}

func (h *Hub) setDecodedSessionId(id string, data *session.SessionIdData) {
	if len(id) == 0 {
		return
	}

	cache := h.getDecodeCache(id)
	cache.Set(id, data)
}

func (h *Hub) decodePrivateSessionId(id api.PrivateSessionId) *session.SessionIdData {
	if len(id) == 0 {
		return nil
	}

	cache_key := string(id)
	cache := h.getDecodeCache(cache_key)
	if result := cache.Get(cache_key); result != nil {
		return result
	}

	data, err := h.sessionIds.DecodePrivate(id)
	if err != nil {
		return nil
	}

	cache.Set(cache_key, data)
	return data
}

func (h *Hub) decodePublicSessionId(id api.PublicSessionId) *session.SessionIdData {
	if len(id) == 0 {
		return nil
	}

	cache_key := string(id)
	cache := h.getDecodeCache(cache_key)
	if result := cache.Get(cache_key); result != nil {
		return result
	}

	data, err := h.sessionIds.DecodePublic(id)
	if err != nil {
		return nil
	}

	cache.Set(cache_key, data)
	return data
}

func (h *Hub) GetSessionByPublicId(sessionId api.PublicSessionId) Session {
	data := h.decodePublicSessionId(sessionId)
	if data == nil {
		return nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	session := h.sessions[data.Sid]
	if session != nil && session.PublicId() != sessionId {
		// Session was created on different server.
		return nil
	}
	return session
}

func (h *Hub) GetSessionByResumeId(resumeId api.PrivateSessionId) Session {
	data := h.decodePrivateSessionId(resumeId)
	if data == nil {
		return nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	session := h.sessions[data.Sid]
	if session != nil && session.PrivateId() != resumeId {
		// Session was created on different server.
		return nil
	}
	return session
}

func (h *Hub) GetSessionIdByResumeId(resumeId api.PrivateSessionId) api.PublicSessionId {
	session := h.GetSessionByResumeId(resumeId)
	if session == nil {
		return ""
	}

	return session.PublicId()
}

func (h *Hub) GetSessionIdByRoomSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error) {
	return h.roomSessions.GetSessionId(roomSessionId)
}

func (h *Hub) IsSessionIdInCall(sessionId api.PublicSessionId, roomId string, backendUrl string) (bool, bool) {
	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		return false, false
	}

	inCall := true
	room := session.GetRoom()
	if room == nil || room.Id() != roomId || !room.Backend().HasUrl(backendUrl) ||
		(session.ClientType() != api.HelloClientTypeInternal && !room.IsSessionInCall(session)) {
		// Recipient is not in a room, a different room or not in the call.
		inCall = false
	}

	return inCall, true
}

func (h *Hub) GetPublisherIdForSessionId(ctx context.Context, sessionId api.PublicSessionId, streamType sfu.StreamType) (*grpc.GetPublisherIdReply, error) {
	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	clientSession, ok := session.(*ClientSession)
	if !ok {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	publisher := clientSession.GetOrWaitForPublisher(ctx, streamType)
	if publisher, ok := publisher.(sfu.PublisherWithConnectionUrlAndIP); ok {
		connUrl, ip := publisher.GetConnectionURL()
		reply := &grpc.GetPublisherIdReply{
			PublisherId: publisher.Id(),
			ProxyUrl:    connUrl,
		}
		if len(ip) > 0 {
			reply.Ip = ip.String()
		}
		var err error
		if reply.ConnectToken, err = h.CreateProxyToken(""); err != nil && !errors.Is(err, ErrNoProxyTokenSupported) {
			h.logger.Printf("Error creating proxy token for connection: %s", err)
			return nil, status.Error(codes.Internal, "error creating proxy connect token")
		}
		if reply.PublisherToken, err = h.CreateProxyToken(publisher.Id()); err != nil && !errors.Is(err, ErrNoProxyTokenSupported) {
			h.logger.Printf("Error creating proxy token for publisher %s: %s", publisher.Id(), err)
			return nil, status.Error(codes.Internal, "error creating proxy publisher token")
		}
		return reply, nil
	}

	return nil, status.Error(codes.NotFound, "no such publisher")
}

func (h *Hub) GetDialoutSessions(roomId string, backend *talk.Backend) (result []*ClientSession) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for session := range h.dialoutSessions {
		if !backend.HasUrl(session.BackendUrl()) {
			continue
		}

		if session.GetClient() != nil {
			result = append(result, session)
		}
	}

	return
}

func (h *Hub) GetBackend(u *url.URL) *talk.Backend {
	if u == nil {
		return h.backend.GetCompatBackend()
	}
	return h.backend.GetBackend(u)
}

func (h *Hub) CreateProxyToken(publisherId string) (string, error) {
	withToken, ok := h.mcu.(sfu.WithToken)
	if !ok {
		return "", ErrNoProxyTokenSupported
	}

	return withToken.CreateToken(publisherId)
}

// +checklocks:h.mu
func (h *Hub) checkExpiredSessions(now time.Time) {
	for session, expires := range h.expiredSessions {
		if now.After(expires) {
			h.mu.Unlock()
			h.logger.Printf("Closing expired session %s (private=%s)", session.PublicId(), session.PrivateId())
			session.Close()
			h.mu.Lock()
			// Should already be deleted by the close code, but better be sure.
			delete(h.expiredSessions, session)
		}
	}
}

// +checklocks:h.mu
func (h *Hub) checkAnonymousSessions(now time.Time) {
	for session, timeout := range h.anonymousSessions {
		if now.After(timeout) {
			// This will close the client connection.
			h.mu.Unlock()
			if client := session.GetClient(); client != nil {
				client.SendByeResponseWithReason(nil, "room_join_timeout")
			}
			session.Close()
			h.mu.Lock()
		}
	}
}

// +checklocks:h.mu
func (h *Hub) checkInitialHello(now time.Time) {
	for client, timeout := range h.expectHelloClients {
		if now.After(timeout) {
			// This will close the client connection.
			h.mu.Unlock()
			client.SendByeResponseWithReason(nil, "hello_timeout")
			h.mu.Lock()
		}
	}
}

func (h *Hub) performHousekeeping(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.checkExpiredSessions(now)
	h.checkAnonymousSessions(now)
	h.checkInitialHello(now)
}

func (h *Hub) removeSession(session Session) (removed bool) {
	session.LeaveRoom(true)
	h.invalidatePrivateSessionId(session.PrivateId())
	h.invalidatePublicSessionId(session.PublicId())

	h.mu.Lock()
	if data := session.Data(); data != nil && data.Sid > 0 {
		delete(h.clients, data.Sid)
		if _, found := h.sessions[data.Sid]; found {
			delete(h.sessions, data.Sid)
			statsHubSessionsCurrent.WithLabelValues(session.Backend().Id(), string(session.ClientType())).Dec()
			removed = true
		}
	}
	delete(h.expiredSessions, session)
	if session, ok := session.(*ClientSession); ok {
		delete(h.federatedSessions, session)
		delete(h.anonymousSessions, session)
		delete(h.dialoutSessions, session)
	}
	if h.IsShutdownScheduled() && !h.hasSessionsLocked(false) {
		go h.shutdown.Close()
	}
	h.mu.Unlock()
	return
}

func (h *Hub) removeFederationClient(client *FederationClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.federationClients, client)
}

// +checklocksread:h.mu
func (h *Hub) hasSessionsLocked(withInternal bool) bool {
	if withInternal {
		return len(h.sessions) > 0
	}

	for _, s := range h.sessions {
		if s.ClientType() != api.HelloClientTypeInternal {
			return true
		}
	}

	return false
}

func (h *Hub) startWaitAnonymousSessionRoom(session *ClientSession) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.startWaitAnonymousSessionRoomLocked(session)
}

// +checklocks:h.mu
func (h *Hub) startWaitAnonymousSessionRoomLocked(session *ClientSession) {
	if session.ClientType() == api.HelloClientTypeInternal {
		// Internal clients don't need to join a room.
		return
	}

	// Anonymous sessions must join a public room within a given time,
	// otherwise they get disconnected to avoid blocking resources forever.
	now := time.Now()
	h.anonymousSessions[session] = now.Add(anonmyousJoinRoomTimeout)
}

func (h *Hub) startExpectHello(client ClientWithSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !client.IsConnected() {
		return
	}

	if client.IsAuthenticated() {
		return
	}

	// Clients must send a "Hello" request to get a session within a given time.
	now := time.Now()
	h.expectHelloClients[client] = now.Add(initialHelloTimeout)
}

func (h *Hub) processNewClient(client ClientWithSession) {
	h.startExpectHello(client)
	h.sendWelcome(client)
}

func (h *Hub) sendWelcome(client ClientWithSession) {
	client.SendMessage(h.getWelcomeMessage())
}

func (h *Hub) registerClient(client ClientWithSession) uint64 {
	sid := h.sid.Add(1)
	for sid == 0 {
		sid = h.sid.Add(1)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[sid] = client
	return sid
}

func (h *Hub) unregisterClient(sid uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, sid)
}

func (h *Hub) unregisterRemoteSession(session *RemoteSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.remoteSessions, session)
}

func (h *Hub) newSessionIdData(backend *talk.Backend) *session.SessionIdData {
	sid := h.sid.Add(1)
	for sid == 0 {
		sid = h.sid.Add(1)
	}
	sessionIdData := &session.SessionIdData{
		Sid:       sid,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	return sessionIdData
}

func (h *Hub) processRegister(client ClientWithSession, message *api.ClientMessage, backend *talk.Backend, auth *talk.BackendClientResponse) {
	if !client.IsConnected() {
		// Client disconnected while waiting for "hello" response.
		return
	}

	if auth.Type == "error" {
		client.SendMessage(message.NewErrorServerMessage(auth.Error))
		return
	} else if auth.Type != "auth" {
		client.SendMessage(message.NewErrorServerMessage(UserAuthFailed))
		return
	}

	sessionIdData := h.newSessionIdData(backend)
	privateSessionId, err := h.sessionIds.EncodePrivate(sessionIdData)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}
	publicSessionId, err := h.sessionIds.EncodePublic(sessionIdData)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	userId := auth.Auth.UserId
	if userId != "" {
		h.logger.Printf("Register user %s@%s from %s in %s (%s) %s (private=%s)", userId, backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	} else if message.Hello.Auth.Type != api.HelloClientTypeClient {
		h.logger.Printf("Register %s@%s from %s in %s (%s) %s (private=%s)", message.Hello.Auth.Type, backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	} else {
		h.logger.Printf("Register anonymous@%s from %s in %s (%s) %s (private=%s)", backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	}

	session, err := NewClientSession(h, privateSessionId, publicSessionId, sessionIdData, backend, message.Hello, auth.Auth)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	if err := backend.AddSession(session); err != nil {
		h.logger.Printf("Error adding session %s to backend %s: %s", session.PublicId(), backend.Id(), err)
		session.Close()
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	if limit := uint32(backend.Limit()); limit > 0 && h.rpcClients != nil {
		var totalCount atomic.Uint32
		totalCount.Add(uint32(backend.Len()))
		var wg sync.WaitGroup
		ctx, cancel := context.WithTimeout(client.Context(), time.Second)
		defer cancel()
		for _, grpcClient := range h.rpcClients.GetClients() {
			wg.Go(func() {
				count, err := grpcClient.GetSessionCount(ctx, session.BackendUrl())
				if err != nil {
					h.logger.Printf("Received error while getting session count for %s from %s: %s", session.BackendUrl(), grpcClient.Target(), err)
					return
				}

				if count > 0 {
					h.logger.Printf("%d sessions connected for %s on %s", count, session.BackendUrl(), grpcClient.Target())
					totalCount.Add(count)
				}
			})
		}
		wg.Wait()
		if totalCount.Load() > limit {
			backend.RemoveSession(session)
			h.logger.Printf("Error adding session %s to backend %s: %s", session.PublicId(), backend.Id(), talk.SessionLimitExceeded)
			session.Close()
			client.SendMessage(message.NewWrappedErrorServerMessage(talk.SessionLimitExceeded))
			return
		}
	}

	h.mu.Lock()
	if !client.IsConnected() {
		// Client disconnected while waiting for backend response.
		h.mu.Unlock()

		session.Close()
		return
	}

	session.SetClient(client)
	h.sessions[sessionIdData.Sid] = session
	h.clients[sessionIdData.Sid] = client
	delete(h.expectHelloClients, client)
	if userId == "" && session.ClientType() != api.HelloClientTypeInternal {
		h.startWaitAnonymousSessionRoomLocked(session)
	} else if session.ClientType() == api.HelloClientTypeInternal && session.HasFeature(api.ClientFeatureStartDialout) {
		// TODO: There is a small race condition for sessions that take some time
		// between connecting and joining a room.
		h.dialoutSessions[session] = true
	}
	h.mu.Unlock()

	if country := client.Country(); geoip.IsValidCountry(country) {
		statsClientCountries.WithLabelValues(string(country)).Inc()
	}
	statsHubSessionsCurrent.WithLabelValues(backend.Id(), string(session.ClientType())).Inc()
	statsHubSessionsTotal.WithLabelValues(backend.Id(), string(session.ClientType())).Inc()

	h.setDecodedPrivateSessionId(privateSessionId, sessionIdData)
	h.setDecodedPublicSessionId(publicSessionId, sessionIdData)
	h.sendHelloResponse(session, message)
}

func (h *Hub) processUnregister(client ClientWithSession) Session {
	session := client.GetSession()

	h.mu.Lock()
	delete(h.expectHelloClients, client)
	if session != nil {
		delete(h.clients, session.Data().Sid)
		now := time.Now()
		h.expiredSessions[session] = now.Add(sessionExpireDuration)
	}
	h.mu.Unlock()
	if session != nil {
		h.logger.Printf("Unregister %s (private=%s)", session.PublicId(), session.PrivateId())
		if cs, ok := session.(*ClientSession); ok {
			cs.ClearClient(client)
		}
	}

	client.Close()
	return session
}

func (h *Hub) processMessage(client ClientWithSession, data []byte) {
	var message api.ClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		if session := client.GetSession(); session != nil {
			h.logger.Printf("Error decoding message from client %s: %v", session.PublicId(), err)
			session.SendError(InvalidFormat)
		} else {
			h.logger.Printf("Error decoding message from %s: %v", client.RemoteAddr(), err)
			client.SendError(InvalidFormat)
		}
		return
	}

	if err := message.CheckValid(); err != nil {
		if session := client.GetSession(); session != nil {
			h.logger.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
			if err, ok := err.(*api.Error); ok {
				session.SendMessage(message.NewErrorServerMessage(err))
			} else {
				session.SendMessage(message.NewErrorServerMessage(InvalidFormat))
			}
		} else {
			h.logger.Printf("Invalid message %+v from %s: %v", message, client.RemoteAddr(), err)
			if err, ok := err.(*api.Error); ok {
				client.SendMessage(message.NewErrorServerMessage(err))
			} else {
				client.SendMessage(message.NewErrorServerMessage(InvalidFormat))
			}
		}
		return
	}

	statsMessagesTotal.WithLabelValues(message.Type).Inc()

	session := client.GetSession()
	if session == nil {
		if message.Type != "hello" {
			client.SendMessage(message.NewErrorServerMessage(HelloExpected))
			return
		}

		h.processHello(client, &message)
		return
	}

	isLocalMessage := message.Type == "room" ||
		message.Type == "hello" ||
		message.Type == "bye"
	if cs, ok := session.(*ClientSession); ok && !isLocalMessage {
		if federated := cs.GetFederationClient(); federated != nil {
			if err := federated.ProxyMessage(&message); err != nil {
				client.SendMessage(message.NewWrappedErrorServerMessage(err))
			}
			return
		}
	}

	switch message.Type {
	case "room":
		h.processRoom(session, &message)
	case "message":
		h.processMessageMsg(session, &message)
	case "control":
		h.processControlMsg(session, &message)
	case "internal":
		h.processInternalMsg(session, &message)
	case "transient":
		h.processTransientMsg(session, &message)
	case "bye":
		h.processByeMsg(client, &message)
	case "hello":
		h.logger.Printf("Ignore hello %+v for already authenticated connection %s", message.Hello, session.PublicId())
	default:
		h.logger.Printf("Ignore unknown message %+v from %s", message, session.PublicId())
	}
}

func (h *Hub) sendHelloResponse(session *ClientSession, message *api.ClientMessage) bool {
	response := &api.ServerMessage{
		Id:   message.Id,
		Type: "hello",
		Hello: &api.HelloServerMessage{
			Version:   message.Hello.Version,
			SessionId: session.PublicId(),
			ResumeId:  session.PrivateId(),
			UserId:    session.UserId(),
			Server:    h.GetServerInfo(session),
		},
	}
	return session.SendMessage(response)
}

type remoteClientInfo struct {
	client   *grpc.Client
	response *grpc.LookupResumeIdReply
}

func (h *Hub) tryProxyResume(c ClientWithSession, resumeId api.PrivateSessionId, message *api.ClientMessage) bool {
	client, ok := c.(*HubClient)
	if !ok {
		return false
	}

	var clients []*grpc.Client
	if h.rpcClients != nil {
		clients = h.rpcClients.GetClients()
	}
	if len(clients) == 0 {
		return false
	}

	rpcCtx, rpcCancel := context.WithTimeout(client.Context(), 5*time.Second)
	defer rpcCancel()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(rpcCtx)
	defer cancel()

	var remoteClient atomic.Pointer[remoteClientInfo]
	for _, grpcClient := range clients {
		wg.Go(func() {
			if grpcClient.IsSelf() {
				return
			}

			response, err := grpcClient.LookupResumeId(ctx, resumeId)
			if err != nil {
				h.logger.Printf("Could not lookup resume id %s on %s: %s", resumeId, grpcClient.Target(), err)
				return
			}

			cancel()
			remoteClient.CompareAndSwap(nil, &remoteClientInfo{
				client:   grpcClient,
				response: response,
			})
		})
	}
	wg.Wait()

	if !client.IsConnected() {
		// Client disconnected while checking message.
		return false
	}

	info := remoteClient.Load()
	if info == nil {
		return false
	}

	rs, err := NewRemoteSession(h, client, info.client, api.PublicSessionId(info.response.SessionId))
	if err != nil {
		h.logger.Printf("Could not create remote session %s on %s: %s", info.response.SessionId, info.client.Target(), err)
		return false
	}

	if err := rs.Start(message); err != nil {
		rs.Close()
		h.logger.Printf("Could not start remote session %s on %s: %s", info.response.SessionId, info.client.Target(), err)
		return false
	}

	h.logger.Printf("Proxy session %s to %s", info.response.SessionId, info.client.Target())
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remoteSessions[rs] = true
	delete(h.expectHelloClients, client)
	return true
}

func (h *Hub) processHello(client ClientWithSession, message *api.ClientMessage) {
	ctx := log.NewLoggerContext(client.Context(), h.logger)
	resumeId := message.Hello.ResumeId
	if resumeId != "" {
		throttle, err := h.throttler.CheckBruteforce(ctx, client.RemoteAddr(), "HelloResume")
		if err == async.ErrBruteforceDetected {
			client.SendMessage(message.NewErrorServerMessage(TooManyRequests))
			return
		} else if err != nil {
			h.logger.Printf("Error checking for bruteforce: %s", err)
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		data := h.decodePrivateSessionId(resumeId)
		if data == nil {
			statsHubSessionResumeFailed.Inc()
			if h.tryProxyResume(client, resumeId, message) {
				return
			}

			throttle(ctx)
			client.SendMessage(message.NewErrorServerMessage(NoSuchSession))
			return
		}

		h.mu.Lock()
		session, found := h.sessions[data.Sid]
		if !found || resumeId != session.PrivateId() {
			h.mu.Unlock()
			statsHubSessionResumeFailed.Inc()
			if h.tryProxyResume(client, resumeId, message) {
				return
			}

			// NOTE: we don't throttle if the resume id syntax is valid but the session has expired already.
			client.SendMessage(message.NewErrorServerMessage(NoSuchSession))
			return
		}

		clientSession, ok := session.(*ClientSession)
		if !ok {
			// Should never happen as clients only can resume their own sessions.
			h.mu.Unlock()
			h.logger.Printf("Client resumed non-client session %s (private=%s)", session.PublicId(), session.PrivateId())
			statsHubSessionResumeFailed.Inc()
			client.SendMessage(message.NewErrorServerMessage(NoSuchSession))
			return
		}

		if !client.IsConnected() {
			// Client disconnected while checking message.
			h.mu.Unlock()
			return
		}

		if prev := clientSession.SetClient(client); prev != nil {
			h.logger.Printf("Closing previous client from %s for session %s", prev.RemoteAddr(), session.PublicId())
			prev.SendByeResponseWithReason(nil, "session_resumed")
		}

		delete(h.expiredSessions, clientSession)
		h.clients[data.Sid] = client
		delete(h.expectHelloClients, client)
		h.mu.Unlock()

		h.logger.Printf("Resume session from %s in %s (%s) %s (private=%s)", client.RemoteAddr(), client.Country(), client.UserAgent(), session.PublicId(), session.PrivateId())

		statsHubSessionsResumedTotal.WithLabelValues(clientSession.Backend().Id(), string(clientSession.ClientType())).Inc()
		h.sendHelloResponse(clientSession, message)
		clientSession.NotifySessionResumed(client)
		return
	}

	// Make sure client doesn't get disconnected while calling auth backend.
	h.mu.Lock()
	delete(h.expectHelloClients, client)
	h.mu.Unlock()

	switch message.Hello.Auth.Type {
	case api.HelloClientTypeClient:
		fallthrough
	case api.HelloClientTypeFederation:
		h.processHelloClient(client, message)
	case api.HelloClientTypeInternal:
		h.processHelloInternal(client, message)
	default:
		h.startExpectHello(client)
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
	}
}

func (h *Hub) processHelloV1(ctx context.Context, client ClientWithSession, message *api.ClientMessage) (*talk.Backend, *talk.BackendClientResponse, error) {
	url := message.Hello.Auth.ParsedUrl
	backend := h.backend.GetBackend(url)
	if backend == nil {
		return nil, nil, InvalidBackendUrl
	}

	url = url.JoinPath(PathToOcsSignalingBackend)

	// Run in timeout context to prevent blocking too long.
	ctx, cancel := context.WithTimeout(ctx, h.backendTimeout)
	defer cancel()

	var auth talk.BackendClientResponse
	request := talk.NewBackendClientAuthRequest(message.Hello.Auth.Params)
	if err := h.backend.PerformJSONRequest(ctx, url, request, &auth); err != nil {
		return nil, nil, err
	}

	// TODO(jojo): Validate response

	return backend, &auth, nil
}

func (h *Hub) processHelloV2(ctx context.Context, client ClientWithSession, message *api.ClientMessage) (*talk.Backend, *talk.BackendClientResponse, error) {
	url := message.Hello.Auth.ParsedUrl
	backend := h.backend.GetBackend(url)
	if backend == nil {
		return nil, nil, InvalidBackendUrl
	}

	var tokenString string
	var tokenClaims jwt.Claims
	switch message.Hello.Auth.Type {
	case api.HelloClientTypeClient:
		tokenString = message.Hello.Auth.HelloV2Params.Token
		tokenClaims = &api.HelloV2TokenClaims{}
	case api.HelloClientTypeFederation:
		if !h.backend.HasCapabilityFeature(ctx, url, talk.FeatureFederationV2) {
			return nil, nil, ErrFederationNotSupported
		}

		tokenString = message.Hello.Auth.FederationParams.Token
		tokenClaims = &api.FederationTokenClaims{}
	default:
		return nil, nil, InvalidClientType
	}
	token, err := jwt.ParseWithClaims(tokenString, tokenClaims, func(token *jwt.Token) (any, error) {
		// Only public-private-key algorithms are supported.
		var loadKeyFunc func([]byte) (any, error)
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			loadKeyFunc = func(data []byte) (any, error) {
				return jwt.ParseRSAPublicKeyFromPEM(data)
			}
		case *jwt.SigningMethodECDSA:
			loadKeyFunc = func(data []byte) (any, error) {
				return jwt.ParseECPublicKeyFromPEM(data)
			}
		case *jwt.SigningMethodEd25519:
			loadKeyFunc = func(data []byte) (any, error) {
				if !bytes.HasPrefix(data, []byte("-----BEGIN ")) {
					// Nextcloud sends the Ed25519 key as base64-encoded public key data.
					decoded, err := base64.StdEncoding.DecodeString(string(data))
					if err != nil {
						return nil, err
					}

					key := ed25519.PublicKey(decoded)
					data, err = x509.MarshalPKIXPublicKey(key)
					if err != nil {
						return nil, err
					}

					data = pem.EncodeToMemory(&pem.Block{
						Type:  "PUBLIC KEY",
						Bytes: data,
					})
				}
				return jwt.ParseEdPublicKeyFromPEM(data)
			}
		default:
			h.logger.Printf("Unexpected signing method: %v", token.Header["alg"])
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		// Run in timeout context to prevent blocking too long.
		backendCtx, cancel := context.WithTimeout(ctx, h.backendTimeout)
		defer cancel()

		keyData, cached, found := h.backend.GetStringConfig(backendCtx, url, talk.ConfigGroupSignaling, talk.ConfigKeyHelloV2TokenKey)
		if !found {
			if cached {
				// The Nextcloud instance might just have enabled JWT but we probably use
				// the cached capabilities without the public key. Make sure to re-fetch.
				h.backend.InvalidateCapabilities(url)
				keyData, _, found = h.backend.GetStringConfig(backendCtx, url, talk.ConfigGroupSignaling, talk.ConfigKeyHelloV2TokenKey)
			}
			if !found {
				return nil, errors.New("no key found for issuer")
			}
		}

		key, err := loadKeyFunc([]byte(keyData))
		if err != nil {
			return nil, fmt.Errorf("could not parse token key: %w", err)
		}

		return key, nil
	}, jwt.WithValidMethods([]string{
		jwt.SigningMethodRS256.Alg(),
		jwt.SigningMethodRS384.Alg(),
		jwt.SigningMethodRS512.Alg(),
		jwt.SigningMethodES256.Alg(),
		jwt.SigningMethodES384.Alg(),
		jwt.SigningMethodES512.Alg(),
		jwt.SigningMethodEdDSA.Alg(),
	}), jwt.WithIssuedAt(), jwt.WithLeeway(tokenLeeway))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenNotValidYet) || errors.Is(err, jwt.ErrTokenUsedBeforeIssued) {
			return nil, nil, TokenNotValidYet
		} else if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, nil, TokenExpired
		}

		return nil, nil, InvalidToken
	}

	var authTokenClaims api.AuthTokenClaims
	switch message.Hello.Auth.Type {
	case api.HelloClientTypeClient:
		claims, ok := token.Claims.(*api.HelloV2TokenClaims)
		if !ok || !token.Valid {
			return nil, nil, InvalidToken
		}
		authTokenClaims = claims
	case api.HelloClientTypeFederation:
		claims, ok := token.Claims.(*api.FederationTokenClaims)
		if !ok || !token.Valid {
			return nil, nil, InvalidToken
		}
		authTokenClaims = claims
	}

	issuedAt, err := authTokenClaims.GetIssuedAt()
	if err != nil {
		return nil, nil, InvalidToken
	}
	expiresAt, err := authTokenClaims.GetExpirationTime()
	if err != nil {
		return nil, nil, InvalidToken
	}
	now := time.Now()
	if issuedAt != nil && expiresAt != nil && expiresAt.Before(issuedAt.Time) {
		return nil, nil, TokenExpired
	} else if issuedAt == nil {
		return nil, nil, TokenNotValidYet
	} else if minExpiresAt := now.Add(-tokenLeeway); expiresAt == nil || expiresAt.Before(minExpiresAt) {
		return nil, nil, TokenExpired
	}

	subject, err := authTokenClaims.GetSubject()
	if err != nil {
		return nil, nil, InvalidToken
	}

	auth := &talk.BackendClientResponse{
		Type: "auth",
		Auth: &talk.BackendClientAuthResponse{
			Version: message.Hello.Version,
			UserId:  subject,
			User:    authTokenClaims.GetUserData(),
		},
	}
	return backend, auth, nil
}

func (h *Hub) processHelloClient(client ClientWithSession, message *api.ClientMessage) {
	// Make sure the client must send another "hello" in case of errors.
	defer h.startExpectHello(client)

	var authFunc func(context.Context, ClientWithSession, *api.ClientMessage) (*talk.Backend, *talk.BackendClientResponse, error)
	switch message.Hello.Version {
	case api.HelloVersionV1:
		// Auth information contains a ticket that must be validated against the
		// Nextcloud instance.
		authFunc = h.processHelloV1
	case api.HelloVersionV2:
		// Auth information contains a JWT that contains all information of the user.
		authFunc = h.processHelloV2
	default:
		client.SendMessage(message.NewErrorServerMessage(api.InvalidHelloVersion))
		return
	}

	backend, auth, err := authFunc(client.Context(), client, message)
	if err != nil {
		if e, ok := err.(*api.Error); ok {
			client.SendMessage(message.NewErrorServerMessage(e))
		} else {
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
		}
		return
	}

	h.processRegister(client, message, backend, auth)
}

func (h *Hub) processHelloInternal(client ClientWithSession, message *api.ClientMessage) {
	defer h.startExpectHello(client)
	if len(h.internalClientsSecret) == 0 {
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
		return
	}

	ctx := log.NewLoggerContext(client.Context(), h.logger)
	throttle, err := h.throttler.CheckBruteforce(ctx, client.RemoteAddr(), "HelloInternal")
	if err == async.ErrBruteforceDetected {
		client.SendMessage(message.NewErrorServerMessage(TooManyRequests))
		return
	} else if err != nil {
		h.logger.Printf("Error checking for bruteforce: %s", err)
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	// Validate internal connection.
	rnd := message.Hello.Auth.InternalParams.Random
	mac := hmac.New(sha256.New, h.internalClientsSecret)
	mac.Write([]byte(rnd)) // nolint
	check := hex.EncodeToString(mac.Sum(nil))
	if len(rnd) < minTokenRandomLength || check != message.Hello.Auth.InternalParams.Token {
		throttle(ctx)
		client.SendMessage(message.NewErrorServerMessage(InvalidToken))
		return
	}

	backend := h.backend.GetBackend(message.Hello.Auth.InternalParams.ParsedBackend)
	if backend == nil {
		throttle(ctx)
		client.SendMessage(message.NewErrorServerMessage(InvalidBackendUrl))
		return
	}

	auth := &talk.BackendClientResponse{
		Type: "auth",
		Auth: &talk.BackendClientAuthResponse{},
	}
	h.processRegister(client, message, backend, auth)
}

func (h *Hub) disconnectByRoomSessionId(ctx context.Context, roomSessionId api.RoomSessionId, backend *talk.Backend) {
	sessionId, err := h.roomSessions.LookupSessionId(ctx, roomSessionId, "room_session_reconnected")
	if err == ErrNoSuchRoomSession {
		return
	} else if err != nil {
		h.logger.Printf("Could not get session id for room session %s: %s", roomSessionId, err)
		return
	}

	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		// Session is located on a different server. Should already have been closed
		// but send "bye" again as additional safeguard.
		msg := &events.AsyncMessage{
			Type: "message",
			Message: &api.ServerMessage{
				Type: "bye",
				Bye: &api.ByeServerMessage{
					Reason: "room_session_reconnected",
				},
			},
		}
		if err := h.events.PublishSessionMessage(sessionId, backend, msg); err != nil {
			h.logger.Printf("Could not send reconnect bye to session %s: %s", sessionId, err)
		}
		return
	}

	h.logger.Printf("Closing session %s because same room session %s connected", session.PublicId(), roomSessionId)
	h.disconnectSessionWithReason(session, "room_session_reconnected")
}

func (h *Hub) DisconnectSessionByRoomSessionId(sessionId api.PublicSessionId, roomSessionId api.RoomSessionId, reason string) {
	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		return
	}

	h.logger.Printf("Closing session %s because same room session %s connected", session.PublicId(), roomSessionId)
	h.disconnectSessionWithReason(session, reason)
}

func (h *Hub) disconnectSessionWithReason(session Session, reason string) {
	session.LeaveRoom(false)
	switch sess := session.(type) {
	case *ClientSession:
		if client := sess.GetClient(); client != nil {
			client.SendByeResponseWithReason(nil, reason)
		}
	}
	session.Close()
}

func (h *Hub) sendRoom(session *ClientSession, message *api.ClientMessage, room *Room) bool {
	response := &api.ServerMessage{
		Type: "room",
	}
	if message != nil {
		response.Id = message.Id
	}
	if room == nil {
		response.Room = &api.RoomServerMessage{
			RoomId: "",
		}
	} else {
		response.Room = &api.RoomServerMessage{
			RoomId:     room.id,
			Properties: room.Properties(),
		}
		var mcuStreamBitrate api.Bandwidth
		var mcuScreenBitrate api.Bandwidth
		if mcu := h.mcu; mcu != nil {
			mcuStreamBitrate, mcuScreenBitrate = mcu.GetBandwidthLimits()
		}

		var backendStreamBitrate api.Bandwidth
		var backendScreenBitrate api.Bandwidth
		if backend := room.Backend(); backend != nil {
			backendStreamBitrate = backend.MaxStreamBitrate()
			backendScreenBitrate = backend.MaxScreenBitrate()
		}

		var maxStreamBitrate api.Bandwidth
		if mcuStreamBitrate != 0 && backendStreamBitrate != 0 {
			maxStreamBitrate = min(mcuStreamBitrate, backendStreamBitrate)
		} else if mcuStreamBitrate != 0 {
			maxStreamBitrate = mcuStreamBitrate
		} else {
			maxStreamBitrate = backendStreamBitrate
		}

		var maxScreenBitrate api.Bandwidth
		if mcuScreenBitrate != 0 && backendScreenBitrate != 0 {
			maxScreenBitrate = min(mcuScreenBitrate, backendScreenBitrate)
		} else if mcuScreenBitrate != 0 {
			maxScreenBitrate = mcuScreenBitrate
		} else {
			maxScreenBitrate = backendScreenBitrate
		}

		if maxStreamBitrate != 0 || maxScreenBitrate != 0 {
			response.Room.Bandwidth = &api.RoomBandwidth{
				MaxStreamBitrate: maxStreamBitrate,
				MaxScreenBitrate: maxScreenBitrate,
			}
		}
	}
	return session.SendMessage(response)
}

func (h *Hub) processRoom(sess Session, message *api.ClientMessage) {
	session, ok := sess.(*ClientSession)
	if !ok {
		return
	}

	roomId := message.Room.RoomId
	if roomId == "" {
		// We can handle leaving a room directly.
		if session.LeaveRoomWithMessage(true, message) != nil {
			if session.UserId() == "" && session.ClientType() != api.HelloClientTypeInternal {
				h.startWaitAnonymousSessionRoom(session)
			}
			// User was in a room before, so need to notify about leaving it.
			h.sendRoom(session, message, nil)
		}

		return
	}

	if federation := message.Room.Federation; federation != nil {
		h.mu.Lock()
		// The session will join a room, make sure it doesn't expire while connecting.
		delete(h.anonymousSessions, session)
		h.mu.Unlock()

		ctx, cancel := context.WithTimeout(session.Context(), h.federationTimeout)
		defer cancel()

		client := session.GetFederationClient()
		var err error
		if client != nil {
			if client.CanReuse(federation) {
				err = client.ChangeRoom(message)
				if errors.Is(err, ErrNotConnected) {
					client = nil
				}
			} else {
				client = nil
			}
		}
		if client == nil {
			client, err = NewFederationClient(ctx, h, session, message)
		}

		if err != nil {
			if session.UserId() == "" && client == nil {
				h.startWaitAnonymousSessionRoom(session)
			}
			if ae, ok := internal.AsErrorType[*api.Error](err); ok {
				session.SendMessage(message.NewErrorServerMessage(ae))
				return
			}

			var details any
			if ce, ok := internal.AsErrorType[*tls.CertificateVerificationError](err); ok {
				details = map[string]string{
					"code":    "certificate_verification_error",
					"message": ce.Error(),
				}
			} else if ne, ok := internal.AsErrorType[net.Error](err); ok {
				details = map[string]string{
					"code":    "network_error",
					"message": ne.Error(),
				}
			} else if errors.Is(err, websocket.ErrBadHandshake) {
				details = map[string]string{
					"code":    "network_error",
					"message": err.Error(),
				}
			} else if we, ok := internal.AsErrorType[websocket.HandshakeError](err); ok {
				details = map[string]string{
					"code":    "network_error",
					"message": we.Error(),
				}
			}

			h.logger.Printf("Error creating federation client to %s for %s to join room %s: %s", federation.SignalingUrl, session.PublicId(), roomId, err)
			session.SendMessage(message.NewErrorServerMessage(
				api.NewErrorDetail("federation_error", "Failed to create federation client.", details),
			))
			return
		}

		session.SetFederationClient(client)

		roomSessionId := message.Room.SessionId
		if roomSessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			h.logger.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			roomSessionId = api.RoomSessionId(session.PublicId())
		}

		// Prefix room session id to allow using the same signaling server for two Nextcloud instances during development.
		// Otherwise the same room session id will be detected and the other session will be kicked.
		if err := session.UpdateRoomSessionId(api.FederatedRoomSessionIdPrefix + roomSessionId); err != nil {
			h.logger.Printf("Error updating room session id for session %s: %s", session.PublicId(), err)
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		h.federatedSessions[session] = true
		h.federationClients[client] = true
		return
	}

	if room := h.GetRoomForBackend(roomId, session.Backend()); room != nil && room.HasSession(session) {
		// Session already is in that room, no action needed.
		roomSessionId := message.Room.SessionId
		if roomSessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			h.logger.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			roomSessionId = api.RoomSessionId(session.PublicId())
		}

		if err := session.UpdateRoomSessionId(roomSessionId); err != nil {
			h.logger.Printf("Error updating room session id for session %s: %s", session.PublicId(), err)
		}
		session.SendMessage(message.NewErrorServerMessage(
			api.NewErrorDetail("already_joined", "Already joined this room.", &api.RoomErrorDetails{
				Room: &api.RoomServerMessage{
					RoomId:     room.id,
					Properties: room.Properties(),
				},
			}),
		))
		return
	}

	var room talk.BackendClientResponse
	var joinRoomTime time.Time
	if session.ClientType() == api.HelloClientTypeInternal {
		// Internal clients can join any room.
		joinRoomTime = time.Now()
		room = talk.BackendClientResponse{
			Type: "room",
			Room: &talk.BackendClientRoomResponse{
				RoomId: roomId,
			},
		}
	} else {
		// Run in timeout context to prevent blocking too long.
		ctx, cancel := context.WithTimeout(session.Context(), h.backendTimeout)
		defer cancel()

		sessionId := message.Room.SessionId
		if sessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			h.logger.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			sessionId = api.RoomSessionId(session.PublicId())
		}
		request := talk.NewBackendClientRoomRequest(roomId, session.UserId(), sessionId)
		request.Room.UpdateFromSession(session)
		if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendOcsUrl(), request, &room); err != nil {
			session.SendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		// TODO(jojo): Validate response

		joinRoomTime = time.Now()
		if message.Room.SessionId != "" {
			// There can only be one connection per Nextcloud Talk session,
			// disconnect any other connections without sending a "leave" event.
			ctx, cancel := context.WithTimeout(session.Context(), time.Second)
			defer cancel()

			h.disconnectByRoomSessionId(ctx, message.Room.SessionId, session.Backend())
		}
	}

	h.processJoinRoom(session, message, &room, joinRoomTime)
}

func (h *Hub) publishFederatedSessions() (int, *sync.WaitGroup) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var wg sync.WaitGroup
	if len(h.federatedSessions) == 0 {
		return 0, &wg
	}

	rooms := make(map[string]map[string][]talk.BackendPingEntry)
	urls := make(map[string]*url.URL)
	for session := range h.federatedSessions {
		u := session.BackendUrl()
		if u == "" {
			continue
		}

		federation := session.GetFederationClient()
		if federation == nil {
			continue
		}

		var sid api.RoomSessionId
		var uid string
		// Use Nextcloud session id and user id
		if sid = session.RoomSessionId().WithoutFederation(); sid == "" {
			continue
		}
		uid = session.AuthUserId()

		roomId := federation.RoomId()
		entries, found := rooms[roomId]
		if !found {
			entries = make(map[string][]talk.BackendPingEntry)
			rooms[roomId] = entries
		}

		e, found := entries[u]
		if !found {
			p := session.ParsedBackendOcsUrl()
			if p == nil {
				// Should not happen, invalid URLs should get rejected earlier.
				continue
			}
			urls[u] = p
		}

		entries[u] = append(e, talk.BackendPingEntry{
			SessionId: sid,
			UserId:    uid,
		})
	}

	if len(urls) == 0 {
		return 0, &wg
	}
	count := 0
	ctx := log.NewLoggerContext(context.Background(), h.logger)
	for roomId, entries := range rooms {
		for u, e := range entries {
			wg.Add(1)
			count += len(e)
			go func(roomId string, url *url.URL, entries []talk.BackendPingEntry) {
				defer wg.Done()
				sendCtx, cancel := context.WithTimeout(ctx, h.backendTimeout)
				defer cancel()

				if err := h.roomPing.SendPings(sendCtx, roomId, url, entries); err != nil {
					h.logger.Printf("Error pinging room %s for active entries %+v: %s", roomId, entries, err)
				}
			}(roomId, urls[u], e)
		}
	}
	return count, &wg
}

func (h *Hub) GetRoomForBackend(id string, backend *talk.Backend) *Room {
	internalRoomId := getRoomIdForBackend(id, backend)

	h.ru.RLock()
	defer h.ru.RUnlock()
	return h.rooms[internalRoomId]
}

func (h *Hub) GetInternalSessions(roomId string, backend *talk.Backend) ([]*grpc.InternalSessionData, []*grpc.VirtualSessionData, bool) {
	room := h.GetRoomForBackend(roomId, backend)
	if room == nil {
		return nil, nil, false
	}

	room.mu.RLock()
	defer room.mu.RUnlock()

	var internalSessions []*grpc.InternalSessionData
	var virtualSessions []*grpc.VirtualSessionData
	for session := range room.internalSessions {
		internalSessions = append(internalSessions, &grpc.InternalSessionData{
			SessionId: string(session.PublicId()),
			InCall:    uint32(session.GetInCall()),
			Features:  session.GetFeatures(),
		})
	}

	for session := range room.virtualSessions {
		virtualSessions = append(virtualSessions, &grpc.VirtualSessionData{
			SessionId: string(session.PublicId()),
			InCall:    uint32(session.GetInCall()),
		})
	}

	return internalSessions, virtualSessions, true
}

func (h *Hub) GetTransientEntries(roomId string, backend *talk.Backend) (api.TransientDataEntries, bool) {
	room := h.GetRoomForBackend(roomId, backend)
	if room == nil {
		return nil, false
	}

	entries := room.transientData.GetEntries()
	return entries, true
}

func (h *Hub) removeRoom(room *Room) {
	internalRoomId := getRoomIdForBackend(room.Id(), room.Backend())
	h.ru.Lock()
	if _, found := h.rooms[internalRoomId]; found {
		delete(h.rooms, internalRoomId)
		statsHubRoomsCurrent.WithLabelValues(room.Backend().Id()).Dec()
	}
	h.ru.Unlock()
	h.roomPing.DeleteRoom(room.Id())
}

func (h *Hub) CreateRoom(id string, properties json.RawMessage, backend *talk.Backend) (*Room, error) {
	h.ru.Lock()
	defer h.ru.Unlock()

	return h.createRoomLocked(id, properties, backend)
}

// +checklocks:h.ru
func (h *Hub) createRoomLocked(id string, properties json.RawMessage, backend *talk.Backend) (*Room, error) {
	// Note the write lock must be held.
	room, err := NewRoom(id, properties, h, h.events, backend)
	if err != nil {
		return nil, err
	}

	internalRoomId := getRoomIdForBackend(id, backend)
	h.rooms[internalRoomId] = room
	statsHubRoomsCurrent.WithLabelValues(backend.Id()).Inc()
	return room, nil
}

func (h *Hub) processJoinRoom(session *ClientSession, message *api.ClientMessage, room *talk.BackendClientResponse, joinTime time.Time) {
	if room.Type == "error" {
		session.SendMessage(message.NewErrorServerMessage(room.Error))
		return
	} else if room.Type != "room" {
		session.SendMessage(message.NewErrorServerMessage(RoomJoinFailed))
		return
	}

	session.LeaveRoom(true)
	h.mu.Lock()
	delete(h.federatedSessions, session)
	h.mu.Unlock()

	roomId := room.Room.RoomId
	internalRoomId := getRoomIdForBackend(roomId, session.Backend())
	if err := session.SubscribeRoomEvents(roomId, message.Room.SessionId); err != nil {
		session.SendMessage(message.NewWrappedErrorServerMessage(err))
		// The session (implicitly) left the room due to an error.
		h.sendRoom(session, nil, nil)
		return
	}

	h.ru.Lock()
	r, found := h.rooms[internalRoomId]
	if !found {
		var err error
		if r, err = h.createRoomLocked(roomId, room.Room.Properties, session.Backend()); err != nil {
			h.ru.Unlock()
			session.SendMessage(message.NewWrappedErrorServerMessage(err))
			// The session (implicitly) left the room due to an error.
			session.UnsubscribeRoomEvents()
			h.sendRoom(session, nil, nil)
			return
		}
	}
	h.ru.Unlock()

	h.mu.Lock()
	// The session now joined a room, don't expire if it is anonymous.
	delete(h.anonymousSessions, session)
	if session.ClientType() == api.HelloClientTypeInternal && session.HasFeature(api.ClientFeatureStartDialout) {
		// An internal session in a room can not be used for dialout.
		delete(h.dialoutSessions, session)
	}
	h.mu.Unlock()
	session.SetRoom(r, joinTime)
	if room.Room.Permissions != nil {
		session.SetPermissions(*room.Room.Permissions)
	}
	h.sendRoom(session, message, r)
	r.AddSession(session, room.Room.Session)
}

func (h *Hub) processMessageMsg(sess Session, message *api.ClientMessage) {
	session, ok := sess.(*ClientSession)
	if !ok {
		// Client is not connected yet.
		return
	}

	msg := message.Message
	var recipient *ClientSession
	var subject string
	var clientData *api.MessageClientMessageData
	var serverRecipient *api.MessageClientMessageRecipient
	var recipientSessionId api.PublicSessionId
	var room *Room
	switch msg.Recipient.Type {
	case api.RecipientTypeSession:
		if h.mcu != nil {
			// Maybe this is a message to be processed by the MCU.
			var data api.MessageClientMessageData
			if err := data.UnmarshalJSON(msg.Data); err == nil {
				if err := data.CheckValid(); err != nil {
					h.logger.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
					if err, ok := err.(*api.Error); ok {
						session.SendMessage(message.NewErrorServerMessage(err))
					} else {
						session.SendMessage(message.NewErrorServerMessage(InvalidFormat))
					}
					return
				}

				clientData = &data

				switch clientData.Type {
				case "requestoffer":
					// Process asynchronously to avoid blocking regular
					// message processing for this client.
					go h.processMcuMessage(session, message, msg, clientData)
					return
				case "offer":
					fallthrough
				case "answer":
					fallthrough
				case "endOfCandidates":
					fallthrough
				case "selectStream":
					fallthrough
				case "candidate":
					h.processMcuMessage(session, message, msg, clientData)
					return
				case "unshareScreen":
					if msg.Recipient.SessionId == session.PublicId() {
						// User is stopping to share his screen. Firefox doesn't properly clean
						// up the peer connections in all cases, so make sure to stop publishing
						// in the MCU.
						go func(session *ClientSession) {
							sleepCtx, cancel := context.WithTimeout(session.Context(), cleanupScreenPublisherDelay)
							defer cancel()

							<-sleepCtx.Done()
							if session.Context().Err() != nil {
								// Session was closed while waiting.
								return
							}

							publisher := session.GetPublisher(sfu.StreamTypeScreen)
							if publisher == nil {
								return
							}

							h.logger.Printf("Closing screen publisher for %s", session.PublicId())
							ctx, cancel := context.WithTimeout(context.Background(), h.mcuTimeout)
							defer cancel()
							publisher.Close(ctx)
						}(session)
					}
				}
			}
		}

		sess := h.GetSessionByPublicId(msg.Recipient.SessionId)
		if sess != nil {
			// Recipient is also connected to this instance.
			if sess.Backend().Id() != session.Backend().Id() {
				// Clients are only allowed to send to sessions from the same backend.
				return
			}

			if msg.Recipient.SessionId == session.PublicId() {
				// Don't loop messages to the sender.
				return
			}

			subject = events.GetSubjectForSessionId(msg.Recipient.SessionId, sess.Backend())
			recipientSessionId = msg.Recipient.SessionId
			if sess, ok := sess.(*ClientSession); ok {
				recipient = sess
			}

			// Send to client connection for virtual sessions.
			if sess.ClientType() == api.HelloClientTypeVirtual {
				virtualSession := sess.(*VirtualSession)
				clientSession := virtualSession.Session()
				subject = events.GetSubjectForSessionId(clientSession.PublicId(), sess.Backend())
				recipientSessionId = clientSession.PublicId()
				recipient = clientSession
				// The client should see his session id as recipient.
				serverRecipient = &api.MessageClientMessageRecipient{
					Type:      "session",
					SessionId: virtualSession.SessionId(),
				}
			}
		} else {
			subject = events.GetSubjectForSessionId(msg.Recipient.SessionId, nil)
			recipientSessionId = msg.Recipient.SessionId
			serverRecipient = &msg.Recipient
		}
	case api.RecipientTypeUser:
		if msg.Recipient.UserId != "" {
			if msg.Recipient.UserId == session.UserId() {
				// Don't loop messages to the sender.
				// TODO(jojo): Should we allow users to send messages to their
				// other sessions?
				return
			}

			subject = events.GetSubjectForUserId(msg.Recipient.UserId, session.Backend())
		}
	case api.RecipientTypeRoom:
		fallthrough
	case api.RecipientTypeCall:
		if session != nil {
			if room = session.GetRoom(); room != nil {
				subject = events.GetSubjectForRoomId(room.Id(), room.Backend())

				if h.mcu != nil {
					var data api.MessageClientMessageData
					if err := data.UnmarshalJSON(msg.Data); err == nil {
						if err := data.CheckValid(); err != nil {
							h.logger.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
							if err, ok := err.(*api.Error); ok {
								session.SendMessage(message.NewErrorServerMessage(err))
							} else {
								session.SendMessage(message.NewErrorServerMessage(InvalidFormat))
							}
							return
						}

						clientData = &data
					}
				}
			}
		}
	}
	if subject == "" {
		h.logger.Printf("Unknown recipient in message %+v from %s", msg, session.PublicId())
		return
	}

	response := &api.ServerMessage{
		Type: "message",
		Message: &api.MessageServerMessage{
			Sender: &api.MessageServerMessageSender{
				Type:      msg.Recipient.Type,
				SessionId: session.PublicId(),
				UserId:    session.UserId(),
			},
			Recipient: serverRecipient,
			Data:      msg.Data,
		},
	}
	if recipient != nil {
		// The recipient is connected to this instance, no need to go through asynchronous events.
		if clientData != nil && clientData.Type == "sendoffer" {
			if err := session.IsAllowedToSend(clientData); err != nil {
				h.logger.Printf("Session %s is not allowed to send offer for %s, ignoring (%s)", session.PublicId(), clientData.RoomType, err)
				sendNotAllowed(session, message, "Not allowed to send offer")
				return
			}

			// It may take some time for the publisher (which is the current
			// client) to start his stream, so we must not block the active
			// goroutine.
			go func() {
				ctx, cancel := context.WithTimeout(session.Context(), h.mcuTimeout)
				defer cancel()

				mc, err := recipient.GetOrCreateSubscriber(ctx, h.mcu, session.PublicId(), sfu.StreamType(clientData.RoomType))
				if err != nil {
					h.logger.Printf("Could not create MCU subscriber for session %s to send %+v to %s: %s", session.PublicId(), clientData, recipient.PublicId(), err)
					sendMcuClientNotFound(session, message)
					return
				} else if mc == nil {
					h.logger.Printf("No MCU subscriber found for session %s to send %+v to %s", session.PublicId(), clientData, recipient.PublicId())
					sendMcuClientNotFound(session, message)
					return
				}

				mc.SendMessage(session.Context(), msg, clientData, func(err error, response api.StringMap) {
					if err != nil {
						h.logger.Printf("Could not send MCU message %+v for session %s to %s: %s", clientData, session.PublicId(), recipient.PublicId(), err)
						sendMcuProcessingFailed(session, message)
						return
					} else if response == nil {
						// No response received
						return
					}

					// The response (i.e. the "offer") must be sent to the recipient but
					// should be coming from the sender.
					msg.Recipient.SessionId = session.PublicId()
					h.sendMcuMessageResponse(recipient, mc, msg, clientData, response)
				})
			}()
			return
		}

		recipient.SendMessage(response)
	} else {
		if clientData != nil && clientData.Type == "sendoffer" {
			if err := session.IsAllowedToSend(clientData); err != nil {
				h.logger.Printf("Session %s is not allowed to send offer for %s, ignoring (%s)", session.PublicId(), clientData.RoomType, err)
				sendNotAllowed(session, message, "Not allowed to send offer")
				return
			}

			async := &events.AsyncMessage{
				Type: "sendoffer",
				SendOffer: &events.SendOfferMessage{
					MessageId: message.Id,
					SessionId: session.PublicId(),
					Data:      clientData,
				},
			}
			if err := h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async); err != nil {
				h.logger.Printf("Error publishing message to remote session: %s", err)
			}
			return
		}

		async := &events.AsyncMessage{
			Type:    "message",
			Message: response,
		}
		var err error
		switch msg.Recipient.Type {
		case api.RecipientTypeSession:
			err = h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async)
		case api.RecipientTypeUser:
			err = h.events.PublishUserMessage(msg.Recipient.UserId, session.Backend(), async)
		case api.RecipientTypeRoom:
			fallthrough
		case api.RecipientTypeCall:
			err = h.events.PublishRoomMessage(room.Id(), session.Backend(), async)
		default:
			err = fmt.Errorf("unsupported recipient type: %s", msg.Recipient.Type)
		}

		if err != nil {
			h.logger.Printf("Error publishing message to remote session: %s", err)
		}
	}
}

func isAllowedToControl(session Session) bool {
	if session.ClientType() == api.HelloClientTypeInternal {
		// Internal clients are allowed to send any control message.
		return true
	}

	if session.HasPermission(api.PERMISSION_MAY_CONTROL) {
		// Moderator clients are allowed to send any control message.
		return true
	}

	return false
}

func (h *Hub) processControlMsg(session Session, message *api.ClientMessage) {
	msg := message.Control
	if !isAllowedToControl(session) {
		h.logger.Printf("Ignore control message %+v from %s", msg, session.PublicId())
		return
	}

	var recipient *ClientSession
	var subject string
	var serverRecipient *api.MessageClientMessageRecipient
	var recipientSessionId api.PublicSessionId
	var room *Room
	switch msg.Recipient.Type {
	case api.RecipientTypeSession:
		data := h.decodePublicSessionId(msg.Recipient.SessionId)
		if data != nil {
			if msg.Recipient.SessionId == session.PublicId() {
				// Don't loop messages to the sender.
				return
			}

			subject = events.GetSubjectForSessionId(msg.Recipient.SessionId, nil)
			recipientSessionId = msg.Recipient.SessionId
			h.mu.RLock()
			sess, found := h.sessions[data.Sid]
			if found && sess.PublicId() == msg.Recipient.SessionId {
				if sess, ok := sess.(*ClientSession); ok {
					recipient = sess
				}

				// Send to client connection for virtual sessions.
				if sess.ClientType() == api.HelloClientTypeVirtual {
					virtualSession := sess.(*VirtualSession)
					clientSession := virtualSession.Session()
					subject = events.GetSubjectForSessionId(clientSession.PublicId(), sess.Backend())
					recipientSessionId = clientSession.PublicId()
					recipient = clientSession
					// The client should see his session id as recipient.
					serverRecipient = &api.MessageClientMessageRecipient{
						Type:      "session",
						SessionId: virtualSession.SessionId(),
					}
				}
			} else {
				serverRecipient = &msg.Recipient
			}
			h.mu.RUnlock()
		} else {
			serverRecipient = &msg.Recipient
		}
	case api.RecipientTypeUser:
		if msg.Recipient.UserId != "" {
			if msg.Recipient.UserId == session.UserId() {
				// Don't loop messages to the sender.
				// TODO(jojo): Should we allow users to send messages to their
				// other sessions?
				return
			}

			subject = events.GetSubjectForUserId(msg.Recipient.UserId, session.Backend())
		}
	case api.RecipientTypeRoom:
		fallthrough
	case api.RecipientTypeCall:
		if session != nil {
			if room = session.GetRoom(); room != nil {
				subject = events.GetSubjectForRoomId(room.Id(), room.Backend())
			}
		}
	}
	if subject == "" {
		h.logger.Printf("Unknown recipient in message %+v from %s", msg, session.PublicId())
		return
	}

	response := &api.ServerMessage{
		Type: "control",
		Control: &api.ControlServerMessage{
			Sender: &api.MessageServerMessageSender{
				Type:      msg.Recipient.Type,
				SessionId: session.PublicId(),
				UserId:    session.UserId(),
			},
			Recipient: serverRecipient,
			Data:      msg.Data,
		},
	}
	if recipient != nil {
		recipient.SendMessage(response)
	} else {
		async := &events.AsyncMessage{
			Type:    "message",
			Message: response,
		}
		var err error
		switch msg.Recipient.Type {
		case api.RecipientTypeSession:
			err = h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async)
		case api.RecipientTypeUser:
			err = h.events.PublishUserMessage(msg.Recipient.UserId, session.Backend(), async)
		case api.RecipientTypeRoom:
			fallthrough
		case api.RecipientTypeCall:
			err = h.events.PublishRoomMessage(room.Id(), room.Backend(), async)
		default:
			err = fmt.Errorf("unsupported recipient type: %s", msg.Recipient.Type)
		}
		if err != nil {
			h.logger.Printf("Error publishing message to remote session: %s", err)
		}
	}
}

func (h *Hub) processInternalMsg(sess Session, message *api.ClientMessage) {
	msg := message.Internal
	session, ok := sess.(*ClientSession)
	if !ok {
		// Client is not connected yet.
		return
	} else if session.ClientType() != api.HelloClientTypeInternal {
		h.logger.Printf("Ignore internal message %+v from %s", msg, session.PublicId())
		return
	}

	if session.ProcessResponse(message) {
		return
	}

	switch msg.Type {
	case "addsession":
		msg := msg.AddSession
		room := h.GetRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			h.logger.Printf("Ignore add session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
			return
		}

		sessionIdData := h.newSessionIdData(session.Backend())
		privateSessionId, err := h.sessionIds.EncodePrivate(sessionIdData)
		if err != nil {
			h.logger.Printf("Could not encode private virtual session id: %s", err)
			return
		}
		publicSessionId, err := h.sessionIds.EncodePublic(sessionIdData)
		if err != nil {
			h.logger.Printf("Could not encode public virtual session id: %s", err)
			return
		}

		ctx, cancel := context.WithTimeout(session.Context(), h.backendTimeout)
		defer cancel()

		virtualSessionId := GetVirtualSessionId(session, msg.SessionId)

		sess, err := NewVirtualSession(session, privateSessionId, publicSessionId, sessionIdData, msg)
		if err != nil {
			h.logger.Printf("Could not create virtual session %s: %s", virtualSessionId, err)
			reply := message.NewErrorServerMessage(api.NewError("add_failed", "Could not create virtual session."))
			session.SendMessage(reply)
			return
		}

		if options := msg.Options; options != nil && options.ActorId != "" && options.ActorType != "" {
			request := talk.NewBackendClientRoomRequest(room.Id(), msg.UserId, api.RoomSessionId(publicSessionId))
			request.Room.ActorId = options.ActorId
			request.Room.ActorType = options.ActorType
			request.Room.InCall = sess.GetInCall()

			var response talk.BackendClientResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendOcsUrl(), request, &response); err != nil {
				sess.Close()
				h.logger.Printf("Could not join virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(api.NewError("add_failed", "Could not join virtual session."))
				session.SendMessage(reply)
				return
			}

			if response.Type == "error" {
				sess.Close()
				h.logger.Printf("Could not join virtual session %s at backend %s: %+v", virtualSessionId, session.BackendUrl(), response.Error)
				reply := message.NewErrorServerMessage(api.NewError("add_failed", response.Error.Error()))
				session.SendMessage(reply)
				return
			}
		} else {
			request := talk.NewBackendClientSessionRequest(room.Id(), "add", publicSessionId, msg)
			var response talk.BackendClientSessionResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendOcsUrl(), request, &response); err != nil {
				sess.Close()
				h.logger.Printf("Could not add virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(api.NewError("add_failed", "Could not add virtual session."))
				session.SendMessage(reply)
				return
			}
		}

		h.mu.Lock()
		h.sessions[sessionIdData.Sid] = sess
		h.virtualSessions[virtualSessionId] = sessionIdData.Sid
		h.mu.Unlock()
		statsHubSessionsCurrent.WithLabelValues(session.Backend().Id(), string(sess.ClientType())).Inc()
		statsHubSessionsTotal.WithLabelValues(session.Backend().Id(), string(sess.ClientType())).Inc()
		h.logger.Printf("Session %s added virtual session %s with initial flags %d", session.PublicId(), sess.PublicId(), sess.Flags())
		session.AddVirtualSession(sess)
		sess.SetRoom(room, time.Now())
		room.AddSession(sess, nil)
	case "updatesession":
		msg := msg.UpdateSession
		room := h.GetRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			h.logger.Printf("Ignore remove session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
			return
		}

		virtualSessionId := GetVirtualSessionId(session, msg.SessionId)
		h.mu.Lock()
		sid, found := h.virtualSessions[virtualSessionId]
		if !found {
			h.mu.Unlock()
			return
		}

		sess := h.sessions[sid]
		h.mu.Unlock()
		if sess != nil {
			var changed SessionChangeFlag
			if virtualSession, ok := sess.(*VirtualSession); ok {
				if msg.Flags != nil {
					if virtualSession.SetFlags(*msg.Flags) {
						changed |= SessionChangeFlags
					}
				}
				if msg.InCall != nil {
					if virtualSession.SetInCall(*msg.InCall) {
						changed |= SessionChangeInCall
					}
				}
			} else {
				h.logger.Printf("Ignore update request for non-virtual session %s", sess.PublicId())
			}
			if changed != 0 {
				room.NotifySessionChanged(sess, changed)
			}
		}
	case "removesession":
		msg := msg.RemoveSession
		room := h.GetRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			h.logger.Printf("Ignore remove session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
			return
		}

		virtualSessionId := GetVirtualSessionId(session, msg.SessionId)
		h.mu.Lock()
		sid, found := h.virtualSessions[virtualSessionId]
		if !found {
			h.mu.Unlock()
			return
		}

		delete(h.virtualSessions, virtualSessionId)
		sess := h.sessions[sid]
		h.mu.Unlock()
		if sess != nil {
			h.logger.Printf("Session %s removed virtual session %s", session.PublicId(), sess.PublicId())
			if vsess, ok := sess.(*VirtualSession); ok {
				// We should always have a VirtualSession here.
				vsess.CloseWithFeedback(session, message)
			} else {
				sess.Close()
			}
		}
	case "incall":
		msg := msg.InCall
		if session.SetInCall(msg.InCall) {
			if room := session.GetRoom(); room != nil {
				room.NotifySessionChanged(session, SessionChangeInCall)
			}
		}
	case "dialout":
		roomId := msg.Dialout.RoomId
		msg.Dialout.RoomId = "" // Don't send room id to recipients.
		if msg.Dialout.Type == "status" {
			asyncMessage := &events.AsyncMessage{
				Type: "room",
				Room: &talk.BackendServerRoomRequest{
					Type: "transient",
					Transient: &talk.BackendRoomTransientRequest{
						Action: talk.TransientActionSet,
						Key:    "callstatus_" + msg.Dialout.Status.CallId,
						Value:  msg.Dialout.Status,
					},
				},
			}
			if msg.Dialout.Status.Status == api.DialoutStatusCleared || msg.Dialout.Status.Status == api.DialoutStatusRejected {
				asyncMessage.Room.Transient.TTL = removeCallStatusTTL
			}
			if err := h.events.PublishBackendRoomMessage(roomId, session.Backend(), asyncMessage); err != nil {
				h.logger.Printf("Error publishing dialout message %+v to room %s", msg.Dialout, roomId)
			}
		} else {
			if err := h.events.PublishRoomMessage(roomId, session.Backend(), &events.AsyncMessage{
				Type: "message",
				Message: &api.ServerMessage{
					Type:    "dialout",
					Dialout: msg.Dialout,
				},
			}); err != nil {
				h.logger.Printf("Error publishing dialout message %+v to room %s", msg.Dialout, roomId)
			}
		}
	default:
		h.logger.Printf("Ignore unsupported internal message %+v from %s", msg, session.PublicId())
		return
	}
}

func isAllowedToUpdateTransientData(session Session) bool {
	if session.ClientType() == api.HelloClientTypeInternal {
		// Internal clients are always allowed.
		return true
	}

	if session.HasPermission(api.PERMISSION_TRANSIENT_DATA) {
		return true
	}

	return false
}

func isAllowedToUpdateTransientDataKey(session Session, key string) bool {
	if session.ClientType() == api.HelloClientTypeInternal {
		// Internal clients may update all transient keys.
		return true
	}

	if sid, found := strings.CutPrefix(key, api.TransientSessionDataPrefix); found {
		// Session data may only be modified by the session itself.
		return sid == string(session.PublicId())
	}

	return true
}

func (h *Hub) processTransientMsg(session Session, message *api.ClientMessage) {
	room := session.GetRoom()
	if room == nil {
		response := message.NewErrorServerMessage(api.NewError("not_in_room", "No room joined yet."))
		session.SendMessage(response)
		return
	}

	msg := message.TransientData
	switch msg.Type {
	case "set":
		if !isAllowedToUpdateTransientData(session) {
			sendNotAllowed(session, message, "Not allowed to update transient data.")
			return
		} else if !isAllowedToUpdateTransientDataKey(session, msg.Key) {
			sendNotAllowed(session, message, "Not allowed to update this transient data entry.")
			return
		}

		if err := room.SetTransientDataTTL(msg.Key, msg.Value, msg.TTL); err != nil {
			response := message.NewWrappedErrorServerMessage(err)
			session.SendMessage(response)
			return
		}
	case "remove":
		if !isAllowedToUpdateTransientData(session) {
			sendNotAllowed(session, message, "Not allowed to update transient data.")
			return
		} else if !isAllowedToUpdateTransientDataKey(session, msg.Key) {
			sendNotAllowed(session, message, "Not allowed to update this transient data entry.")
			return
		}

		if err := room.RemoveTransientData(msg.Key); err != nil {
			response := message.NewWrappedErrorServerMessage(err)
			session.SendMessage(response)
			return
		}
	default:
		response := message.NewErrorServerMessage(api.NewError("ignored", "Unsupported message type."))
		session.SendMessage(response)
	}
}

func sendNotAllowed(session Session, message *api.ClientMessage, reason string) {
	response := message.NewErrorServerMessage(api.NewError("not_allowed", reason))
	session.SendMessage(response)
}

func sendMcuClientNotFound(session Session, message *api.ClientMessage) {
	response := message.NewErrorServerMessage(api.NewError("client_not_found", "No MCU client found to send message to."))
	session.SendMessage(response)
}

func sendMcuProcessingFailed(session Session, message *api.ClientMessage) {
	response := message.NewErrorServerMessage(api.NewError("processing_failed", "Processing of the message failed, please check server logs."))
	session.SendMessage(response)
}

func (h *Hub) isInSameCallRemote(ctx context.Context, senderSession *ClientSession, senderRoom *Room, recipientSessionId api.PublicSessionId) bool {
	clients := h.rpcClients.GetClients()
	if len(clients) == 0 {
		return false
	}

	var result atomic.Bool
	var wg sync.WaitGroup
	rpcCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, client := range clients {
		wg.Go(func() {
			inCall, err := client.IsSessionInCall(rpcCtx, recipientSessionId, senderRoom.Id(), senderSession.BackendUrl())
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				h.logger.Printf("Error checking session %s in call on %s: %s", recipientSessionId, client.Target(), err)
				return
			} else if !inCall {
				return
			}

			cancel()
			result.Store(true)
		})
	}
	wg.Wait()

	return result.Load()
}

func (h *Hub) isInSameCall(ctx context.Context, senderSession *ClientSession, recipientSessionId api.PublicSessionId) bool {
	if senderSession.ClientType() == api.HelloClientTypeInternal {
		// Internal clients may subscribe all streams.
		return true
	}

	senderRoom := senderSession.GetRoom()
	if senderRoom == nil || !senderRoom.IsSessionInCall(senderSession) {
		// Sender is not in a room or not in the call.
		return false
	}

	recipientSession := h.GetSessionByPublicId(recipientSessionId)
	if recipientSession == nil {
		// Recipient session does not exist.
		return h.isInSameCallRemote(ctx, senderSession, senderRoom, recipientSessionId)
	}

	recipientRoom := recipientSession.GetRoom()
	if recipientRoom == nil || !senderRoom.IsEqual(recipientRoom) ||
		(recipientSession.ClientType() != api.HelloClientTypeInternal && !recipientRoom.IsSessionInCall(recipientSession)) {
		// Recipient is not in a room, a different room or not in the call.
		return false
	}

	return true
}

func (h *Hub) processMcuMessage(session *ClientSession, client_message *api.ClientMessage, message *api.MessageClientMessage, data *api.MessageClientMessageData) {
	ctx, cancel := context.WithTimeout(session.Context(), h.mcuTimeout)
	defer cancel()

	var mc sfu.Client
	var err error
	var clientType string
	switch data.Type {
	case "requestoffer":
		if session.PublicId() == message.Recipient.SessionId {
			h.logger.Printf("Not requesting offer from itself for session %s", session.PublicId())
			return
		}

		// A user is only allowed to subscribe a stream if she is in the same room
		// as the other user and both have their "inCall" flag set.
		if !h.allowSubscribeAnyStream && !h.isInSameCall(ctx, session, message.Recipient.SessionId) {
			h.logger.Printf("Session %s is not in the same call as session %s, not requesting offer", session.PublicId(), message.Recipient.SessionId)
			sendNotAllowed(session, client_message, "Not allowed to request offer.")
			return
		}

		clientType = "subscriber"
		mc, err = session.GetOrCreateSubscriber(ctx, h.mcu, message.Recipient.SessionId, sfu.StreamType(data.RoomType))
	case "sendoffer":
		// Will be sent directly.
		return
	case "offer":
		clientType = "publisher"
		mc, err = session.GetOrCreatePublisher(ctx, h.mcu, sfu.StreamType(data.RoomType), data)
		if err, ok := err.(*PermissionError); ok {
			h.logger.Printf("Session %s is not allowed to offer %s, ignoring (%s)", session.PublicId(), data.RoomType, err)
			sendNotAllowed(session, client_message, "Not allowed to publish.")
			return
		}
	case "selectStream":
		if session.PublicId() == message.Recipient.SessionId {
			h.logger.Printf("Not selecting substream for own %s stream in session %s", data.RoomType, session.PublicId())
			return
		}

		clientType = "subscriber"
		mc = session.GetSubscriber(message.Recipient.SessionId, sfu.StreamType(data.RoomType))
	default:
		if data.Type == "candidate" && api.FilterCandidate(data.Candidate, h.allowedCandidates.Load(), h.blockedCandidates.Load()) {
			// Silently ignore filtered candidates.
			return
		}

		if session.PublicId() == message.Recipient.SessionId {
			if err := session.IsAllowedToSend(data); err != nil {
				h.logger.Printf("Session %s is not allowed to send candidate for %s, ignoring (%s)", session.PublicId(), data.RoomType, err)
				sendNotAllowed(session, client_message, "Not allowed to send candidate.")
				return
			}

			clientType = "publisher"
			mc = session.GetPublisher(sfu.StreamType(data.RoomType))
		} else {
			clientType = "subscriber"
			mc = session.GetSubscriber(message.Recipient.SessionId, sfu.StreamType(data.RoomType))
		}
	}
	if err != nil {
		h.logger.Printf("Could not create MCU %s for session %s to send %+v to %s: %s", clientType, session.PublicId(), data, message.Recipient.SessionId, err)
		sendMcuClientNotFound(session, client_message)
		return
	} else if mc == nil {
		h.logger.Printf("No MCU %s found for session %s to send %+v to %s", clientType, session.PublicId(), data, message.Recipient.SessionId)
		sendMcuClientNotFound(session, client_message)
		return
	}

	mc.SendMessage(session.Context(), message, data, func(err error, response api.StringMap) {
		if err != nil {
			if !errors.Is(err, api.ErrCandidateFiltered) {
				h.logger.Printf("Could not send MCU message %+v for session %s to %s: %s", data, session.PublicId(), message.Recipient.SessionId, err)
				sendMcuProcessingFailed(session, client_message)
			}
			return
		} else if len(response) == 0 {
			// No response received
			return
		}

		h.sendMcuMessageResponse(session, mc, message, data, response)
	})
}

func (h *Hub) sendMcuMessageResponse(session *ClientSession, mcuClient sfu.Client, message *api.MessageClientMessage, data *api.MessageClientMessageData, response api.StringMap) {
	var response_message *api.ServerMessage
	switch response["type"] {
	case "answer":
		answer_message := &api.AnswerOfferMessage{
			To:       session.PublicId(),
			From:     session.PublicId(),
			Type:     "answer",
			RoomType: data.RoomType,
			Payload:  response,
			Sid:      mcuClient.Sid(),
		}
		answer_data, err := json.Marshal(answer_message)
		if err != nil {
			h.logger.Printf("Could not serialize answer %+v to %s: %s", answer_message, session.PublicId(), err)
			return
		}
		response_message = &api.ServerMessage{
			Type: "message",
			Message: &api.MessageServerMessage{
				Sender: &api.MessageServerMessageSender{
					Type:      "session",
					SessionId: session.PublicId(),
					UserId:    session.UserId(),
				},
				Data: answer_data,
			},
		}
	case "offer":
		offer_message := &api.AnswerOfferMessage{
			To:       session.PublicId(),
			From:     message.Recipient.SessionId,
			Type:     "offer",
			RoomType: data.RoomType,
			Payload:  response,
			Sid:      mcuClient.Sid(),
		}
		offer_data, err := json.Marshal(offer_message)
		if err != nil {
			h.logger.Printf("Could not serialize offer %+v to %s: %s", offer_message, session.PublicId(), err)
			return
		}
		response_message = &api.ServerMessage{
			Type: "message",
			Message: &api.MessageServerMessage{
				Sender: &api.MessageServerMessageSender{
					Type:      "session",
					SessionId: message.Recipient.SessionId,
					// TODO(jojo): Set "UserId" field if known user.
				},
				Data: offer_data,
			},
		}
	default:
		h.logger.Printf("Unsupported response %+v received to send to %s", response, session.PublicId())
		return
	}

	session.SendMessage(response_message)
}

func (h *Hub) processByeMsg(client ClientWithSession, message *api.ClientMessage) {
	client.SendByeResponse(message)
	if session := h.processUnregister(client); session != nil {
		session.Close()
	}
}

func (h *Hub) processRoomUpdated(message *talk.BackendServerRoomRequest) {
	room := h.GetRoomForBackend(message.RoomId, message.Backend)
	if room == nil {
		return
	}

	room.UpdateProperties(message.Update.Properties)
}

func (h *Hub) processRoomDeleted(message *talk.BackendServerRoomRequest) {
	room := h.GetRoomForBackend(message.RoomId, message.Backend)
	if room == nil {
		return
	}

	sessions := room.Close()
	for _, session := range sessions {
		// The session is no longer in the room
		session.LeaveRoom(true)
		switch sess := session.(type) {
		case *ClientSession:
			if client := sess.GetClient(); client != nil {
				h.sendRoom(sess, nil, nil)
			}
		}
	}
}

func (h *Hub) processRoomInCallChanged(message *talk.BackendServerRoomRequest) {
	room := h.GetRoomForBackend(message.RoomId, message.Backend)
	if room == nil {
		return
	}

	if message.InCall.All {
		var flags int
		if err := json.Unmarshal(message.InCall.InCall, &flags); err != nil {
			var incall bool
			if err := json.Unmarshal(message.InCall.InCall, &incall); err != nil {
				h.logger.Printf("Unsupported InCall flags type: %+v, ignoring", string(message.InCall.InCall))
				return
			}

			if incall {
				flags = FlagInCall
			}
		}

		room.PublishUsersInCallChangedAll(flags)
	} else {
		room.PublishUsersInCallChanged(message.InCall.Changed, message.InCall.Users)
	}
}

func (h *Hub) processRoomParticipants(message *talk.BackendServerRoomRequest) {
	room := h.GetRoomForBackend(message.RoomId, message.Backend)
	if room == nil {
		return
	}

	room.PublishUsersChanged(message.Participants.Changed, message.Participants.Users)
}

func (h *Hub) GetStats() api.StringMap {
	result := make(api.StringMap)
	h.ru.RLock()
	result["rooms"] = len(h.rooms)
	h.ru.RUnlock()
	h.mu.Lock()
	result["sessions"] = len(h.sessions)
	h.mu.Unlock()
	if h.mcu != nil {
		if stats := h.mcu.GetStats(); stats != nil {
			result["mcu"] = stats
		}
	}
	return result
}

func (h *Hub) GetServerInfoDialout() (result []talk.BackendServerInfoDialout) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for session := range h.dialoutSessions {
		dialout := talk.BackendServerInfoDialout{
			SessionId: session.PublicId(),
		}
		if client := session.GetClient(); client != nil && client.IsConnected() {
			dialout.Connected = true
			dialout.Address = client.RemoteAddr()
			if ua := client.UserAgent(); ua != "" {
				dialout.UserAgent = ua
				// Extract version from user-agent, expects "software/version".
				if pos := strings.IndexByte(ua, '/'); pos != -1 {
					version := ua[pos+1:]
					if pos = strings.IndexByte(version, ' '); pos != -1 {
						version = version[:pos]
					}
					dialout.Version = version
				}
			}
			dialout.Features = session.GetFeatures()
		}
		result = append(result, dialout)
	}

	slices.SortFunc(result, func(a, b talk.BackendServerInfoDialout) int {
		return strings.Compare(string(a.SessionId), string(b.SessionId))
	})
	return
}

func (h *Hub) getRealUserIP(r *http.Request) string {
	return client.GetRealUserIP(r, h.trustedProxies.Load())
}

func (h *Hub) serveWs(w http.ResponseWriter, r *http.Request) {
	addr := h.getRealUserIP(r)
	agent := r.Header.Get("User-Agent")

	header := http.Header{}
	header.Set("Server", "nextcloud-spreed-signaling/"+h.version)
	header.Set("X-Spreed-Signaling-Features", strings.Join(h.info.Features, ", "))

	conn, err := h.upgrader.Upgrade(w, r, header)
	if err != nil {
		h.logger.Printf("Could not upgrade request from %s: %s", addr, err)
		return
	}

	ctx := log.NewLoggerContext(r.Context(), h.logger)
	if conn.Subprotocol() == janus.EventsSubprotocol {
		janus.RunEventsHandler(ctx, h.mcu, conn, addr, agent)
		return
	}

	client, err := NewHubClient(ctx, conn, addr, agent, h)
	if err != nil {
		h.logger.Printf("Could not create client for %s: %s", addr, err)
		return
	}

	h.processNewClient(client)
	go func(h *Hub) {
		h.writePumpActive.Add(1)
		defer h.writePumpActive.Add(-1)
		client.WritePump()
	}(h)

	h.readPumpActive.Add(1)
	defer h.readPumpActive.Add(-1)
	client.ReadPump()
}

func (h *Hub) ProxySession(request grpc.RpcSessions_ProxySessionServer) error {
	client, err := newRemoteGrpcClient(h, request)
	if err != nil {
		return err
	}

	sid := h.registerClient(client)
	defer h.unregisterClient(sid)

	return client.run()
}

func (h *Hub) LookupCountry(addr string) geoip.Country {
	ip := net.ParseIP(addr)
	if ip == nil {
		return geoip.NoCountry
	}

	if country, found := h.geoipOverrides.Load().Lookup(ip); found {
		return country
	}

	if ip.IsLoopback() {
		return geoip.Loopback
	}

	country := geoip.UnknownCountry
	if h.geoip != nil {
		var err error
		country, err = h.geoip.LookupCountry(ip)
		if err != nil {
			h.logger.Printf("Could not lookup country for %s: %s", ip, err)
			return geoip.UnknownCountry
		}

		if country == "" {
			country = geoip.UnknownCountry
		}
	}
	return country
}

func (h *Hub) ShutdownChannel() <-chan struct{} {
	return h.shutdown.C
}

func (h *Hub) IsShutdownScheduled() bool {
	return h.shutdownScheduled.Load()
}

func (h *Hub) ScheduleShutdown() {
	if !h.shutdownScheduled.CompareAndSwap(false, true) {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.hasSessionsLocked(false) {
		go h.shutdown.Close()
	}
}
