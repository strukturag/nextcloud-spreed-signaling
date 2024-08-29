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
package signaling

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
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/websocket"
)

var (
	DuplicateClient     = NewError("duplicate_client", "Client already registered.")
	HelloExpected       = NewError("hello_expected", "Expected Hello request.")
	InvalidHelloVersion = NewError("invalid_hello_version", "The hello version is not supported.")
	UserAuthFailed      = NewError("auth_failed", "The user could not be authenticated.")
	RoomJoinFailed      = NewError("room_join_failed", "Could not join the room.")
	InvalidClientType   = NewError("invalid_client_type", "The client type is not supported.")
	InvalidBackendUrl   = NewError("invalid_backend", "The backend URL is not supported.")
	InvalidToken        = NewError("invalid_token", "The passed token is invalid.")
	NoSuchSession       = NewError("no_such_session", "The session to resume does not exist.")
	TokenNotValidYet    = NewError("token_not_valid_yet", "The token is not valid yet.")
	TokenExpired        = NewError("token_expired", "The token is expired.")
	TooManyRequests     = NewError("too_many_requests", "Too many requests.")

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

	// Delay after which a screen publisher should be cleaned up.
	cleanupScreenPublisherDelay = time.Second

	// Delay after which a "cleared" / "rejected" dialout status should be removed.
	removeCallStatusTTL = 5 * time.Second

	DefaultTrustedProxies = DefaultPrivateIps()
)

const (
	privateSessionName = "private-session"
	publicSessionName  = "public-session"
)

func init() {
	RegisterHubStats()
}

type Hub struct {
	version      string
	events       AsyncEvents
	upgrader     websocket.Upgrader
	cookie       *securecookie.SecureCookie
	info         *WelcomeServerMessage
	infoInternal *WelcomeServerMessage
	welcome      atomic.Value // *ServerMessage

	closer          *Closer
	readPumpActive  atomic.Int32
	writePumpActive atomic.Int32

	shutdown          *Closer
	shutdownScheduled atomic.Bool

	roomUpdated      chan *BackendServerRoomRequest
	roomDeleted      chan *BackendServerRoomRequest
	roomInCall       chan *BackendServerRoomRequest
	roomParticipants chan *BackendServerRoomRequest

	mu sync.RWMutex
	ru sync.RWMutex

	sid      atomic.Uint64
	clients  map[uint64]HandlerClient
	sessions map[uint64]Session
	rooms    map[string]*Room

	roomSessions    RoomSessions
	roomPing        *RoomPing
	virtualSessions map[string]uint64

	decodeCaches []*LruCache

	mcu                   Mcu
	mcuTimeout            time.Duration
	internalClientsSecret []byte

	allowSubscribeAnyStream bool

	expiredSessions    map[Session]time.Time
	anonymousSessions  map[*ClientSession]time.Time
	expectHelloClients map[HandlerClient]time.Time
	dialoutSessions    map[*ClientSession]bool
	remoteSessions     map[*RemoteSession]bool
	federatedSessions  map[*ClientSession]bool

	backendTimeout time.Duration
	backend        *BackendClient

	trustedProxies atomic.Pointer[AllowedIps]
	geoip          *GeoLookup
	geoipOverrides atomic.Pointer[map[*net.IPNet]string]
	geoipUpdating  atomic.Bool

	rpcServer  *GrpcServer
	rpcClients *GrpcClients

	throttler Throttler

	skipFederationVerify bool
	federationTimeout    time.Duration
}

func NewHub(config *goconf.ConfigFile, events AsyncEvents, rpcServer *GrpcServer, rpcClients *GrpcClients, etcdClient *EtcdClient, r *mux.Router, version string) (*Hub, error) {
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
		return nil, fmt.Errorf("the sessions block key must be 16, 24 or 32 bytes but is %d bytes", len(blockKey))
	}

	internalClientsSecret, _ := config.GetString("clients", "internalsecret")
	if internalClientsSecret == "" {
		log.Println("WARNING: No shared secret has been set for internal clients.")
	}

	maxConcurrentRequestsPerHost, _ := config.GetInt("backend", "connectionsperhost")
	if maxConcurrentRequestsPerHost <= 0 {
		maxConcurrentRequestsPerHost = defaultMaxConcurrentRequestsPerHost
	}

	backend, err := NewBackendClient(config, maxConcurrentRequestsPerHost, version, etcdClient)
	if err != nil {
		return nil, err
	}
	log.Printf("Using a maximum of %d concurrent backend connections per host", maxConcurrentRequestsPerHost)

	backendTimeoutSeconds, _ := config.GetInt("backend", "timeout")
	if backendTimeoutSeconds <= 0 {
		backendTimeoutSeconds = defaultBackendTimeoutSeconds
	}
	backendTimeout := time.Duration(backendTimeoutSeconds) * time.Second
	log.Printf("Using a timeout of %s for backend connections", backendTimeout)

	mcuTimeoutSeconds, _ := config.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second

	allowSubscribeAnyStream, _ := config.GetBool("app", "allowsubscribeany")
	if allowSubscribeAnyStream {
		log.Printf("WARNING: Allow subscribing any streams, this is insecure and should only be enabled for testing")
	}

	trustedProxies, _ := config.GetString("app", "trustedproxies")
	trustedProxiesIps, err := ParseAllowedIps(trustedProxies)
	if err != nil {
		return nil, err
	}

	skipFederationVerify, _ := config.GetBool("federation", "skipverify")
	if skipFederationVerify {
		log.Println("WARNING: Federation target verification is disabled!")
	}
	federationTimeoutSeconds, _ := config.GetInt("federation", "timeout")
	if federationTimeoutSeconds <= 0 {
		federationTimeoutSeconds = defaultFederationTimeoutSeconds
	}
	federationTimeout := time.Duration(federationTimeoutSeconds) * time.Second

	if !trustedProxiesIps.Empty() {
		log.Printf("Trusted proxies: %s", trustedProxiesIps)
	} else {
		trustedProxiesIps = DefaultTrustedProxies
		log.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
	}

	decodeCaches := make([]*LruCache, 0, numDecodeCaches)
	for i := 0; i < numDecodeCaches; i++ {
		decodeCaches = append(decodeCaches, NewLruCache(decodeCacheSize))
	}

	roomSessions, err := NewBuiltinRoomSessions(rpcClients)
	if err != nil {
		return nil, err
	}

	roomPing, err := NewRoomPing(backend, backend.capabilities)
	if err != nil {
		return nil, err
	}

	geoipUrl, _ := config.GetString("geoip", "url")
	if geoipUrl == "default" || geoipUrl == "none" {
		geoipUrl = ""
	}
	if geoipUrl == "" {
		if geoipLicense, _ := config.GetString("geoip", "license"); geoipLicense != "" {
			geoipUrl = GetGeoIpDownloadUrl(geoipLicense)
		}
	}

	var geoip *GeoLookup
	if geoipUrl != "" {
		if strings.HasPrefix(geoipUrl, "file://") {
			geoipUrl = geoipUrl[7:]
			log.Printf("Using GeoIP database from %s", geoipUrl)
			geoip, err = NewGeoLookupFromFile(geoipUrl)
		} else {
			log.Printf("Downloading GeoIP database from %s", geoipUrl)
			geoip, err = NewGeoLookupFromUrl(geoipUrl)
		}
		if err != nil {
			return nil, err
		}
	} else {
		log.Printf("Not using GeoIP database")
	}

	geoipOverrides, err := LoadGeoIPOverrides(config, false)
	if err != nil {
		return nil, err
	}

	throttler, err := NewMemoryThrottler()
	if err != nil {
		return nil, err
	}

	hub := &Hub{
		version: version,
		events:  events,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
		},
		cookie:       securecookie.New([]byte(hashKey), blockBytes).MaxAge(0),
		info:         NewWelcomeServerMessage(version, DefaultFeatures...),
		infoInternal: NewWelcomeServerMessage(version, DefaultFeaturesInternal...),

		closer:   NewCloser(),
		shutdown: NewCloser(),

		roomUpdated:      make(chan *BackendServerRoomRequest),
		roomDeleted:      make(chan *BackendServerRoomRequest),
		roomInCall:       make(chan *BackendServerRoomRequest),
		roomParticipants: make(chan *BackendServerRoomRequest),

		clients:  make(map[uint64]HandlerClient),
		sessions: make(map[uint64]Session),
		rooms:    make(map[string]*Room),

		roomSessions:    roomSessions,
		roomPing:        roomPing,
		virtualSessions: make(map[string]uint64),

		decodeCaches: decodeCaches,

		mcuTimeout:            mcuTimeout,
		internalClientsSecret: []byte(internalClientsSecret),

		allowSubscribeAnyStream: allowSubscribeAnyStream,

		expiredSessions:    make(map[Session]time.Time),
		anonymousSessions:  make(map[*ClientSession]time.Time),
		expectHelloClients: make(map[HandlerClient]time.Time),
		dialoutSessions:    make(map[*ClientSession]bool),
		remoteSessions:     make(map[*RemoteSession]bool),
		federatedSessions:  make(map[*ClientSession]bool),

		backendTimeout: backendTimeout,
		backend:        backend,

		geoip: geoip,

		rpcServer:  rpcServer,
		rpcClients: rpcClients,

		throttler: throttler,

		skipFederationVerify: skipFederationVerify,
		federationTimeout:    federationTimeout,
	}
	hub.trustedProxies.Store(trustedProxiesIps)
	if len(geoipOverrides) > 0 {
		hub.geoipOverrides.Store(&geoipOverrides)
	}
	hub.setWelcomeMessage(&ServerMessage{
		Type:    "welcome",
		Welcome: NewWelcomeServerMessage(version, DefaultWelcomeFeatures...),
	})
	backend.hub = hub
	if rpcServer != nil {
		rpcServer.hub = hub
	}
	hub.upgrader.CheckOrigin = hub.checkOrigin
	r.HandleFunc("/spreed", func(w http.ResponseWriter, r *http.Request) {
		hub.serveWs(w, r)
	})

	return hub, nil
}

func (h *Hub) setWelcomeMessage(msg *ServerMessage) {
	h.welcome.Store(msg)
}

func (h *Hub) getWelcomeMessage() *ServerMessage {
	return h.welcome.Load().(*ServerMessage)
}

func (h *Hub) SetMcu(mcu Mcu) {
	h.mcu = mcu
	// Create copy of message so it can be updated concurrently.
	welcome := *h.getWelcomeMessage()
	if mcu == nil {
		h.info.RemoveFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)
		h.infoInternal.RemoveFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)

		welcome.Welcome.RemoveFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)
	} else {
		log.Printf("Using a timeout of %s for MCU requests", h.mcuTimeout)
		h.info.AddFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)
		h.infoInternal.AddFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)

		welcome.Welcome.AddFeature(ServerFeatureMcu, ServerFeatureSimulcast, ServerFeatureUpdateSdp)
	}
	h.setWelcomeMessage(&welcome)
}

func (h *Hub) checkOrigin(r *http.Request) bool {
	// We allow any Origin to connect to the service.
	return true
}

func (h *Hub) GetServerInfo(session Session) *WelcomeServerMessage {
	if session.ClientType() == HelloClientTypeInternal {
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
	backoff, err := NewExponentialBackoff(time.Second, 5*time.Minute)
	if err != nil {
		log.Printf("Could not create exponential backoff: %s", err)
		return
	}

	for !h.closer.IsClosed() {
		err := h.geoip.Update()
		if err == nil {
			break
		}

		log.Printf("Could not update GeoIP database, will retry in %s (%s)", backoff.NextWait(), err)
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

func (h *Hub) Reload(config *goconf.ConfigFile) {
	trustedProxies, _ := config.GetString("app", "trustedproxies")
	if trustedProxiesIps, err := ParseAllowedIps(trustedProxies); err == nil {
		if !trustedProxiesIps.Empty() {
			log.Printf("Trusted proxies: %s", trustedProxiesIps)
		} else {
			trustedProxiesIps = DefaultTrustedProxies
			log.Printf("No trusted proxies configured, only allowing for %s", trustedProxiesIps)
		}
		h.trustedProxies.Store(trustedProxiesIps)
	} else {
		log.Printf("Error parsing trusted proxies from \"%s\": %s", trustedProxies, err)
	}

	geoipOverrides, _ := LoadGeoIPOverrides(config, true)
	if len(geoipOverrides) > 0 {
		h.geoipOverrides.Store(&geoipOverrides)
	} else {
		h.geoipOverrides.Store(nil)
	}

	if h.mcu != nil {
		h.mcu.Reload(config)
	}
	h.backend.Reload(config)
	h.rpcClients.Reload(config)
}

func reverseSessionId(s string) (string, error) {
	// Note that we are assuming base64 encoded strings here.
	decoded, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}

	for i, j := 0, len(decoded)-1; i < j; i, j = i+1, j-1 {
		decoded[i], decoded[j] = decoded[j], decoded[i]
	}
	return base64.URLEncoding.EncodeToString(decoded), nil
}

func (h *Hub) encodeSessionId(data *SessionIdData, sessionType string) (string, error) {
	encoded, err := h.cookie.Encode(sessionType, data)
	if err != nil {
		return "", err
	}
	if sessionType == publicSessionName {
		// We are reversing the public session ids because clients compare them
		// to decide who calls whom. The prefix of the session id is increasing
		// (a timestamp) but the suffix the (random) hash.
		// By reversing we move the hash to the front, making the comparison of
		// session ids "random".
		encoded, err = reverseSessionId(encoded)
	}
	return encoded, err
}

func (h *Hub) getDecodeCache(cache_key string) *LruCache {
	hash := fnv.New32a()
	hash.Write([]byte(cache_key)) // nolint
	idx := hash.Sum32() % uint32(len(h.decodeCaches))
	return h.decodeCaches[idx]
}

func (h *Hub) invalidateSessionId(id string, sessionType string) {
	if len(id) == 0 {
		return
	}

	cache_key := id + "|" + sessionType
	cache := h.getDecodeCache(cache_key)
	cache.Remove(cache_key)
}

func (h *Hub) setDecodedSessionId(id string, sessionType string, data *SessionIdData) {
	if len(id) == 0 {
		return
	}

	cache_key := id + "|" + sessionType
	cache := h.getDecodeCache(cache_key)
	cache.Set(cache_key, data)
}

func (h *Hub) decodeSessionId(id string, sessionType string) *SessionIdData {
	if len(id) == 0 {
		return nil
	}

	cache_key := id + "|" + sessionType
	cache := h.getDecodeCache(cache_key)
	if result := cache.Get(cache_key); result != nil {
		return result.(*SessionIdData)
	}

	if sessionType == publicSessionName {
		var err error
		id, err = reverseSessionId(id)
		if err != nil {
			return nil
		}
	}

	var data SessionIdData
	if h.cookie.Decode(sessionType, id, &data) != nil {
		return nil
	}

	cache.Set(cache_key, &data)
	return &data
}

func (h *Hub) GetSessionByPublicId(sessionId string) Session {
	data := h.decodeSessionId(sessionId, publicSessionName)
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

func (h *Hub) GetSessionByResumeId(resumeId string) Session {
	data := h.decodeSessionId(resumeId, privateSessionName)
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

func (h *Hub) GetSessionIdByRoomSessionId(roomSessionId string) (string, error) {
	return h.roomSessions.GetSessionId(roomSessionId)
}

func (h *Hub) GetDialoutSession(roomId string, backend *Backend) *ClientSession {
	url := backend.Url()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for session := range h.dialoutSessions {
		if session.backend.Url() != url {
			continue
		}

		if session.GetClient() != nil {
			return session
		}
	}

	return nil
}

func (h *Hub) GetBackend(u *url.URL) *Backend {
	return h.backend.GetBackend(u)
}

func (h *Hub) checkExpiredSessions(now time.Time) {
	for session, expires := range h.expiredSessions {
		if now.After(expires) {
			h.mu.Unlock()
			log.Printf("Closing expired session %s (private=%s)", session.PublicId(), session.PrivateId())
			session.Close()
			h.mu.Lock()
			// Should already be deleted by the close code, but better be sure.
			delete(h.expiredSessions, session)
		}
	}
}

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
	h.checkExpiredSessions(now)
	h.checkAnonymousSessions(now)
	h.checkInitialHello(now)
	h.mu.Unlock()
}

func (h *Hub) removeSession(session Session) (removed bool) {
	session.LeaveRoom(true)
	h.invalidateSessionId(session.PrivateId(), privateSessionName)
	h.invalidateSessionId(session.PublicId(), publicSessionName)

	h.mu.Lock()
	if data := session.Data(); data != nil && data.Sid > 0 {
		delete(h.clients, data.Sid)
		if _, found := h.sessions[data.Sid]; found {
			delete(h.sessions, data.Sid)
			statsHubSessionsCurrent.WithLabelValues(session.Backend().Id(), session.ClientType()).Dec()
			removed = true
		}
	}
	delete(h.expiredSessions, session)
	if session, ok := session.(*ClientSession); ok {
		delete(h.anonymousSessions, session)
		delete(h.dialoutSessions, session)
	}
	if h.IsShutdownScheduled() && !h.hasSessionsLocked(false) {
		go h.shutdown.Close()
	}
	h.mu.Unlock()
	return
}

func (h *Hub) hasSessionsLocked(withInternal bool) bool {
	if withInternal {
		return len(h.sessions) > 0
	}

	for _, s := range h.sessions {
		if s.ClientType() != HelloClientTypeInternal {
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

func (h *Hub) startWaitAnonymousSessionRoomLocked(session *ClientSession) {
	if session.ClientType() == HelloClientTypeInternal {
		// Internal clients don't need to join a room.
		return
	}

	// Anonymous sessions must join a public room within a given time,
	// otherwise they get disconnected to avoid blocking resources forever.
	now := time.Now()
	h.anonymousSessions[session] = now.Add(anonmyousJoinRoomTimeout)
}

func (h *Hub) startExpectHello(client HandlerClient) {
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

func (h *Hub) processNewClient(client HandlerClient) {
	h.startExpectHello(client)
	h.sendWelcome(client)
}

func (h *Hub) sendWelcome(client HandlerClient) {
	client.SendMessage(h.getWelcomeMessage())
}

func (h *Hub) registerClient(client HandlerClient) uint64 {
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

func (h *Hub) newSessionIdData(backend *Backend) *SessionIdData {
	sid := h.sid.Add(1)
	for sid == 0 {
		sid = h.sid.Add(1)
	}
	sessionIdData := &SessionIdData{
		Sid:       sid,
		Created:   time.Now(),
		BackendId: backend.Id(),
	}
	return sessionIdData
}

func (h *Hub) processRegister(c HandlerClient, message *ClientMessage, backend *Backend, auth *BackendClientResponse) {
	if !c.IsConnected() {
		// Client disconnected while waiting for "hello" response.
		return
	}

	if auth.Type == "error" {
		c.SendMessage(message.NewErrorServerMessage(auth.Error))
		return
	} else if auth.Type != "auth" {
		c.SendMessage(message.NewErrorServerMessage(UserAuthFailed))
		return
	}

	client, ok := c.(*Client)
	if !ok {
		log.Printf("Can't register non-client %T", c)
		client.SendMessage(message.NewWrappedErrorServerMessage(errors.New("can't register non-client")))
		return
	}

	sessionIdData := h.newSessionIdData(backend)
	privateSessionId, err := h.encodeSessionId(sessionIdData, privateSessionName)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}
	publicSessionId, err := h.encodeSessionId(sessionIdData, publicSessionName)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	userId := auth.Auth.UserId
	if userId != "" {
		log.Printf("Register user %s@%s from %s in %s (%s) %s (private=%s)", userId, backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	} else if message.Hello.Auth.Type != HelloClientTypeClient {
		log.Printf("Register %s@%s from %s in %s (%s) %s (private=%s)", message.Hello.Auth.Type, backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	} else {
		log.Printf("Register anonymous@%s from %s in %s (%s) %s (private=%s)", backend.Id(), client.RemoteAddr(), client.Country(), client.UserAgent(), publicSessionId, privateSessionId)
	}

	session, err := NewClientSession(h, privateSessionId, publicSessionId, sessionIdData, backend, message.Hello, auth.Auth)
	if err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	if err := backend.AddSession(session); err != nil {
		log.Printf("Error adding session %s to backend %s: %s", session.PublicId(), backend.Id(), err)
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
		for _, client := range h.rpcClients.GetClients() {
			wg.Add(1)
			go func(c *GrpcClient) {
				defer wg.Done()

				count, err := c.GetSessionCount(ctx, backend.ParsedUrl())
				if err != nil {
					log.Printf("Received error while getting session count for %s from %s: %s", backend.Url(), c.Target(), err)
					return
				}

				if count > 0 {
					log.Printf("%d sessions connected for %s on %s", count, backend.Url(), c.Target())
					totalCount.Add(count)
				}
			}(client)
		}
		wg.Wait()
		if totalCount.Load() > limit {
			backend.RemoveSession(session)
			log.Printf("Error adding session %s to backend %s: %s", session.PublicId(), backend.Id(), SessionLimitExceeded)
			session.Close()
			client.SendMessage(message.NewWrappedErrorServerMessage(SessionLimitExceeded))
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
	if userId == "" && session.ClientType() != HelloClientTypeInternal {
		h.startWaitAnonymousSessionRoomLocked(session)
	} else if session.ClientType() == HelloClientTypeInternal && session.HasFeature(ClientFeatureStartDialout) {
		// TODO: There is a small race condition for sessions that take some time
		// between connecting and joining a room.
		h.dialoutSessions[session] = true
	}
	h.mu.Unlock()

	if country := client.Country(); IsValidCountry(country) {
		statsClientCountries.WithLabelValues(country).Inc()
	}
	statsHubSessionsCurrent.WithLabelValues(backend.Id(), session.ClientType()).Inc()
	statsHubSessionsTotal.WithLabelValues(backend.Id(), session.ClientType()).Inc()

	h.setDecodedSessionId(privateSessionId, privateSessionName, sessionIdData)
	h.setDecodedSessionId(publicSessionId, publicSessionName, sessionIdData)
	h.sendHelloResponse(session, message)
}

func (h *Hub) processUnregister(client HandlerClient) Session {
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
		log.Printf("Unregister %s (private=%s)", session.PublicId(), session.PrivateId())
		if c, ok := client.(*Client); ok {
			if cs, ok := session.(*ClientSession); ok {
				cs.ClearClient(c)
			}
		}
	}

	client.Close()
	return session
}

func (h *Hub) processMessage(client HandlerClient, data []byte) {
	var message ClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Error decoding message from client %s: %v", session.PublicId(), err)
			session.SendError(InvalidFormat)
		} else {
			log.Printf("Error decoding message from %s: %v", client.RemoteAddr(), err)
			client.SendError(InvalidFormat)
		}
		return
	}

	if err := message.CheckValid(); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
			if err, ok := err.(*Error); ok {
				session.SendMessage(message.NewErrorServerMessage(err))
			} else {
				session.SendMessage(message.NewErrorServerMessage(InvalidFormat))
			}
		} else {
			log.Printf("Invalid message %+v from %s: %v", message, client.RemoteAddr(), err)
			if err, ok := err.(*Error); ok {
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
		log.Printf("Ignore hello %+v for already authenticated connection %s", message.Hello, session.PublicId())
	default:
		log.Printf("Ignore unknown message %+v from %s", message, session.PublicId())
	}
}

func (h *Hub) sendHelloResponse(session *ClientSession, message *ClientMessage) bool {
	response := &ServerMessage{
		Id:   message.Id,
		Type: "hello",
		Hello: &HelloServerMessage{
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
	client   *GrpcClient
	response *LookupResumeIdReply
}

func (h *Hub) tryProxyResume(c HandlerClient, resumeId string, message *ClientMessage) bool {
	client, ok := c.(*Client)
	if !ok {
		return false
	}

	var clients []*GrpcClient
	if h.rpcClients != nil {
		clients = h.rpcClients.GetClients()
	}
	if len(clients) == 0 {
		return false
	}

	rpcCtx, rpcCancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer rpcCancel()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(rpcCtx)
	defer cancel()

	var remoteClient atomic.Pointer[remoteClientInfo]
	for _, c := range clients {
		wg.Add(1)
		go func(client *GrpcClient) {
			defer wg.Done()

			if client.IsSelf() {
				return
			}

			response, err := client.LookupResumeId(ctx, resumeId)
			if err != nil {
				log.Printf("Could not lookup resume id %s on %s: %s", resumeId, client.Target(), err)
				return
			}

			cancel()
			remoteClient.CompareAndSwap(nil, &remoteClientInfo{
				client:   client,
				response: response,
			})
		}(c)
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

	rs, err := NewRemoteSession(h, client, info.client, info.response.SessionId)
	if err != nil {
		log.Printf("Could not create remote session %s on %s: %s", info.response.SessionId, info.client.Target(), err)
		return false
	}

	if err := rs.Start(message); err != nil {
		rs.Close()
		log.Printf("Could not start remote session %s on %s: %s", info.response.SessionId, info.client.Target(), err)
		return false
	}

	log.Printf("Proxy session %s to %s", info.response.SessionId, info.client.Target())
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remoteSessions[rs] = true
	delete(h.expectHelloClients, client)
	return true
}

func (h *Hub) processHello(client HandlerClient, message *ClientMessage) {
	ctx := context.TODO()
	resumeId := message.Hello.ResumeId
	if resumeId != "" {
		throttle, err := h.throttler.CheckBruteforce(ctx, client.RemoteAddr(), "HelloResume")
		if err == ErrBruteforceDetected {
			client.SendMessage(message.NewErrorServerMessage(TooManyRequests))
			return
		} else if err != nil {
			log.Printf("Error checking for bruteforce: %s", err)
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		data := h.decodeSessionId(resumeId, privateSessionName)
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
			log.Printf("Client resumed non-client session %s (private=%s)", session.PublicId(), session.PrivateId())
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
			log.Printf("Closing previous client from %s for session %s", prev.RemoteAddr(), session.PublicId())
			prev.SendByeResponseWithReason(nil, "session_resumed")
		}

		delete(h.expiredSessions, clientSession)
		h.clients[data.Sid] = client
		delete(h.expectHelloClients, client)
		h.mu.Unlock()

		log.Printf("Resume session from %s in %s (%s) %s (private=%s)", client.RemoteAddr(), client.Country(), client.UserAgent(), session.PublicId(), session.PrivateId())

		statsHubSessionsResumedTotal.WithLabelValues(clientSession.Backend().Id(), clientSession.ClientType()).Inc()
		h.sendHelloResponse(clientSession, message)
		clientSession.NotifySessionResumed(client)
		return
	}

	// Make sure client doesn't get disconnected while calling auth backend.
	h.mu.Lock()
	delete(h.expectHelloClients, client)
	h.mu.Unlock()

	switch message.Hello.Auth.Type {
	case HelloClientTypeClient:
		fallthrough
	case HelloClientTypeFederation:
		h.processHelloClient(client, message)
	case HelloClientTypeInternal:
		h.processHelloInternal(client, message)
	default:
		h.startExpectHello(client)
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
	}
}

func (h *Hub) processHelloV1(ctx context.Context, client HandlerClient, message *ClientMessage) (*Backend, *BackendClientResponse, error) {
	url := message.Hello.Auth.parsedUrl
	backend := h.backend.GetBackend(url)
	if backend == nil {
		return nil, nil, InvalidBackendUrl
	}

	// Run in timeout context to prevent blocking too long.
	ctx, cancel := context.WithTimeout(ctx, h.backendTimeout)
	defer cancel()

	var auth BackendClientResponse
	request := NewBackendClientAuthRequest(message.Hello.Auth.Params)
	if err := h.backend.PerformJSONRequest(ctx, url, request, &auth); err != nil {
		return nil, nil, err
	}

	// TODO(jojo): Validate response

	return backend, &auth, nil
}

func (h *Hub) processHelloV2(ctx context.Context, client HandlerClient, message *ClientMessage) (*Backend, *BackendClientResponse, error) {
	url := message.Hello.Auth.parsedUrl
	backend := h.backend.GetBackend(url)
	if backend == nil {
		return nil, nil, InvalidBackendUrl
	}

	var tokenString string
	var tokenClaims jwt.Claims
	switch message.Hello.Auth.Type {
	case HelloClientTypeClient:
		tokenString = message.Hello.Auth.helloV2Params.Token
		tokenClaims = &HelloV2TokenClaims{}
	case HelloClientTypeFederation:
		if !h.backend.capabilities.HasCapabilityFeature(ctx, url, FeatureFederationV2) {
			return nil, nil, ErrFederationNotSupported
		}

		tokenString = message.Hello.Auth.federationParams.Token
		tokenClaims = &FederationTokenClaims{}
	default:
		return nil, nil, InvalidClientType
	}
	token, err := jwt.ParseWithClaims(tokenString, tokenClaims, func(token *jwt.Token) (interface{}, error) {
		// Only public-private-key algorithms are supported.
		var loadKeyFunc func([]byte) (interface{}, error)
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			loadKeyFunc = func(data []byte) (interface{}, error) {
				return jwt.ParseRSAPublicKeyFromPEM(data)
			}
		case *jwt.SigningMethodECDSA:
			loadKeyFunc = func(data []byte) (interface{}, error) {
				return jwt.ParseECPublicKeyFromPEM(data)
			}
		case *jwt.SigningMethodEd25519:
			loadKeyFunc = func(data []byte) (interface{}, error) {
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
			log.Printf("Unexpected signing method: %v", token.Header["alg"])
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}

		// Run in timeout context to prevent blocking too long.
		backendCtx, cancel := context.WithTimeout(ctx, h.backendTimeout)
		defer cancel()

		keyData, cached, found := h.backend.capabilities.GetStringConfig(backendCtx, url, ConfigGroupSignaling, ConfigKeyHelloV2TokenKey)
		if !found {
			if cached {
				// The Nextcloud instance might just have enabled JWT but we probably use
				// the cached capabilities without the public key. Make sure to re-fetch.
				h.backend.capabilities.InvalidateCapabilities(url)
				keyData, _, found = h.backend.capabilities.GetStringConfig(backendCtx, url, ConfigGroupSignaling, ConfigKeyHelloV2TokenKey)
			}
			if !found {
				return nil, fmt.Errorf("No key found for issuer")
			}
		}

		key, err := loadKeyFunc([]byte(keyData))
		if err != nil {
			return nil, fmt.Errorf("Could not parse token key: %w", err)
		}

		return key, nil
	})
	if err != nil {
		if err, ok := err.(*jwt.ValidationError); ok {
			if err.Errors&jwt.ValidationErrorIssuedAt == jwt.ValidationErrorIssuedAt {
				return nil, nil, TokenNotValidYet
			}
			if err.Errors&jwt.ValidationErrorExpired == jwt.ValidationErrorExpired {
				return nil, nil, TokenExpired
			}
		}

		return nil, nil, InvalidToken
	}

	var authTokenClaims AuthTokenClaims
	switch message.Hello.Auth.Type {
	case HelloClientTypeClient:
		claims, ok := token.Claims.(*HelloV2TokenClaims)
		if !ok || !token.Valid {
			return nil, nil, InvalidToken
		}
		authTokenClaims = claims
	case HelloClientTypeFederation:
		claims, ok := token.Claims.(*FederationTokenClaims)
		if !ok || !token.Valid {
			return nil, nil, InvalidToken
		}
		authTokenClaims = claims
	}
	now := time.Now()
	if !authTokenClaims.VerifyIssuedAt(now, true) {
		return nil, nil, TokenNotValidYet
	}
	if !authTokenClaims.VerifyExpiresAt(now, true) {
		return nil, nil, TokenExpired
	}

	auth := &BackendClientResponse{
		Type: "auth",
		Auth: &BackendClientAuthResponse{
			Version: message.Hello.Version,
			UserId:  authTokenClaims.TokenSubject(),
			User:    authTokenClaims.TokenUserData(),
		},
	}
	return backend, auth, nil
}

func (h *Hub) processHelloClient(client HandlerClient, message *ClientMessage) {
	// Make sure the client must send another "hello" in case of errors.
	defer h.startExpectHello(client)

	var authFunc func(context.Context, HandlerClient, *ClientMessage) (*Backend, *BackendClientResponse, error)
	switch message.Hello.Version {
	case HelloVersionV1:
		// Auth information contains a ticket that must be validated against the
		// Nextcloud instance.
		authFunc = h.processHelloV1
	case HelloVersionV2:
		// Auth information contains a JWT that contains all information of the user.
		authFunc = h.processHelloV2
	default:
		client.SendMessage(message.NewErrorServerMessage(InvalidHelloVersion))
		return
	}

	backend, auth, err := authFunc(client.Context(), client, message)
	if err != nil {
		if e, ok := err.(*Error); ok {
			client.SendMessage(message.NewErrorServerMessage(e))
		} else {
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
		}
		return
	}

	h.processRegister(client, message, backend, auth)
}

func (h *Hub) processHelloInternal(client HandlerClient, message *ClientMessage) {
	defer h.startExpectHello(client)
	if len(h.internalClientsSecret) == 0 {
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
		return
	}

	ctx := context.TODO()
	throttle, err := h.throttler.CheckBruteforce(ctx, client.RemoteAddr(), "HelloInternal")
	if err == ErrBruteforceDetected {
		client.SendMessage(message.NewErrorServerMessage(TooManyRequests))
		return
	} else if err != nil {
		log.Printf("Error checking for bruteforce: %s", err)
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	// Validate internal connection.
	rnd := message.Hello.Auth.internalParams.Random
	mac := hmac.New(sha256.New, h.internalClientsSecret)
	mac.Write([]byte(rnd)) // nolint
	check := hex.EncodeToString(mac.Sum(nil))
	if len(rnd) < minTokenRandomLength || check != message.Hello.Auth.internalParams.Token {
		throttle(ctx)
		client.SendMessage(message.NewErrorServerMessage(InvalidToken))
		return
	}

	backend := h.backend.GetBackend(message.Hello.Auth.internalParams.parsedBackend)
	if backend == nil {
		throttle(ctx)
		client.SendMessage(message.NewErrorServerMessage(InvalidBackendUrl))
		return
	}

	auth := &BackendClientResponse{
		Type: "auth",
		Auth: &BackendClientAuthResponse{},
	}
	h.processRegister(client, message, backend, auth)
}

func (h *Hub) disconnectByRoomSessionId(ctx context.Context, roomSessionId string, backend *Backend) {
	sessionId, err := h.roomSessions.LookupSessionId(ctx, roomSessionId, "room_session_reconnected")
	if err == ErrNoSuchRoomSession {
		return
	} else if err != nil {
		log.Printf("Could not get session id for room session %s: %s", roomSessionId, err)
		return
	}

	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		// Session is located on a different server. Should already have been closed
		// but send "bye" again as additional safeguard.
		msg := &AsyncMessage{
			Type: "message",
			Message: &ServerMessage{
				Type: "bye",
				Bye: &ByeServerMessage{
					Reason: "room_session_reconnected",
				},
			},
		}
		if err := h.events.PublishSessionMessage(sessionId, backend, msg); err != nil {
			log.Printf("Could not send reconnect bye to session %s: %s", sessionId, err)
		}
		return
	}

	log.Printf("Closing session %s because same room session %s connected", session.PublicId(), roomSessionId)
	session.LeaveRoom(false)
	switch sess := session.(type) {
	case *ClientSession:
		if client := sess.GetClient(); client != nil {
			client.SendByeResponseWithReason(nil, "room_session_reconnected")
		}
	}
	session.Close()
}

func (h *Hub) sendRoom(session *ClientSession, message *ClientMessage, room *Room) bool {
	response := &ServerMessage{
		Type: "room",
	}
	if message != nil {
		response.Id = message.Id
	}
	if room == nil {
		response.Room = &RoomServerMessage{
			RoomId: "",
		}
	} else {
		response.Room = &RoomServerMessage{
			RoomId:     room.id,
			Properties: room.properties,
		}
	}
	return session.SendMessage(response)
}

func (h *Hub) processRoom(sess Session, message *ClientMessage) {
	session, ok := sess.(*ClientSession)
	if !ok {
		return
	}

	roomId := message.Room.RoomId
	if roomId == "" {
		// We can handle leaving a room directly.
		if session.LeaveRoomWithMessage(true, message) != nil {
			// User was in a room before, so need to notify about leaving it.
			h.sendRoom(session, message, nil)
			if session.UserId() == "" && session.ClientType() != HelloClientTypeInternal {
				h.startWaitAnonymousSessionRoom(session)
			}
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
			var ae *Error
			if errors.As(err, &ae) {
				session.SendMessage(message.NewErrorServerMessage(ae))
				return
			}

			var details interface{}
			var ce *tls.CertificateVerificationError
			if errors.As(err, &ce) {
				details = map[string]string{
					"code":    "certificate_verification_error",
					"message": ce.Error(),
				}
			}
			var ne net.Error
			if details == nil && errors.As(err, &ne) {
				details = map[string]string{
					"code":    "network_error",
					"message": ne.Error(),
				}
			}
			if details == nil {
				var we websocket.HandshakeError
				if errors.Is(err, websocket.ErrBadHandshake) {
					details = map[string]string{
						"code":    "network_error",
						"message": err.Error(),
					}
				} else if errors.As(err, &we) {
					details = map[string]string{
						"code":    "network_error",
						"message": we.Error(),
					}
				}
			}

			log.Printf("Error creating federation client to %s for %s to join room %s: %s", federation.SignalingUrl, session.PublicId(), roomId, err)
			session.SendMessage(message.NewErrorServerMessage(
				NewErrorDetail("federation_error", "Failed to create federation client.", details),
			))
			return
		}

		session.SetFederationClient(client)

		roomSessionId := message.Room.SessionId
		if roomSessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			log.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			roomSessionId = session.PublicId()
		}

		// Prefix room session id to allow using the same signaling server for two Nextcloud instances during development.
		// Otherwise the same room session id will be detected and the other session will be kicked.
		if err := session.UpdateRoomSessionId(FederatedRoomSessionIdPrefix + roomSessionId); err != nil {
			log.Printf("Error updating room session id for session %s: %s", session.PublicId(), err)
		}

		h.mu.Lock()
		h.federatedSessions[session] = true
		h.mu.Unlock()
		return
	}

	if room := h.getRoomForBackend(roomId, session.Backend()); room != nil && room.HasSession(session) {
		// Session already is in that room, no action needed.
		roomSessionId := message.Room.SessionId
		if roomSessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			log.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			roomSessionId = session.PublicId()
		}

		if err := session.UpdateRoomSessionId(roomSessionId); err != nil {
			log.Printf("Error updating room session id for session %s: %s", session.PublicId(), err)
		}
		session.SendMessage(message.NewErrorServerMessage(
			NewErrorDetail("already_joined", "Already joined this room.", &RoomErrorDetails{
				Room: &RoomServerMessage{
					RoomId:     room.id,
					Properties: room.properties,
				},
			}),
		))
		return
	}

	var room BackendClientResponse
	if session.ClientType() == HelloClientTypeInternal {
		// Internal clients can join any room.
		room = BackendClientResponse{
			Type: "room",
			Room: &BackendClientRoomResponse{
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
			log.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			sessionId = session.PublicId()
		}
		request := NewBackendClientRoomRequest(roomId, session.UserId(), sessionId)
		request.Room.UpdateFromSession(session)
		if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &room); err != nil {
			session.SendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		// TODO(jojo): Validate response

		if message.Room.SessionId != "" {
			// There can only be one connection per Nextcloud Talk session,
			// disconnect any other connections without sending a "leave" event.
			ctx, cancel := context.WithTimeout(session.Context(), time.Second)
			defer cancel()

			h.disconnectByRoomSessionId(ctx, message.Room.SessionId, session.Backend())
		}
	}

	h.processJoinRoom(session, message, &room)
}

func (h *Hub) publishFederatedSessions() (int, *sync.WaitGroup) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var wg sync.WaitGroup
	if len(h.federatedSessions) == 0 {
		return 0, &wg
	}

	rooms := make(map[string]map[string][]BackendPingEntry)
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

		var sid string
		var uid string
		// Use Nextcloud session id and user id
		sid = strings.TrimPrefix(session.RoomSessionId(), FederatedRoomSessionIdPrefix)
		uid = session.AuthUserId()
		if sid == "" {
			continue
		}

		roomId := federation.RoomId()
		entries, found := rooms[roomId]
		if !found {
			entries = make(map[string][]BackendPingEntry)
			rooms[roomId] = entries
		}

		e, found := entries[u]
		if !found {
			p := session.ParsedBackendUrl()
			if p == nil {
				// Should not happen, invalid URLs should get rejected earlier.
				continue
			}
			urls[u] = p
		}

		entries[u] = append(e, BackendPingEntry{
			SessionId: sid,
			UserId:    uid,
		})
	}

	if len(urls) == 0 {
		return 0, &wg
	}
	count := 0
	for roomId, entries := range rooms {
		for u, e := range entries {
			wg.Add(1)
			count += len(e)
			go func(roomId string, url *url.URL, entries []BackendPingEntry) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), h.backendTimeout)
				defer cancel()

				if err := h.roomPing.SendPings(ctx, roomId, url, entries); err != nil {
					log.Printf("Error pinging room %s for active entries %+v: %s", roomId, entries, err)
				}
			}(roomId, urls[u], e)
		}
	}
	return count, &wg
}

func (h *Hub) getRoomForBackend(id string, backend *Backend) *Room {
	internalRoomId := getRoomIdForBackend(id, backend)

	h.ru.RLock()
	defer h.ru.RUnlock()
	return h.rooms[internalRoomId]
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

func (h *Hub) createRoom(id string, properties json.RawMessage, backend *Backend) (*Room, error) {
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

func (h *Hub) processJoinRoom(session *ClientSession, message *ClientMessage, room *BackendClientResponse) {
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
		if r, err = h.createRoom(roomId, room.Room.Properties, session.Backend()); err != nil {
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
	if session.ClientType() == HelloClientTypeInternal && session.HasFeature(ClientFeatureStartDialout) {
		// An internal session in a room can not be used for dialout.
		delete(h.dialoutSessions, session)
	}
	h.mu.Unlock()
	session.SetRoom(r)
	if room.Room.Permissions != nil {
		session.SetPermissions(*room.Room.Permissions)
	}
	h.sendRoom(session, message, r)
	r.AddSession(session, room.Room.Session)
}

func (h *Hub) processMessageMsg(sess Session, message *ClientMessage) {
	session, ok := sess.(*ClientSession)
	if !ok {
		// Client is not connected yet.
		return
	}

	msg := message.Message
	var recipient *ClientSession
	var subject string
	var clientData *MessageClientMessageData
	var serverRecipient *MessageClientMessageRecipient
	var recipientSessionId string
	var room *Room
	switch msg.Recipient.Type {
	case RecipientTypeSession:
		if h.mcu != nil {
			// Maybe this is a message to be processed by the MCU.
			var data MessageClientMessageData
			if err := json.Unmarshal(msg.Data, &data); err == nil {
				if err := data.CheckValid(); err != nil {
					log.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
					if err, ok := err.(*Error); ok {
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

							publisher := session.GetPublisher(StreamTypeScreen)
							if publisher == nil {
								return
							}

							log.Printf("Closing screen publisher for %s", session.PublicId())
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

			subject = "session." + msg.Recipient.SessionId
			recipientSessionId = msg.Recipient.SessionId
			if sess, ok := sess.(*ClientSession); ok {
				recipient = sess
			}

			// Send to client connection for virtual sessions.
			if sess.ClientType() == HelloClientTypeVirtual {
				virtualSession := sess.(*VirtualSession)
				clientSession := virtualSession.Session()
				subject = "session." + clientSession.PublicId()
				recipientSessionId = clientSession.PublicId()
				recipient = clientSession
				// The client should see his session id as recipient.
				serverRecipient = &MessageClientMessageRecipient{
					Type:      "session",
					SessionId: virtualSession.SessionId(),
				}
			}
		} else {
			subject = "session." + msg.Recipient.SessionId
			recipientSessionId = msg.Recipient.SessionId
			serverRecipient = &msg.Recipient
		}
	case RecipientTypeUser:
		if msg.Recipient.UserId != "" {
			if msg.Recipient.UserId == session.UserId() {
				// Don't loop messages to the sender.
				// TODO(jojo): Should we allow users to send messages to their
				// other sessions?
				return
			}

			subject = GetSubjectForUserId(msg.Recipient.UserId, session.Backend())
		}
	case RecipientTypeRoom:
		if session != nil {
			if room = session.GetRoom(); room != nil {
				subject = GetSubjectForRoomId(room.Id(), room.Backend())

				if h.mcu != nil {
					var data MessageClientMessageData
					if err := json.Unmarshal(msg.Data, &data); err == nil {
						if err := data.CheckValid(); err != nil {
							log.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
							if err, ok := err.(*Error); ok {
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
		log.Printf("Unknown recipient in message %+v from %s", msg, session.PublicId())
		return
	}

	response := &ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Sender: &MessageServerMessageSender{
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
				log.Printf("Session %s is not allowed to send offer for %s, ignoring (%s)", session.PublicId(), clientData.RoomType, err)
				sendNotAllowed(session, message, "Not allowed to send offer")
				return
			}

			// It may take some time for the publisher (which is the current
			// client) to start his stream, so we must not block the active
			// goroutine.
			go func() {
				ctx, cancel := context.WithTimeout(session.Context(), h.mcuTimeout)
				defer cancel()

				mc, err := recipient.GetOrCreateSubscriber(ctx, h.mcu, session.PublicId(), StreamType(clientData.RoomType))
				if err != nil {
					log.Printf("Could not create MCU subscriber for session %s to send %+v to %s: %s", session.PublicId(), clientData, recipient.PublicId(), err)
					sendMcuClientNotFound(session, message)
					return
				} else if mc == nil {
					log.Printf("No MCU subscriber found for session %s to send %+v to %s", session.PublicId(), clientData, recipient.PublicId())
					sendMcuClientNotFound(session, message)
					return
				}

				mc.SendMessage(session.Context(), msg, clientData, func(err error, response map[string]interface{}) {
					if err != nil {
						log.Printf("Could not send MCU message %+v for session %s to %s: %s", clientData, session.PublicId(), recipient.PublicId(), err)
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
				log.Printf("Session %s is not allowed to send offer for %s, ignoring (%s)", session.PublicId(), clientData.RoomType, err)
				sendNotAllowed(session, message, "Not allowed to send offer")
				return
			}

			async := &AsyncMessage{
				Type: "sendoffer",
				SendOffer: &SendOfferMessage{
					MessageId: message.Id,
					SessionId: session.PublicId(),
					Data:      clientData,
				},
			}
			if err := h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async); err != nil {
				log.Printf("Error publishing message to remote session: %s", err)
			}
			return
		}

		async := &AsyncMessage{
			Type:    "message",
			Message: response,
		}
		var err error
		switch msg.Recipient.Type {
		case RecipientTypeSession:
			err = h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async)
		case RecipientTypeUser:
			err = h.events.PublishUserMessage(msg.Recipient.UserId, session.Backend(), async)
		case RecipientTypeRoom:
			err = h.events.PublishRoomMessage(room.Id(), session.Backend(), async)
		default:
			err = fmt.Errorf("unsupported recipient type: %s", msg.Recipient.Type)
		}

		if err != nil {
			log.Printf("Error publishing message to remote session: %s", err)
		}
	}
}

func isAllowedToControl(session Session) bool {
	if session.ClientType() == HelloClientTypeInternal {
		// Internal clients are allowed to send any control message.
		return true
	}

	if session.HasPermission(PERMISSION_MAY_CONTROL) {
		// Moderator clients are allowed to send any control message.
		return true
	}

	return false
}

func (h *Hub) processControlMsg(session Session, message *ClientMessage) {
	msg := message.Control
	if !isAllowedToControl(session) {
		log.Printf("Ignore control message %+v from %s", msg, session.PublicId())
		return
	}

	var recipient *ClientSession
	var subject string
	var serverRecipient *MessageClientMessageRecipient
	var recipientSessionId string
	var room *Room
	switch msg.Recipient.Type {
	case RecipientTypeSession:
		data := h.decodeSessionId(msg.Recipient.SessionId, publicSessionName)
		if data != nil {
			if msg.Recipient.SessionId == session.PublicId() {
				// Don't loop messages to the sender.
				return
			}

			subject = "session." + msg.Recipient.SessionId
			recipientSessionId = msg.Recipient.SessionId
			h.mu.RLock()
			sess, found := h.sessions[data.Sid]
			if found && sess.PublicId() == msg.Recipient.SessionId {
				if sess, ok := sess.(*ClientSession); ok {
					recipient = sess
				}

				// Send to client connection for virtual sessions.
				if sess.ClientType() == HelloClientTypeVirtual {
					virtualSession := sess.(*VirtualSession)
					clientSession := virtualSession.Session()
					subject = "session." + clientSession.PublicId()
					recipientSessionId = clientSession.PublicId()
					recipient = clientSession
					// The client should see his session id as recipient.
					serverRecipient = &MessageClientMessageRecipient{
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
	case RecipientTypeUser:
		if msg.Recipient.UserId != "" {
			if msg.Recipient.UserId == session.UserId() {
				// Don't loop messages to the sender.
				// TODO(jojo): Should we allow users to send messages to their
				// other sessions?
				return
			}

			subject = GetSubjectForUserId(msg.Recipient.UserId, session.Backend())
		}
	case RecipientTypeRoom:
		if session != nil {
			if room = session.GetRoom(); room != nil {
				subject = GetSubjectForRoomId(room.Id(), room.Backend())
			}
		}
	}
	if subject == "" {
		log.Printf("Unknown recipient in message %+v from %s", msg, session.PublicId())
		return
	}

	response := &ServerMessage{
		Type: "control",
		Control: &ControlServerMessage{
			Sender: &MessageServerMessageSender{
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
		async := &AsyncMessage{
			Type:    "message",
			Message: response,
		}
		var err error
		switch msg.Recipient.Type {
		case RecipientTypeSession:
			err = h.events.PublishSessionMessage(recipientSessionId, session.Backend(), async)
		case RecipientTypeUser:
			err = h.events.PublishUserMessage(msg.Recipient.UserId, session.Backend(), async)
		case RecipientTypeRoom:
			err = h.events.PublishRoomMessage(room.Id(), room.Backend(), async)
		default:
			err = fmt.Errorf("unsupported recipient type: %s", msg.Recipient.Type)
		}
		if err != nil {
			log.Printf("Error publishing message to remote session: %s", err)
		}
	}
}

func (h *Hub) processInternalMsg(sess Session, message *ClientMessage) {
	msg := message.Internal
	session, ok := sess.(*ClientSession)
	if !ok {
		// Client is not connected yet.
		return
	} else if session.ClientType() != HelloClientTypeInternal {
		log.Printf("Ignore internal message %+v from %s", msg, session.PublicId())
		return
	}

	if session.ProcessResponse(message) {
		return
	}

	switch msg.Type {
	case "addsession":
		msg := msg.AddSession
		room := h.getRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			log.Printf("Ignore add session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
			return
		}

		sessionIdData := h.newSessionIdData(session.Backend())
		privateSessionId, err := h.encodeSessionId(sessionIdData, privateSessionName)
		if err != nil {
			log.Printf("Could not encode private virtual session id: %s", err)
			return
		}
		publicSessionId, err := h.encodeSessionId(sessionIdData, publicSessionName)
		if err != nil {
			log.Printf("Could not encode public virtual session id: %s", err)
			return
		}

		ctx, cancel := context.WithTimeout(session.Context(), h.backendTimeout)
		defer cancel()

		virtualSessionId := GetVirtualSessionId(session, msg.SessionId)

		sess, err := NewVirtualSession(session, privateSessionId, publicSessionId, sessionIdData, msg)
		if err != nil {
			log.Printf("Could not create virtual session %s: %s", virtualSessionId, err)
			reply := message.NewErrorServerMessage(NewError("add_failed", "Could not create virtual session."))
			session.SendMessage(reply)
			return
		}

		if msg.Options != nil {
			request := NewBackendClientRoomRequest(room.Id(), msg.UserId, publicSessionId)
			request.Room.ActorId = msg.Options.ActorId
			request.Room.ActorType = msg.Options.ActorType
			request.Room.InCall = sess.GetInCall()

			var response BackendClientResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &response); err != nil {
				sess.Close()
				log.Printf("Could not join virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(NewError("add_failed", "Could not join virtual session."))
				session.SendMessage(reply)
				return
			}

			if response.Type == "error" {
				sess.Close()
				log.Printf("Could not join virtual session %s at backend %s: %+v", virtualSessionId, session.BackendUrl(), response.Error)
				reply := message.NewErrorServerMessage(NewError("add_failed", response.Error.Error()))
				session.SendMessage(reply)
				return
			}
		} else {
			request := NewBackendClientSessionRequest(room.Id(), "add", publicSessionId, msg)
			var response BackendClientSessionResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &response); err != nil {
				sess.Close()
				log.Printf("Could not add virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(NewError("add_failed", "Could not add virtual session."))
				session.SendMessage(reply)
				return
			}
		}

		h.mu.Lock()
		h.sessions[sessionIdData.Sid] = sess
		h.virtualSessions[virtualSessionId] = sessionIdData.Sid
		h.mu.Unlock()
		statsHubSessionsCurrent.WithLabelValues(session.Backend().Id(), sess.ClientType()).Inc()
		statsHubSessionsTotal.WithLabelValues(session.Backend().Id(), sess.ClientType()).Inc()
		log.Printf("Session %s added virtual session %s with initial flags %d", session.PublicId(), sess.PublicId(), sess.Flags())
		session.AddVirtualSession(sess)
		sess.SetRoom(room)
		room.AddSession(sess, nil)
	case "updatesession":
		msg := msg.UpdateSession
		room := h.getRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			log.Printf("Ignore remove session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
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
				log.Printf("Ignore update request for non-virtual session %s", sess.PublicId())
			}
			if changed != 0 {
				room.NotifySessionChanged(sess, changed)
			}
		}
	case "removesession":
		msg := msg.RemoveSession
		room := h.getRoomForBackend(msg.RoomId, session.Backend())
		if room == nil {
			log.Printf("Ignore remove session message %+v for invalid room %s from %s", *msg, msg.RoomId, session.PublicId())
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
			log.Printf("Session %s removed virtual session %s", session.PublicId(), sess.PublicId())
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
			asyncMessage := &AsyncMessage{
				Type: "room",
				Room: &BackendServerRoomRequest{
					Type: "transient",
					Transient: &BackendRoomTransientRequest{
						Action: TransientActionSet,
						Key:    "callstatus_" + msg.Dialout.Status.CallId,
						Value:  msg.Dialout.Status,
					},
				},
			}
			if msg.Dialout.Status.Status == DialoutStatusCleared || msg.Dialout.Status.Status == DialoutStatusRejected {
				asyncMessage.Room.Transient.TTL = removeCallStatusTTL
			}
			if err := h.events.PublishBackendRoomMessage(roomId, session.Backend(), asyncMessage); err != nil {
				log.Printf("Error publishing dialout message %+v to room %s", msg.Dialout, roomId)
			}
		} else {
			if err := h.events.PublishRoomMessage(roomId, session.Backend(), &AsyncMessage{
				Type: "message",
				Message: &ServerMessage{
					Type:    "dialout",
					Dialout: msg.Dialout,
				},
			}); err != nil {
				log.Printf("Error publishing dialout message %+v to room %s", msg.Dialout, roomId)
			}
		}
	default:
		log.Printf("Ignore unsupported internal message %+v from %s", msg, session.PublicId())
		return
	}
}

func isAllowedToUpdateTransientData(session Session) bool {
	if session.ClientType() == HelloClientTypeInternal {
		// Internal clients are always allowed.
		return true
	}

	if session.HasPermission(PERMISSION_TRANSIENT_DATA) {
		return true
	}

	return false
}

func (h *Hub) processTransientMsg(session Session, message *ClientMessage) {
	room := session.GetRoom()
	if room == nil {
		response := message.NewErrorServerMessage(NewError("not_in_room", "No room joined yet."))
		session.SendMessage(response)
		return
	}

	msg := message.TransientData
	switch msg.Type {
	case "set":
		if !isAllowedToUpdateTransientData(session) {
			sendNotAllowed(session, message, "Not allowed to update transient data.")
			return
		}

		if msg.Value == nil {
			room.SetTransientDataTTL(msg.Key, nil, msg.TTL)
		} else {
			room.SetTransientDataTTL(msg.Key, msg.Value, msg.TTL)
		}
	case "remove":
		if !isAllowedToUpdateTransientData(session) {
			sendNotAllowed(session, message, "Not allowed to update transient data.")
			return
		}

		room.RemoveTransientData(msg.Key)
	default:
		response := message.NewErrorServerMessage(NewError("ignored", "Unsupported message type."))
		session.SendMessage(response)
	}
}

func sendNotAllowed(session Session, message *ClientMessage, reason string) {
	response := message.NewErrorServerMessage(NewError("not_allowed", reason))
	session.SendMessage(response)
}

func sendMcuClientNotFound(session Session, message *ClientMessage) {
	response := message.NewErrorServerMessage(NewError("client_not_found", "No MCU client found to send message to."))
	session.SendMessage(response)
}

func sendMcuProcessingFailed(session Session, message *ClientMessage) {
	response := message.NewErrorServerMessage(NewError("processing_failed", "Processing of the message failed, please check server logs."))
	session.SendMessage(response)
}

func (h *Hub) isInSameCallRemote(ctx context.Context, senderSession *ClientSession, senderRoom *Room, recipientSessionId string) bool {
	clients := h.rpcClients.GetClients()
	if len(clients) == 0 {
		return false
	}

	var result atomic.Bool
	var wg sync.WaitGroup
	rpcCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, client := range clients {
		wg.Add(1)
		go func(client *GrpcClient) {
			defer wg.Done()

			inCall, err := client.IsSessionInCall(rpcCtx, recipientSessionId, senderRoom)
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				log.Printf("Error checking session %s in call on %s: %s", recipientSessionId, client.Target(), err)
				return
			} else if !inCall {
				return
			}

			cancel()
			result.Store(true)
		}(client)
	}
	wg.Wait()

	return result.Load()
}

func (h *Hub) isInSameCall(ctx context.Context, senderSession *ClientSession, recipientSessionId string) bool {
	if senderSession.ClientType() == HelloClientTypeInternal {
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
		(recipientSession.ClientType() != HelloClientTypeInternal && !recipientRoom.IsSessionInCall(recipientSession)) {
		// Recipient is not in a room, a different room or not in the call.
		return false
	}

	return true
}

func (h *Hub) processMcuMessage(session *ClientSession, client_message *ClientMessage, message *MessageClientMessage, data *MessageClientMessageData) {
	ctx, cancel := context.WithTimeout(session.Context(), h.mcuTimeout)
	defer cancel()

	var mc McuClient
	var err error
	var clientType string
	switch data.Type {
	case "requestoffer":
		if session.PublicId() == message.Recipient.SessionId {
			log.Printf("Not requesting offer from itself for session %s", session.PublicId())
			return
		}

		// A user is only allowed to subscribe a stream if she is in the same room
		// as the other user and both have their "inCall" flag set.
		if !h.allowSubscribeAnyStream && !h.isInSameCall(ctx, session, message.Recipient.SessionId) {
			log.Printf("Session %s is not in the same call as session %s, not requesting offer", session.PublicId(), message.Recipient.SessionId)
			sendNotAllowed(session, client_message, "Not allowed to request offer.")
			return
		}

		clientType = "subscriber"
		mc, err = session.GetOrCreateSubscriber(ctx, h.mcu, message.Recipient.SessionId, StreamType(data.RoomType))
	case "sendoffer":
		// Will be sent directly.
		return
	case "offer":
		clientType = "publisher"
		mc, err = session.GetOrCreatePublisher(ctx, h.mcu, StreamType(data.RoomType), data)
		if err, ok := err.(*PermissionError); ok {
			log.Printf("Session %s is not allowed to offer %s, ignoring (%s)", session.PublicId(), data.RoomType, err)
			sendNotAllowed(session, client_message, "Not allowed to publish.")
			return
		}
	case "selectStream":
		if session.PublicId() == message.Recipient.SessionId {
			log.Printf("Not selecting substream for own %s stream in session %s", data.RoomType, session.PublicId())
			return
		}

		clientType = "subscriber"
		mc = session.GetSubscriber(message.Recipient.SessionId, StreamType(data.RoomType))
	default:
		if session.PublicId() == message.Recipient.SessionId {
			if err := session.IsAllowedToSend(data); err != nil {
				log.Printf("Session %s is not allowed to send candidate for %s, ignoring (%s)", session.PublicId(), data.RoomType, err)
				sendNotAllowed(session, client_message, "Not allowed to send candidate.")
				return
			}

			clientType = "publisher"
			mc = session.GetPublisher(StreamType(data.RoomType))
		} else {
			clientType = "subscriber"
			mc = session.GetSubscriber(message.Recipient.SessionId, StreamType(data.RoomType))
		}
	}
	if err != nil {
		log.Printf("Could not create MCU %s for session %s to send %+v to %s: %s", clientType, session.PublicId(), data, message.Recipient.SessionId, err)
		sendMcuClientNotFound(session, client_message)
		return
	} else if mc == nil {
		log.Printf("No MCU %s found for session %s to send %+v to %s", clientType, session.PublicId(), data, message.Recipient.SessionId)
		sendMcuClientNotFound(session, client_message)
		return
	}

	mc.SendMessage(session.Context(), message, data, func(err error, response map[string]interface{}) {
		if err != nil {
			log.Printf("Could not send MCU message %+v for session %s to %s: %s", data, session.PublicId(), message.Recipient.SessionId, err)
			sendMcuProcessingFailed(session, client_message)
			return
		} else if response == nil {
			// No response received
			return
		}

		h.sendMcuMessageResponse(session, mc, message, data, response)
	})
}

func (h *Hub) sendMcuMessageResponse(session *ClientSession, mcuClient McuClient, message *MessageClientMessage, data *MessageClientMessageData, response map[string]interface{}) {
	var response_message *ServerMessage
	switch response["type"] {
	case "answer":
		answer_message := &AnswerOfferMessage{
			To:       session.PublicId(),
			From:     session.PublicId(),
			Type:     "answer",
			RoomType: data.RoomType,
			Payload:  response,
			Sid:      mcuClient.Sid(),
		}
		answer_data, err := json.Marshal(answer_message)
		if err != nil {
			log.Printf("Could not serialize answer %+v to %s: %s", answer_message, session.PublicId(), err)
			return
		}
		response_message = &ServerMessage{
			Type: "message",
			Message: &MessageServerMessage{
				Sender: &MessageServerMessageSender{
					Type:      "session",
					SessionId: session.PublicId(),
					UserId:    session.UserId(),
				},
				Data: answer_data,
			},
		}
	case "offer":
		offer_message := &AnswerOfferMessage{
			To:       session.PublicId(),
			From:     message.Recipient.SessionId,
			Type:     "offer",
			RoomType: data.RoomType,
			Payload:  response,
			Sid:      mcuClient.Sid(),
		}
		offer_data, err := json.Marshal(offer_message)
		if err != nil {
			log.Printf("Could not serialize offer %+v to %s: %s", offer_message, session.PublicId(), err)
			return
		}
		response_message = &ServerMessage{
			Type: "message",
			Message: &MessageServerMessage{
				Sender: &MessageServerMessageSender{
					Type:      "session",
					SessionId: message.Recipient.SessionId,
					// TODO(jojo): Set "UserId" field if known user.
				},
				Data: offer_data,
			},
		}
	default:
		log.Printf("Unsupported response %+v received to send to %s", response, session.PublicId())
		return
	}

	session.SendMessage(response_message)
}

func (h *Hub) processByeMsg(client HandlerClient, message *ClientMessage) {
	client.SendByeResponse(message)
	if session := h.processUnregister(client); session != nil {
		session.Close()
	}
}

func (h *Hub) processRoomUpdated(message *BackendServerRoomRequest) {
	room := message.room
	room.UpdateProperties(message.Update.Properties)
}

func (h *Hub) processRoomDeleted(message *BackendServerRoomRequest) {
	room := message.room
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

func (h *Hub) processRoomInCallChanged(message *BackendServerRoomRequest) {
	room := message.room
	if message.InCall.All {
		var flags int
		if err := json.Unmarshal(message.InCall.InCall, &flags); err != nil {
			var incall bool
			if err := json.Unmarshal(message.InCall.InCall, &incall); err != nil {
				log.Printf("Unsupported InCall flags type: %+v, ignoring", string(message.InCall.InCall))
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

func (h *Hub) processRoomParticipants(message *BackendServerRoomRequest) {
	room := message.room
	room.PublishUsersChanged(message.Participants.Changed, message.Participants.Users)
}

func (h *Hub) GetStats() map[string]interface{} {
	result := make(map[string]interface{})
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

func GetRealUserIP(r *http.Request, trusted *AllowedIps) string {
	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}

	ip := net.ParseIP(addr)
	if len(ip) == 0 {
		return addr
	}

	// Don't check any headers if the server can be reached by untrusted clients directly.
	if trusted == nil || !trusted.Allowed(ip) {
		return addr
	}

	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		if ip := net.ParseIP(realIP); len(ip) > 0 {
			return realIP
		}
	}

	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For#selecting_an_ip_address
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	if len(forwarded) > 0 {
		slices.Reverse(forwarded)
		var lastTrusted string
		for _, hop := range forwarded {
			hop = strings.TrimSpace(hop)
			// Make sure to remove any port.
			if host, _, err := net.SplitHostPort(hop); err == nil {
				hop = host
			}

			ip := net.ParseIP(hop)
			if len(ip) == 0 {
				continue
			}

			if trusted.Allowed(ip) {
				lastTrusted = hop
				continue
			}

			return hop
		}

		// If all entries in the "X-Forwarded-For" list are trusted, the left-most
		// will be the client IP. This can happen if a subnet is trusted and the
		// client also has an IP from this subnet.
		if lastTrusted != "" {
			return lastTrusted
		}
	}

	return addr
}

func (h *Hub) getRealUserIP(r *http.Request) string {
	return GetRealUserIP(r, h.trustedProxies.Load())
}

func (h *Hub) serveWs(w http.ResponseWriter, r *http.Request) {
	addr := h.getRealUserIP(r)
	agent := r.Header.Get("User-Agent")

	header := http.Header{}
	header.Set("Server", "nextcloud-spreed-signaling/"+h.version)
	header.Set("X-Spreed-Signaling-Features", strings.Join(h.info.Features, ", "))

	conn, err := h.upgrader.Upgrade(w, r, header)
	if err != nil {
		log.Printf("Could not upgrade request from %s: %s", addr, err)
		return
	}

	client, err := NewClient(r.Context(), conn, addr, agent, h)
	if err != nil {
		log.Printf("Could not create client for %s: %s", addr, err)
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

func (h *Hub) OnLookupCountry(client HandlerClient) string {
	ip := net.ParseIP(client.RemoteAddr())
	if ip == nil {
		return noCountry
	}

	if overrides := h.geoipOverrides.Load(); overrides != nil {
		for overrideNet, country := range *overrides {
			if overrideNet.Contains(ip) {
				return country
			}
		}
	}

	if ip.IsLoopback() {
		return loopback
	}

	country := unknownCountry
	if h.geoip != nil {
		var err error
		country, err = h.geoip.LookupCountry(ip)
		if err != nil {
			log.Printf("Could not lookup country for %s: %s", ip, err)
			return unknownCountry
		}

		if country == "" {
			country = unknownCountry
		}
	}
	return country
}

func (h *Hub) OnClosed(client HandlerClient) {
	h.processUnregister(client)
}

func (h *Hub) OnMessageReceived(client HandlerClient, data []byte) {
	h.processMessage(client, data)
}

func (h *Hub) OnRTTReceived(client HandlerClient, rtt time.Duration) {
	// Ignore
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
