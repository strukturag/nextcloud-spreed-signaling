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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/websocket"
)

var (
	DuplicateClient   = NewError("duplicate_client", "Client already registered.")
	HelloExpected     = NewError("hello_expected", "Expected Hello request.")
	UserAuthFailed    = NewError("auth_failed", "The user could not be authenticated.")
	RoomJoinFailed    = NewError("room_join_failed", "Could not join the room.")
	InvalidClientType = NewError("invalid_client_type", "The client type is not supported.")
	InvalidBackendUrl = NewError("invalid_backend", "The backend URL is not supported.")
	InvalidToken      = NewError("invalid_token", "The passed token is invalid.")
	NoSuchSession     = NewError("no_such_session", "The session to resume does not exist.")

	// Maximum number of concurrent requests to a backend.
	defaultMaxConcurrentRequestsPerHost = 8

	// Backend requests will be cancelled if they take too long.
	defaultBackendTimeoutSeconds = 10

	// MCU requests will be cancelled if they take too long.
	defaultMcuTimeoutSeconds = 10

	// New connections have to send a "Hello" request after 2 seconds.
	initialHelloTimeout = 2 * time.Second

	// Anonymous clients have to join a room after 10 seconds.
	anonmyousJoinRoomTimeout = 10 * time.Second

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
)

const (
	privateSessionName = "private-session"
	publicSessionName  = "public-session"
)

type Hub struct {
	nats         NatsClient
	upgrader     websocket.Upgrader
	cookie       *securecookie.SecureCookie
	info         *HelloServerMessageServer
	infoInternal *HelloServerMessageServer

	stopped  int32
	stopChan chan bool

	roomUpdated      chan *BackendServerRoomRequest
	roomDeleted      chan *BackendServerRoomRequest
	roomInCall       chan *BackendServerRoomRequest
	roomParticipants chan *BackendServerRoomRequest

	mu sync.RWMutex
	ru sync.RWMutex

	sid      uint64
	clients  map[uint64]*Client
	sessions map[uint64]Session
	rooms    map[string]*Room

	roomSessions    RoomSessions
	virtualSessions map[string]uint64

	decodeCaches []*LruCache

	mcu                   Mcu
	mcuTimeout            time.Duration
	internalClientsSecret []byte

	expiredSessions    map[Session]bool
	expectHelloClients map[*Client]time.Time
	anonymousClients   map[*Client]time.Time

	backendTimeout time.Duration
	backend        *BackendClient

	geoip          *GeoLookup
	geoipOverrides map[*net.IPNet]string
	geoipUpdating  int32
}

func NewHub(config *goconf.ConfigFile, nats NatsClient, r *mux.Router, version string) (*Hub, error) {
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

	internalClientsSecret, _ := config.GetString("clients", "internalsecret")
	if internalClientsSecret == "" {
		log.Println("WARNING: No shared secret has been set for internal clients.")
	}

	maxConcurrentRequestsPerHost, _ := config.GetInt("backend", "connectionsperhost")
	if maxConcurrentRequestsPerHost <= 0 {
		maxConcurrentRequestsPerHost = defaultMaxConcurrentRequestsPerHost
	}

	backend, err := NewBackendClient(config, maxConcurrentRequestsPerHost, version)
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

	decodeCaches := make([]*LruCache, 0, numDecodeCaches)
	for i := 0; i < numDecodeCaches; i++ {
		decodeCaches = append(decodeCaches, NewLruCache(decodeCacheSize))
	}

	roomSessions, err := NewBuiltinRoomSessions()
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
	var geoipOverrides map[*net.IPNet]string
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

		if options, _ := config.GetOptions("geoip-overrides"); len(options) > 0 {
			geoipOverrides = make(map[*net.IPNet]string)
			for _, option := range options {
				var ip net.IP
				var ipNet *net.IPNet
				if strings.Contains(option, "/") {
					ip, ipNet, err = net.ParseCIDR(option)
					if err != nil {
						return nil, fmt.Errorf("Could not parse CIDR %s: %s", option, err)
					}
				} else {
					ip = net.ParseIP(option)
					if ip == nil {
						return nil, fmt.Errorf("Could not parse IP %s", option)
					}

					var mask net.IPMask
					if ipv4 := ip.To4(); ipv4 != nil {
						mask = net.CIDRMask(32, 32)
					} else {
						mask = net.CIDRMask(128, 128)
					}
					ipNet = &net.IPNet{
						IP:   ip,
						Mask: mask,
					}
				}

				value, _ := config.GetString("geoip-overrides", option)
				value = strings.ToUpper(strings.TrimSpace(value))
				if value == "" {
					log.Printf("IP %s doesn't have a country assigned, skipping", option)
					continue
				} else if !IsValidCountry(value) {
					log.Printf("Country %s for IP %s is invalid, skipping", value, option)
					continue
				}

				log.Printf("Using country %s for %s", value, ipNet)
				geoipOverrides[ipNet] = value
			}
		}
	} else {
		log.Printf("Not using GeoIP database")
	}

	hub := &Hub{
		nats: nats,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  websocketReadBufferSize,
			WriteBufferSize: websocketWriteBufferSize,
		},
		cookie: securecookie.New([]byte(hashKey), blockBytes).MaxAge(0),
		info: &HelloServerMessageServer{
			Version:  version,
			Features: DefaultFeatures,
		},
		infoInternal: &HelloServerMessageServer{
			Version:  version,
			Features: DefaultFeaturesInternal,
		},

		stopChan: make(chan bool),

		roomUpdated:      make(chan *BackendServerRoomRequest),
		roomDeleted:      make(chan *BackendServerRoomRequest),
		roomInCall:       make(chan *BackendServerRoomRequest),
		roomParticipants: make(chan *BackendServerRoomRequest),

		clients:  make(map[uint64]*Client),
		sessions: make(map[uint64]Session),
		rooms:    make(map[string]*Room),

		roomSessions:    roomSessions,
		virtualSessions: make(map[string]uint64),

		decodeCaches: decodeCaches,

		mcuTimeout:            mcuTimeout,
		internalClientsSecret: []byte(internalClientsSecret),

		expiredSessions:    make(map[Session]bool),
		anonymousClients:   make(map[*Client]time.Time),
		expectHelloClients: make(map[*Client]time.Time),

		backendTimeout: backendTimeout,
		backend:        backend,

		geoip:          geoip,
		geoipOverrides: geoipOverrides,
	}
	hub.upgrader.CheckOrigin = hub.checkOrigin
	r.HandleFunc("/spreed", func(w http.ResponseWriter, r *http.Request) {
		hub.serveWs(w, r)
	})

	return hub, nil
}

func addFeature(msg *HelloServerMessageServer, feature string) {
	var newFeatures []string
	added := false
	for _, f := range msg.Features {
		newFeatures = append(newFeatures, f)
		if f == feature {
			added = true
		}
	}
	if !added {
		newFeatures = append(newFeatures, feature)
	}
	msg.Features = newFeatures
}

func removeFeature(msg *HelloServerMessageServer, feature string) {
	var newFeatures []string
	for _, f := range msg.Features {
		if f != feature {
			newFeatures = append(newFeatures, f)
		}
	}
	msg.Features = newFeatures
}

func (h *Hub) SetMcu(mcu Mcu) {
	h.mcu = mcu
	if mcu == nil {
		removeFeature(h.info, ServerFeatureMcu)
		removeFeature(h.infoInternal, ServerFeatureMcu)
	} else {
		log.Printf("Using a timeout of %s for MCU requests", h.mcuTimeout)
		addFeature(h.info, ServerFeatureMcu)
		addFeature(h.infoInternal, ServerFeatureMcu)
	}
}

func (h *Hub) checkOrigin(r *http.Request) bool {
	// We allow any Origin to connect to the service.
	return true
}

func (h *Hub) GetServerInfo(session Session) *HelloServerMessageServer {
	if session.ClientType() == HelloClientTypeInternal {
		return h.infoInternal
	}

	return h.info
}

func (h *Hub) updateGeoDatabase() {
	if h.geoip == nil {
		return
	}

	if !atomic.CompareAndSwapInt32(&h.geoipUpdating, 0, 1) {
		// Already updating
		return
	}

	defer atomic.CompareAndSwapInt32(&h.geoipUpdating, 1, 0)
	delay := time.Second
	for atomic.LoadInt32(&h.stopped) == 0 {
		err := h.geoip.Update()
		if err == nil {
			break
		}

		log.Printf("Could not update GeoIP database, will retry later (%s)", err)
		time.Sleep(delay)
		delay = delay * 2
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
}

func (h *Hub) Run() {
	go h.updateGeoDatabase()

	housekeeping := time.NewTicker(housekeepingInterval)
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
		case <-h.stopChan:
			break loop
		}
	}
	if h.geoip != nil {
		h.geoip.Close()
	}
}

func (h *Hub) Stop() {
	atomic.StoreInt32(&h.stopped, 1)
	select {
	case h.stopChan <- true:
	default:
	}
}

func (h *Hub) Reload(config *goconf.ConfigFile) {
	if h.mcu != nil {
		h.mcu.Reload(config)
	}
	h.backend.Reload(config)
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
	hash.Write([]byte(cache_key))
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

	h.mu.Lock()
	session := h.sessions[data.Sid]
	h.mu.Unlock()
	return session
}

func (h *Hub) checkExpiredSessions(now time.Time) {
	for s := range h.expiredSessions {
		if s.IsExpired(now) {
			h.mu.Unlock()
			log.Printf("Closing expired session %s (private=%s)", s.PublicId(), s.PrivateId())
			s.Close()
			h.mu.Lock()
			// Should already be deleted by the close code, but better be sure.
			delete(h.expiredSessions, s)
		}
	}
}

func (h *Hub) checkExpireClients(now time.Time, clients map[*Client]time.Time, reason string) {
	for client, timeout := range clients {
		if now.After(timeout) {
			// This will close the client connection.
			h.mu.Unlock()
			client.SendByeResponseWithReason(nil, reason)
			if reason == "room_join_timeout" {
				session := client.GetSession()
				if session != nil {
					session.Close()
				}
			}
			h.mu.Lock()
		}
	}
}

func (h *Hub) checkAnonymousClients(now time.Time) {
	h.checkExpireClients(now, h.anonymousClients, "room_join_timeout")
}

func (h *Hub) checkInitialHello(now time.Time) {
	h.checkExpireClients(now, h.expectHelloClients, "hello_timeout")
}

func (h *Hub) performHousekeeping(now time.Time) {
	h.mu.Lock()
	h.checkExpiredSessions(now)
	h.checkAnonymousClients(now)
	h.checkInitialHello(now)
	h.mu.Unlock()
}

func (h *Hub) removeSession(session Session) {
	session.LeaveRoom(true)
	h.invalidateSessionId(session.PrivateId(), privateSessionName)
	h.invalidateSessionId(session.PublicId(), publicSessionName)

	h.mu.Lock()
	if data := session.Data(); data != nil && data.Sid > 0 {
		delete(h.clients, data.Sid)
		delete(h.sessions, data.Sid)
	}
	delete(h.expiredSessions, session)
	h.mu.Unlock()
}

func (h *Hub) startWaitAnonymousClientRoom(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.startWaitAnonymousClientRoomLocked(client)
}

func (h *Hub) startWaitAnonymousClientRoomLocked(client *Client) {
	if !client.IsConnected() {
		return
	}
	if session := client.GetSession(); session != nil && session.ClientType() == HelloClientTypeInternal {
		// Internal clients don't need to join a room.
		return
	}

	// Anonymous clients must join a public room within a given time,
	// otherwise they get disconnected to avoid blocking resources forever.
	now := time.Now()
	h.anonymousClients[client] = now.Add(anonmyousJoinRoomTimeout)
}

func (h *Hub) startExpectHello(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !client.IsConnected() {
		return
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.IsAuthenticated() {
		return
	}

	// Clients must send a "Hello" request to get a session within a given time.
	now := time.Now()
	h.expectHelloClients[client] = now.Add(initialHelloTimeout)
}

func (h *Hub) processNewClient(client *Client) {
	h.startExpectHello(client)
}

func (h *Hub) newSessionIdData(backend *Backend) *SessionIdData {
	sid := atomic.AddUint64(&h.sid, 1)
	for sid == 0 {
		sid = atomic.AddUint64(&h.sid, 1)
	}
	sessionIdData := &SessionIdData{
		Sid:       sid,
		Created:   time.Now(),
		BackendId: backend.Id(),
	}
	return sessionIdData
}

func (h *Hub) processRegister(client *Client, message *ClientMessage, backend *Backend, auth *BackendClientResponse) {
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

	sid := atomic.AddUint64(&h.sid, 1)
	for sid == 0 {
		sid = atomic.AddUint64(&h.sid, 1)
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
	if userId == "" && auth.Type != HelloClientTypeInternal {
		h.startWaitAnonymousClientRoomLocked(client)
	}
	h.mu.Unlock()

	h.setDecodedSessionId(privateSessionId, privateSessionName, sessionIdData)
	h.setDecodedSessionId(publicSessionId, publicSessionName, sessionIdData)
	h.sendHelloResponse(client, message, session)
}

func (h *Hub) processUnregister(client *Client) *ClientSession {
	session := client.GetSession()

	h.mu.Lock()
	delete(h.anonymousClients, client)
	delete(h.expectHelloClients, client)
	if session != nil {
		delete(h.clients, session.Data().Sid)
		session.StartExpire()
	}
	h.mu.Unlock()
	if session != nil {
		log.Printf("Unregister %s (private=%s)", session.PublicId(), session.PrivateId())
		session.ClearClient(client)
	}

	client.Close()
	return session
}

func (h *Hub) processMessage(client *Client, data []byte) {
	var message ClientMessage
	if err := message.UnmarshalJSON(data); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Error decoding message from client %s: %v", session.PublicId(), err)
		} else {
			log.Printf("Error decoding message from %s: %v", client.RemoteAddr(), err)
		}
		client.SendError(InvalidFormat)
		return
	}

	if err := message.CheckValid(); err != nil {
		if session := client.GetSession(); session != nil {
			log.Printf("Invalid message %+v from client %s: %v", message, session.PublicId(), err)
		} else {
			log.Printf("Invalid message %+v from %s: %v", message, client.RemoteAddr(), err)
		}
		client.SendMessage(message.NewErrorServerMessage(InvalidFormat))
		return
	}

	session := client.GetSession()
	if session == nil {
		if message.Type != "hello" {
			client.SendMessage(message.NewErrorServerMessage(HelloExpected))
			return
		}

		h.processHello(client, &message)
		return
	}

	switch message.Type {
	case "room":
		h.processRoom(client, &message)
	case "message":
		h.processMessageMsg(client, &message)
	case "control":
		h.processControlMsg(client, &message)
	case "internal":
		h.processInternalMsg(client, &message)
	case "bye":
		h.processByeMsg(client, &message)
	case "hello":
		log.Printf("Ignore hello %+v for already authenticated connection %s", message.Hello, session.PublicId())
	default:
		log.Printf("Ignore unknown message %+v from %s", message, session.PublicId())
	}
}

func (h *Hub) sendHelloResponse(client *Client, message *ClientMessage, session *ClientSession) bool {
	response := &ServerMessage{
		Id:   message.Id,
		Type: "hello",
		Hello: &HelloServerMessage{
			Version:   HelloVersion,
			SessionId: session.PublicId(),
			ResumeId:  session.PrivateId(),
			UserId:    session.UserId(),
			Server:    h.GetServerInfo(session),
		},
	}
	return client.SendMessage(response)
}

func (h *Hub) processHello(client *Client, message *ClientMessage) {
	resumeId := message.Hello.ResumeId
	if resumeId != "" {
		data := h.decodeSessionId(resumeId, privateSessionName)
		if data == nil {
			client.SendMessage(message.NewErrorServerMessage(NoSuchSession))
			return
		}

		h.mu.Lock()
		session, found := h.sessions[data.Sid]
		if !found || resumeId != session.PrivateId() {
			h.mu.Unlock()
			client.SendMessage(message.NewErrorServerMessage(NoSuchSession))
			return
		}

		clientSession, ok := session.(*ClientSession)
		if !ok {
			// Should never happen as clients only can resume their own sessions.
			h.mu.Unlock()
			log.Printf("Client resumed non-client session %s (private=%s)", session.PublicId(), session.PrivateId())
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

		clientSession.StopExpire()
		h.clients[data.Sid] = client
		delete(h.expectHelloClients, client)
		h.mu.Unlock()

		log.Printf("Resume session from %s in %s (%s) %s (private=%s)", client.RemoteAddr(), client.Country(), client.UserAgent(), session.PublicId(), session.PrivateId())

		h.sendHelloResponse(client, message, clientSession)
		clientSession.NotifySessionResumed(client)
		return
	}

	// Make sure client doesn't get disconnected while calling auth backend.
	h.mu.Lock()
	delete(h.expectHelloClients, client)
	h.mu.Unlock()

	switch message.Hello.Auth.Type {
	case HelloClientTypeClient:
		h.processHelloClient(client, message)
	case HelloClientTypeInternal:
		h.processHelloInternal(client, message)
	default:
		h.startExpectHello(client)
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
	}
}

func (h *Hub) processHelloClient(client *Client, message *ClientMessage) {
	// Make sure the client must send another "hello" in case of errors.
	defer h.startExpectHello(client)

	url := message.Hello.Auth.parsedUrl
	backend := h.backend.GetBackend(url)
	if backend == nil {
		client.SendMessage(message.NewErrorServerMessage(InvalidBackendUrl))
		return
	}

	// Run in timeout context to prevent blocking too long.
	ctx, cancel := context.WithTimeout(context.Background(), h.backendTimeout)
	defer cancel()

	request := NewBackendClientAuthRequest(message.Hello.Auth.Params)
	var auth BackendClientResponse
	if err := h.backend.PerformJSONRequest(ctx, url, request, &auth); err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		return
	}

	// TODO(jojo): Validate response

	h.processRegister(client, message, backend, &auth)
}

func (h *Hub) processHelloInternal(client *Client, message *ClientMessage) {
	defer h.startExpectHello(client)
	if len(h.internalClientsSecret) == 0 {
		client.SendMessage(message.NewErrorServerMessage(InvalidClientType))
		return
	}

	// Validate internal connection.
	rnd := message.Hello.Auth.internalParams.Random
	mac := hmac.New(sha256.New, h.internalClientsSecret)
	mac.Write([]byte(rnd))
	check := hex.EncodeToString(mac.Sum(nil))
	if len(rnd) < minTokenRandomLength || check != message.Hello.Auth.internalParams.Token {
		client.SendMessage(message.NewErrorServerMessage(InvalidToken))
		return
	}

	backend := h.backend.GetBackend(message.Hello.Auth.internalParams.parsedBackend)
	if backend == nil {
		client.SendMessage(message.NewErrorServerMessage(InvalidBackendUrl))
		return
	}

	auth := &BackendClientResponse{
		Type: "auth",
		Auth: &BackendClientAuthResponse{},
	}
	h.processRegister(client, message, backend, auth)
}

func (h *Hub) disconnectByRoomSessionId(roomSessionId string) {
	sessionId, err := h.roomSessions.GetSessionId(roomSessionId)
	if err == ErrNoSuchRoomSession {
		return
	} else if err != nil {
		log.Printf("Could not get session id for room session %s: %s", roomSessionId, err)
		return
	}

	session := h.GetSessionByPublicId(sessionId)
	if session == nil {
		// Session is located on a different server.
		msg := &ServerMessage{
			Type: "bye",
			Bye: &ByeServerMessage{
				Reason: "room_session_reconnected",
			},
		}
		h.nats.PublishMessage("session."+sessionId, msg)
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

func (h *Hub) sendRoom(client *Client, message *ClientMessage, room *Room) bool {
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
	return client.SendMessage(response)
}

func (h *Hub) processRoom(client *Client, message *ClientMessage) {
	session := client.GetSession()
	roomId := message.Room.RoomId
	if roomId == "" {
		if session == nil {
			return
		}

		// We can handle leaving a room directly.
		if session.LeaveRoom(true) != nil {
			// User was in a room before, so need to notify about leaving it.
			h.sendRoom(client, message, nil)
		}
		if session.UserId() == "" && session.ClientType() != HelloClientTypeInternal {
			h.startWaitAnonymousClientRoom(client)
		}
		return
	}

	if session != nil {
		if room := h.getRoomForBackend(roomId, session.Backend()); room != nil && room.HasSession(session) {
			// Session already is in that room, no action needed.
			return
		}
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
		ctx, cancel := context.WithTimeout(context.Background(), h.backendTimeout)
		defer cancel()

		sessionId := message.Room.SessionId
		if sessionId == "" {
			// TODO(jojo): Better make the session id required in the request.
			log.Printf("User did not send a room session id, assuming session %s", session.PublicId())
			sessionId = session.PublicId()
		}
		request := NewBackendClientRoomRequest(roomId, session.UserId(), sessionId)
		if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &room); err != nil {
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
			return
		}

		// TODO(jojo): Validate response

		if message.Room.SessionId != "" {
			// There can only be one connection per Nextcloud Talk session,
			// disconnect any other connections without sending a "leave" event.
			h.disconnectByRoomSessionId(message.Room.SessionId)
		}
	}

	h.processJoinRoom(client, message, &room)
}

func (h *Hub) getRoomForBackend(id string, backend *Backend) *Room {
	internalRoomId := getRoomIdForBackend(id, backend)

	h.ru.RLock()
	defer h.ru.RUnlock()
	return h.rooms[internalRoomId]
}

func (h *Hub) getRoom(id string) *Room {
	h.ru.RLock()
	defer h.ru.RUnlock()
	// TODO: The same room might exist on different backends.
	for _, room := range h.rooms {
		if room.Id() == id {
			return room
		}
	}

	return nil
}

func (h *Hub) removeRoom(room *Room) {
	internalRoomId := getRoomIdForBackend(room.Id(), room.Backend())
	h.ru.Lock()
	delete(h.rooms, internalRoomId)
	h.ru.Unlock()
}

func (h *Hub) createRoom(id string, properties *json.RawMessage, backend *Backend) (*Room, error) {
	// Note the write lock must be held.
	room, err := NewRoom(id, properties, h, h.nats, backend)
	if err != nil {
		return nil, err
	}

	internalRoomId := getRoomIdForBackend(id, backend)
	h.rooms[internalRoomId] = room
	return room, nil
}

func (h *Hub) processJoinRoom(client *Client, message *ClientMessage, room *BackendClientResponse) {
	session := client.GetSession()
	if session == nil {
		// Client disconnected while waiting for join room response.
		return
	}

	if room.Type == "error" {
		client.SendMessage(message.NewErrorServerMessage(room.Error))
		return
	} else if room.Type != "room" {
		client.SendMessage(message.NewErrorServerMessage(RoomJoinFailed))
		return
	}

	session.LeaveRoom(true)

	roomId := room.Room.RoomId
	internalRoomId := getRoomIdForBackend(roomId, session.Backend())
	if err := session.SubscribeRoomNats(h.nats, roomId, message.Room.SessionId); err != nil {
		client.SendMessage(message.NewWrappedErrorServerMessage(err))
		// The client (implicitly) left the room due to an error.
		h.sendRoom(client, nil, nil)
		return
	}

	h.ru.Lock()
	r, found := h.rooms[internalRoomId]
	if !found {
		var err error
		if r, err = h.createRoom(roomId, room.Room.Properties, session.Backend()); err != nil {
			h.ru.Unlock()
			client.SendMessage(message.NewWrappedErrorServerMessage(err))
			// The client (implicitly) left the room due to an error.
			session.UnsubscribeRoomNats()
			h.sendRoom(client, nil, nil)
			return
		}
	}
	h.ru.Unlock()

	h.mu.Lock()
	// The client now joined a room, don't expire him if he is anonymous.
	delete(h.anonymousClients, client)
	h.mu.Unlock()
	session.SetRoom(r)
	if room.Room.Permissions != nil {
		session.SetPermissions(*room.Room.Permissions)
	}
	h.sendRoom(client, message, r)
	h.notifyUserJoinedRoom(r, client, session, room.Room.Session)
}

func (h *Hub) notifyUserJoinedRoom(room *Room, client *Client, session Session, sessionData *json.RawMessage) {
	// Register session with the room
	if sessions := room.AddSession(session, sessionData); len(sessions) > 0 {
		events := make([]*EventServerMessageSessionEntry, 0, len(sessions))
		for _, s := range sessions {
			events = append(events, &EventServerMessageSessionEntry{
				SessionId: s.PublicId(),
				UserId:    s.UserId(),
				User:      s.UserData(),
			})
		}
		msg := &ServerMessage{
			Type: "event",
			Event: &EventServerMessage{
				Target: "room",
				Type:   "join",
				Join:   events,
			},
		}

		// No need to send through NATS, the session is connected locally.
		client.SendMessage(msg)
	}
}

func (h *Hub) processMessageMsg(client *Client, message *ClientMessage) {
	msg := message.Message
	session := client.GetSession()
	if session == nil {
		// Client is not connected yet.
		return
	}

	var recipient *Client
	var subject string
	var clientData *MessageClientMessageData
	var serverRecipient *MessageClientMessageRecipient
	switch msg.Recipient.Type {
	case RecipientTypeSession:
		data := h.decodeSessionId(msg.Recipient.SessionId, publicSessionName)
		if data != nil {
			if data.BackendId != session.Backend().Id() {
				// Clients are only allowed to send to sessions from the same backend.
				return
			}

			if h.mcu != nil {
				// Maybe this is a message to be processed by the MCU.
				var data MessageClientMessageData
				if err := json.Unmarshal(*msg.Data, &data); err == nil {
					clientData = &data
					switch data.Type {
					case "requestoffer":
						// Process asynchronously to avoid blocking regular
						// message processing for this client.
						go h.processMcuMessage(client, client, session, message, msg, &data)
						return
					case "offer":
						fallthrough
					case "answer":
						fallthrough
					case "endOfCandidates":
						fallthrough
					case "candidate":
						h.processMcuMessage(client, client, session, message, msg, &data)
						return
					}
				}
			}

			if msg.Recipient.SessionId == session.PublicId() {
				// Don't loop messages to the sender.
				return
			}

			subject = "session." + msg.Recipient.SessionId
			h.mu.RLock()
			recipient = h.clients[data.Sid]
			if recipient == nil {
				// Send to client connection for virtual sessions.
				sess := h.sessions[data.Sid]
				if sess != nil && sess.ClientType() == HelloClientTypeVirtual {
					virtualSession := sess.(*VirtualSession)
					clientSession := virtualSession.Session()
					subject = "session." + clientSession.PublicId()
					recipient = clientSession.GetClient()
					// The client should see his session id as recipient.
					serverRecipient = &MessageClientMessageRecipient{
						Type:      "session",
						SessionId: virtualSession.SessionId(),
					}
				}
			}
			h.mu.RUnlock()
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
			if room := session.GetRoom(); room != nil {
				subject = GetSubjectForRoomId(room.Id(), room.Backend())

				if h.mcu != nil {
					var data MessageClientMessageData
					if err := json.Unmarshal(*msg.Data, &data); err == nil {
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

	if clientData != nil && clientData.Type == "unshareScreen" {
		// User is stopping to share his screen. Firefox doesn't properly clean
		// up the peer connections in all cases, so make sure to stop publishing
		// in the MCU.
		go func(c *Client) {
			time.Sleep(cleanupScreenPublisherDelay)
			session := c.GetSession()
			if session == nil {
				return
			}

			publisher := session.GetPublisher(streamTypeScreen)
			if publisher == nil {
				return
			}

			log.Printf("Closing screen publisher for %s\n", session.PublicId())
			ctx, cancel := context.WithTimeout(context.Background(), h.mcuTimeout)
			defer cancel()
			publisher.Close(ctx)
		}(client)
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
		// The recipient is connected to this instance, no need to go through NATS.
		if clientData != nil && clientData.Type == "sendoffer" {
			if !isAllowedToSend(session, clientData) {
				log.Printf("Session %s is not allowed to send offer for %s, ignoring", session.PublicId(), clientData.RoomType)
				sendNotAllowed(client, message)
				return
			}

			if recipientSession := recipient.GetSession(); recipientSession != nil {
				msg.Recipient.SessionId = session.PublicId()
				// It may take some time for the publisher (which is the current
				// client) to start his stream, so we must not block the active
				// goroutine.
				go h.processMcuMessage(client, recipient, recipientSession, message, msg, clientData)
			} else {
				// Client is not connected yet.
			}
			return
		}
		recipient.SendMessage(response)
	} else {
		if clientData != nil && clientData.Type == "sendoffer" {
			// TODO(jojo): Implement this.
			log.Printf("Sending offers to remote clients is not supported yet (client %s)", session.PublicId())
			return
		}
		h.nats.PublishMessage(subject, response)
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

func (h *Hub) processControlMsg(client *Client, message *ClientMessage) {
	msg := message.Control
	session := client.GetSession()
	if session == nil {
		// Client is not connected yet.
		return
	} else if !isAllowedToControl(session) {
		log.Printf("Ignore control message %+v from %s", msg, session.PublicId())
		return
	}

	var recipient *Client
	var subject string
	var serverRecipient *MessageClientMessageRecipient
	switch msg.Recipient.Type {
	case RecipientTypeSession:
		data := h.decodeSessionId(msg.Recipient.SessionId, publicSessionName)
		if data != nil {
			if msg.Recipient.SessionId == session.PublicId() {
				// Don't loop messages to the sender.
				return
			}

			subject = "session." + msg.Recipient.SessionId
			h.mu.RLock()
			recipient = h.clients[data.Sid]
			if recipient == nil {
				// Send to client connection for virtual sessions.
				sess := h.sessions[data.Sid]
				if sess != nil && sess.ClientType() == HelloClientTypeVirtual {
					virtualSession := sess.(*VirtualSession)
					clientSession := virtualSession.Session()
					subject = "session." + clientSession.PublicId()
					recipient = clientSession.GetClient()
					// The client should see his session id as recipient.
					serverRecipient = &MessageClientMessageRecipient{
						Type:      "session",
						SessionId: virtualSession.SessionId(),
					}
				}
			}
			h.mu.RUnlock()
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
			if room := session.GetRoom(); room != nil {
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
		h.nats.PublishMessage(subject, response)
	}
}

func (h *Hub) processInternalMsg(client *Client, message *ClientMessage) {
	msg := message.Internal
	session := client.GetSession()
	if session == nil {
		// Client is not connected yet.
		return
	} else if session.ClientType() != HelloClientTypeInternal {
		log.Printf("Ignore internal message %+v from %s", msg, session.PublicId())
		return
	}

	switch msg.Type {
	case "addsession":
		msg := msg.AddSession
		room := h.getRoom(msg.RoomId)
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

		ctx, cancel := context.WithTimeout(context.Background(), h.backendTimeout)
		defer cancel()

		virtualSessionId := GetVirtualSessionId(session, msg.SessionId)

		if msg.Options != nil {
			request := NewBackendClientRoomRequest(room.Id(), msg.UserId, publicSessionId)
			request.Room.ActorId = msg.Options.ActorId
			request.Room.ActorType = msg.Options.ActorType
			request.Room.InCall = FlagInCall | FlagWithPhone

			var response BackendClientResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &response); err != nil {
				log.Printf("Could not join virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(NewError("add_failed", "Could not join virtual session."))
				client.SendMessage(reply)
				return
			}

			if response.Type == "error" {
				log.Printf("Could not join virtual session %s at backend %s: %+v", virtualSessionId, session.BackendUrl(), response.Error)
				reply := message.NewErrorServerMessage(NewError("add_failed", response.Error.Error()))
				client.SendMessage(reply)
				return
			}
		} else {
			request := NewBackendClientSessionRequest(room.Id(), "add", publicSessionId, msg)
			var response BackendClientSessionResponse
			if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &response); err != nil {
				log.Printf("Could not add virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
				reply := message.NewErrorServerMessage(NewError("add_failed", "Could not add virtual session."))
				client.SendMessage(reply)
				return
			}
		}

		sess := NewVirtualSession(session, privateSessionId, publicSessionId, sessionIdData, msg)
		h.mu.Lock()
		h.sessions[sessionIdData.Sid] = sess
		h.virtualSessions[virtualSessionId] = sessionIdData.Sid
		h.mu.Unlock()
		log.Printf("Session %s added virtual session %s with initial flags %d", session.PublicId(), sess.PublicId(), sess.Flags())
		session.AddVirtualSession(sess)
		sess.SetRoom(room)
		room.AddSession(sess, nil)
	case "updatesession":
		msg := msg.UpdateSession
		room := h.getRoom(msg.RoomId)
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
			update := false
			if virtualSession, ok := sess.(*VirtualSession); ok {
				if msg.Flags != nil {
					if virtualSession.SetFlags(*msg.Flags) {
						update = true
					}
				}
			} else {
				log.Printf("Ignore update request for non-virtual session %s", sess.PublicId())
			}
			if update {
				room.NotifySessionChanged(sess)
			}
		}
	case "removesession":
		msg := msg.RemoveSession
		room := h.getRoom(msg.RoomId)
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
			sess.Close()

			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), h.backendTimeout)
				defer cancel()

				var options *AddSessionOptions
				if vsess, ok := sess.(*VirtualSession); ok {
					options = vsess.Options()
				}
				if options != nil {
					request := NewBackendClientRoomRequest(room.Id(), sess.UserId(), sess.PublicId())
					request.Room.Action = "leave"
					if options != nil {
						request.Room.ActorId = options.ActorId
						request.Room.ActorType = options.ActorType
					}

					var response BackendClientResponse
					if err := h.backend.PerformJSONRequest(ctx, session.ParsedBackendUrl(), request, &response); err != nil {
						log.Printf("Could not leave virtual session %s at backend %s: %s", virtualSessionId, session.BackendUrl(), err)
						reply := message.NewErrorServerMessage(NewError("add_failed", "Could not join virtual session."))
						client.SendMessage(reply)
						return
					}

					if response.Type == "error" {
						log.Printf("Could not leave virtual session %s at backend %s: %+v", virtualSessionId, session.BackendUrl(), response.Error)
						reply := message.NewErrorServerMessage(NewError("add_failed", response.Error.Error()))
						client.SendMessage(reply)
						return
					}
				} else {
					request := NewBackendClientSessionRequest(room.Id(), "remove", sess.PublicId(), nil)
					var response BackendClientSessionResponse
					err := h.backend.PerformJSONRequest(ctx, sess.ParsedBackendUrl(), request, &response)
					if err != nil {
						log.Printf("Could not remove virtual session %s from backend %s: %s", sess.PublicId(), sess.BackendUrl(), err)
						reply := message.NewErrorServerMessage(NewError("remove_failed", "Could not remove virtual session from backend."))
						client.SendMessage(reply)
					}
				}
			}()
		}
	default:
		log.Printf("Ignore unsupported internal message %+v from %s", msg, session.PublicId())
		return
	}
}

func isAllowedToSend(session *ClientSession, data *MessageClientMessageData) bool {
	var permission Permission
	if data.RoomType == "screen" {
		permission = PERMISSION_MAY_PUBLISH_SCREEN
	} else {
		permission = PERMISSION_MAY_PUBLISH_MEDIA
	}
	return session.HasPermission(permission)
}

func sendNotAllowed(client *Client, message *ClientMessage) {
	response := message.NewErrorServerMessage(NewError("not_allowed", "Not allowed to publish."))
	client.SendMessage(response)
}

func sendMcuClientNotFound(client *Client, message *ClientMessage) {
	response := message.NewErrorServerMessage(NewError("client_not_found", "No MCU client found to send message to."))
	client.SendMessage(response)
}

func sendMcuProcessingFailed(client *Client, message *ClientMessage) {
	response := message.NewErrorServerMessage(NewError("processing_failed", "Processing of the message failed, please check server logs."))
	client.SendMessage(response)
}

func (h *Hub) processMcuMessage(senderClient *Client, client *Client, session *ClientSession, client_message *ClientMessage, message *MessageClientMessage, data *MessageClientMessageData) {
	ctx, cancel := context.WithTimeout(context.Background(), h.mcuTimeout)
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

		clientType = "subscriber"
		mc, err = session.GetOrCreateSubscriber(ctx, h.mcu, message.Recipient.SessionId, data.RoomType)
	case "sendoffer":
		// Permissions have already been checked in "processMessageMsg".
		clientType = "subscriber"
		mc, err = session.GetOrCreateSubscriber(ctx, h.mcu, message.Recipient.SessionId, data.RoomType)
	case "offer":
		if !isAllowedToSend(session, data) {
			log.Printf("Session %s is not allowed to offer %s, ignoring", session.PublicId(), data.RoomType)
			sendNotAllowed(senderClient, client_message)
			return
		}

		clientType = "publisher"
		mc, err = session.GetOrCreatePublisher(ctx, h.mcu, data.RoomType)
	default:
		if session.PublicId() == message.Recipient.SessionId {
			if !isAllowedToSend(session, data) {
				log.Printf("Session %s is not allowed to send candidate for %s, ignoring", session.PublicId(), data.RoomType)
				sendNotAllowed(senderClient, client_message)
				return
			}

			clientType = "publisher"
			mc = session.GetPublisher(data.RoomType)
		} else {
			clientType = "subscriber"
			mc = session.GetSubscriber(message.Recipient.SessionId, data.RoomType)
		}
	}
	if err != nil {
		log.Printf("Could not create MCU %s for session %s to send %+v to %s: %s", clientType, session.PublicId(), data, message.Recipient.SessionId, err)
		sendMcuClientNotFound(senderClient, client_message)
		return
	} else if mc == nil {
		log.Printf("No MCU %s found for session %s to send %+v to %s", clientType, session.PublicId(), data, message.Recipient.SessionId)
		sendMcuClientNotFound(senderClient, client_message)
		return
	}

	mc.SendMessage(context.TODO(), message, data, func(err error, response map[string]interface{}) {
		if err != nil {
			log.Printf("Could not send MCU message %+v for session %s to %s: %s", data, session.PublicId(), message.Recipient.SessionId, err)
			sendMcuProcessingFailed(senderClient, client_message)
			return
		} else if response == nil {
			// No response received
			return
		}

		h.sendMcuMessageResponse(client, session, message, data, response)
	})
}

func (h *Hub) sendMcuMessageResponse(client *Client, session *ClientSession, message *MessageClientMessage, data *MessageClientMessageData, response map[string]interface{}) {
	var response_message *ServerMessage
	switch response["type"] {
	case "answer":
		answer_message := &AnswerOfferMessage{
			To:       session.PublicId(),
			From:     session.PublicId(),
			Type:     "answer",
			RoomType: data.RoomType,
			Payload:  response,
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
				Data: (*json.RawMessage)(&answer_data),
			},
		}
	case "offer":
		offer_message := &AnswerOfferMessage{
			To:       session.PublicId(),
			From:     message.Recipient.SessionId,
			Type:     "offer",
			RoomType: data.RoomType,
			Payload:  response,
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
				Data: (*json.RawMessage)(&offer_data),
			},
		}
	default:
		log.Printf("Unsupported response %+v received to send to %s", response, session.PublicId())
		return
	}

	if response_message != nil {
		session.SendMessage(response_message)
	}
}

func (h *Hub) processByeMsg(client *Client, message *ClientMessage) {
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
				h.sendRoom(client, nil, nil)
			}
		}
	}
}

func (h *Hub) processRoomInCallChanged(message *BackendServerRoomRequest) {
	room := message.room
	room.PublishUsersInCallChanged(message.InCall.Changed, message.InCall.Users)
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

func (h *Hub) lookupClientCountry(client *Client) string {
	ip := net.ParseIP(client.RemoteAddr())
	if ip == nil {
		return noCountry
	}

	for overrideNet, country := range h.geoipOverrides {
		if overrideNet.Contains(ip) {
			return country
		}
	}

	if ip.IsLoopback() {
		return loopback
	}

	country, err := h.geoip.LookupCountry(ip)
	if err != nil {
		log.Printf("Could not lookup country for %s: %s", ip, err)
		return unknownCountry
	}

	if country == "" {
		return unknownCountry
	}
	return country
}

func (h *Hub) serveWs(w http.ResponseWriter, r *http.Request) {
	addr := getRealUserIP(r)
	agent := r.Header.Get("User-Agent")

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Could not upgrade request from %s: %s", addr, err)
		return
	}

	client, err := NewClient(conn, addr, agent)
	if err != nil {
		log.Printf("Could not create client for %s: %s", addr, err)
		return
	}

	if h.geoip != nil {
		client.OnLookupCountry = h.lookupClientCountry
	}
	client.OnMessageReceived = h.processMessage
	client.OnClosed = func(client *Client) {
		h.processUnregister(client)
	}

	h.processNewClient(client)
	go client.WritePump()
	go client.ReadPump()
}
