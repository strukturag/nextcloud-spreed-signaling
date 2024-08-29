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
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDefaultUserId   = "test-userid"
	authAnonymousUserId = "anonymous-userid"

	testTimeout = 10 * time.Second
)

var (
	testRoomProperties = []byte("{\"prop1\":\"value1\"}")
)

var (
	clusteredTests = []string{
		"local",
		"clustered",
	}

	testHelloV2Algorithms = []string{
		"RSA",
		"ECDSA",
		"Ed25519",
		"Ed25519_Nextcloud",
	}
)

// Only used for testing.
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

func isLocalTest(t *testing.T) bool {
	return strings.HasSuffix(t.Name(), "/local")
}

func getTestConfig(server *httptest.Server) (*goconf.ConfigFile, error) {
	config := goconf.NewConfigFile()
	u, err := url.Parse(server.URL)
	if err != nil {
		return nil, err
	}
	config.AddOption("backend", "allowed", u.Host)
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	config.AddOption("backend", "secret", string(testBackendSecret))
	config.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config.AddOption("clients", "internalsecret", string(testInternalSecret))
	config.AddOption("geoip", "url", "none")
	return config, nil
}

func getTestConfigWithMultipleBackends(server *httptest.Server) (*goconf.ConfigFile, error) {
	config, err := getTestConfig(server)
	if err != nil {
		return nil, err
	}

	config.RemoveOption("backend", "allowed")
	config.RemoveOption("backend", "secret")
	config.AddOption("backend", "backends", "backend1, backend2")

	config.AddOption("backend1", "url", server.URL+"/one")
	config.AddOption("backend1", "secret", string(testBackendSecret))

	config.AddOption("backend2", "url", server.URL+"/two/")
	config.AddOption("backend2", "secret", string(testBackendSecret))
	return config, nil
}

func CreateHubForTestWithConfig(t *testing.T, getConfigFunc func(*httptest.Server) (*goconf.ConfigFile, error)) (*Hub, AsyncEvents, *mux.Router, *httptest.Server) {
	require := require.New(t)
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	events := getAsyncEventsForTest(t)
	config, err := getConfigFunc(server)
	require.NoError(err)
	h, err := NewHub(config, events, nil, nil, nil, r, "no-version")
	require.NoError(err)
	b, err := NewBackendServer(config, h, "no-version")
	require.NoError(err)
	require.NoError(b.Start(r))

	go h.Run()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		WaitForHub(ctx, t, h)
	})

	return h, events, r, server
}

func CreateHubForTest(t *testing.T) (*Hub, AsyncEvents, *mux.Router, *httptest.Server) {
	return CreateHubForTestWithConfig(t, getTestConfig)
}

func CreateHubWithMultipleBackendsForTest(t *testing.T) (*Hub, AsyncEvents, *mux.Router, *httptest.Server) {
	h, events, r, server := CreateHubForTestWithConfig(t, getTestConfigWithMultipleBackends)
	registerBackendHandlerUrl(t, r, "/one")
	registerBackendHandlerUrl(t, r, "/two")
	return h, events, r, server
}

func CreateClusteredHubsForTestWithConfig(t *testing.T, getConfigFunc func(*httptest.Server) (*goconf.ConfigFile, error)) (*Hub, *Hub, *mux.Router, *mux.Router, *httptest.Server, *httptest.Server) {
	require := require.New(t)
	r1 := mux.NewRouter()
	registerBackendHandler(t, r1)

	server1 := httptest.NewServer(r1)
	t.Cleanup(func() {
		server1.Close()
	})

	r2 := mux.NewRouter()
	registerBackendHandler(t, r2)

	server2 := httptest.NewServer(r2)
	t.Cleanup(func() {
		server2.Close()
	})

	nats1 := startLocalNatsServer(t)
	var nats2 string
	if strings.Contains(t.Name(), "Federation") {
		nats2 = startLocalNatsServer(t)
	} else {
		nats2 = nats1
	}
	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	if strings.Contains(t.Name(), "Federation") {
		// Signaling servers should not form a cluster in federation tests.
		addr1, addr2 = addr2, addr1
	}

	events1, err := NewAsyncEvents(nats1)
	require.NoError(err)
	t.Cleanup(func() {
		events1.Close()
	})
	config1, err := getConfigFunc(server1)
	require.NoError(err)
	client1, _ := NewGrpcClientsForTest(t, addr2)
	h1, err := NewHub(config1, events1, grpcServer1, client1, nil, r1, "no-version")
	require.NoError(err)
	b1, err := NewBackendServer(config1, h1, "no-version")
	require.NoError(err)
	events2, err := NewAsyncEvents(nats2)
	require.NoError(err)
	t.Cleanup(func() {
		events2.Close()
	})
	config2, err := getConfigFunc(server2)
	require.NoError(err)
	client2, _ := NewGrpcClientsForTest(t, addr1)
	h2, err := NewHub(config2, events2, grpcServer2, client2, nil, r2, "no-version")
	require.NoError(err)
	b2, err := NewBackendServer(config2, h2, "no-version")
	require.NoError(err)
	require.NoError(b1.Start(r1))
	require.NoError(b2.Start(r2))

	go h1.Run()
	go h2.Run()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		WaitForHub(ctx, t, h1)
		WaitForHub(ctx, t, h2)
	})

	return h1, h2, r1, r2, server1, server2
}

func CreateClusteredHubsForTest(t *testing.T) (*Hub, *Hub, *httptest.Server, *httptest.Server) {
	h1, h2, _, _, server1, server2 := CreateClusteredHubsForTestWithConfig(t, getTestConfig)
	return h1, h2, server1, server2
}

func WaitForHub(ctx context.Context, t *testing.T, h *Hub) {
	// Wait for any channel messages to be processed.
	time.Sleep(10 * time.Millisecond)
	h.Stop()
	for {
		h.mu.Lock()
		clients := len(h.clients)
		sessions := len(h.sessions)
		remoteSessions := len(h.remoteSessions)
		h.mu.Unlock()
		h.ru.Lock()
		rooms := len(h.rooms)
		h.ru.Unlock()
		readActive := h.readPumpActive.Load()
		writeActive := h.writePumpActive.Load()
		if clients == 0 &&
			rooms == 0 &&
			sessions == 0 &&
			remoteSessions == 0 &&
			readActive == 0 &&
			writeActive == 0 {
			break
		}

		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.ru.Lock()
			dumpGoroutines("", os.Stderr)
			assert.Fail(t, "Error waiting for clients %+v / rooms %+v / sessions %+v / remoteSessions %v / %d read / %d write to terminate: %s", h.clients, h.rooms, h.sessions, h.remoteSessions, readActive, writeActive, ctx.Err())
			h.ru.Unlock()
			h.mu.Unlock()
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func validateBackendChecksum(t *testing.T, f func(http.ResponseWriter, *http.Request, *BackendClientRequest) *BackendClientResponse) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		require := require.New(t)
		body, err := io.ReadAll(r.Body)
		require.NoError(err)

		rnd := r.Header.Get(HeaderBackendSignalingRandom)
		checksum := r.Header.Get(HeaderBackendSignalingChecksum)
		if rnd == "" || checksum == "" {
			require.Fail("No checksum headers found in request to %s", r.URL)
		}

		if verify := CalculateBackendChecksum(rnd, body, testBackendSecret); verify != checksum {
			require.Fail("Backend checksum verification failed for request to %s", r.URL)
		}

		var request BackendClientRequest
		require.NoError(json.Unmarshal(body, &request))

		response := f(w, r, &request)
		if response == nil {
			// Function already returned a response.
			return
		}

		data, err := json.Marshal(response)
		require.NoError(err)

		if r.Header.Get("OCS-APIRequest") != "" {
			var ocs OcsResponse
			ocs.Ocs = &OcsBody{
				Meta: OcsMeta{
					Status:     "ok",
					StatusCode: http.StatusOK,
					Message:    http.StatusText(http.StatusOK),
				},
				Data: data,
			}
			data, err = json.Marshal(ocs)
			require.NoError(err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data) // nolint
	}
}

func processAuthRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	require := require.New(t)
	if request.Type != "auth" || request.Auth == nil {
		require.Fail("Expected an auth backend request, got %+v", request)
	}

	var params TestBackendClientAuthParams
	if len(request.Auth.Params) > 0 {
		require.NoError(json.Unmarshal(request.Auth.Params, &params))
	}
	if params.UserId == "" {
		params.UserId = testDefaultUserId
	} else if params.UserId == authAnonymousUserId {
		params.UserId = ""
	}

	response := &BackendClientResponse{
		Type: "auth",
		Auth: &BackendClientAuthResponse{
			Version: BackendVersion,
			UserId:  params.UserId,
		},
	}
	userdata := map[string]string{
		"displayname": "Displayname " + params.UserId,
	}
	data, err := json.Marshal(userdata)
	require.NoError(err)
	response.Auth.User = data
	return response
}

func processRoomRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	require := require.New(t)
	assert := assert.New(t)
	if request.Type != "room" || request.Room == nil {
		require.Fail("Expected an room backend request, got %+v", request)
	}

	switch request.Room.RoomId {
	case "test-room-slow":
		time.Sleep(100 * time.Millisecond)
	case "test-room-takeover-room-session":
		// Additional checks for testcase "TestClientTakeoverRoomSession"
		if request.Room.Action == "leave" && request.Room.UserId == "test-userid1" {
			assert.Fail("Should not receive \"leave\" event for first user, received %+v", request.Room)
		}
	}

	if strings.Contains(t.Name(), "Federation") {
		// Check additional fields present for federated sessions.
		if strings.Contains(request.Room.SessionId, "@federated") {
			assert.Equal(ActorTypeFederatedUsers, request.Room.ActorType)
			assert.NotEmpty(request.Room.ActorId)
		} else {
			assert.Empty(request.Room.ActorType)
			assert.Empty(request.Room.ActorId)
		}
	}

	// Allow joining any room.
	response := &BackendClientResponse{
		Type: "room",
		Room: &BackendClientRoomResponse{
			Version:    BackendVersion,
			RoomId:     request.Room.RoomId,
			Properties: testRoomProperties,
		},
	}
	switch request.Room.RoomId {
	case "test-room-with-sessiondata":
		data := map[string]string{
			"userid": "userid-from-sessiondata",
		}
		tmp, err := json.Marshal(data)
		require.NoError(err)
		response.Room.Session = tmp
	case "test-room-initial-permissions":
		permissions := []Permission{PERMISSION_MAY_PUBLISH_AUDIO}
		response.Room.Permissions = &permissions
	}
	return response
}

var (
	sessionRequestHander struct {
		sync.Mutex
		handlers map[*testing.T]func(*BackendClientSessionRequest)
	}
)

func setSessionRequestHandler(t *testing.T, f func(*BackendClientSessionRequest)) {
	sessionRequestHander.Lock()
	defer sessionRequestHander.Unlock()
	if sessionRequestHander.handlers == nil {
		sessionRequestHander.handlers = make(map[*testing.T]func(*BackendClientSessionRequest))
	}
	if _, found := sessionRequestHander.handlers[t]; !found {
		t.Cleanup(func() {
			sessionRequestHander.Lock()
			defer sessionRequestHander.Unlock()

			delete(sessionRequestHander.handlers, t)
		})
	}
	sessionRequestHander.handlers[t] = f
}

func clearSessionRequestHandler(t *testing.T) { // nolint
	sessionRequestHander.Lock()
	defer sessionRequestHander.Unlock()

	delete(sessionRequestHander.handlers, t)
}

func processSessionRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "session" || request.Session == nil {
		require.Fail(t, "Expected an session backend request, got %+v", request)
	}

	sessionRequestHander.Lock()
	defer sessionRequestHander.Unlock()
	if f, found := sessionRequestHander.handlers[t]; found {
		f(request.Session)
	}

	response := &BackendClientResponse{
		Type: "session",
		Session: &BackendClientSessionResponse{
			Version: BackendVersion,
			RoomId:  request.Session.RoomId,
		},
	}
	return response
}

var pingRequests map[*testing.T][]*BackendClientRequest

func getPingRequests(t *testing.T) []*BackendClientRequest {
	return pingRequests[t]
}

func clearPingRequests(t *testing.T) {
	delete(pingRequests, t)
}

func storePingRequest(t *testing.T, request *BackendClientRequest) {
	if entries, found := pingRequests[t]; !found {
		if pingRequests == nil {
			pingRequests = make(map[*testing.T][]*BackendClientRequest)
		}
		pingRequests[t] = []*BackendClientRequest{
			request,
		}
		t.Cleanup(func() {
			clearPingRequests(t)
		})
	} else {
		pingRequests[t] = append(entries, request)
	}
}

func processPingRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "ping" || request.Ping == nil {
		require.Fail(t, "Expected an ping backend request, got %+v", request)
	}

	if request.Ping.RoomId == "test-room-with-sessiondata" {
		if entries := request.Ping.Entries; assert.Len(t, entries, 1) {
			assert.Empty(t, entries[0].UserId)
		}
	}

	storePingRequest(t, request)

	response := &BackendClientResponse{
		Type: "ping",
		Ping: &BackendClientRingResponse{
			Version: BackendVersion,
			RoomId:  request.Ping.RoomId,
		},
	}
	return response
}

func ensureAuthTokens(t *testing.T) (string, string) {
	require := require.New(t)
	if privateKey := os.Getenv("PRIVATE_AUTH_TOKEN_" + t.Name()); privateKey != "" {
		publicKey := os.Getenv("PUBLIC_AUTH_TOKEN_" + t.Name())
		// should not happen, always both keys are created
		require.NotEmpty(publicKey, "public key is empty")
		return privateKey, publicKey
	}

	var private []byte
	var public []byte

	if strings.Contains(t.Name(), "ECDSA") {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(err)

		private, err = x509.MarshalECPrivateKey(key)
		require.NoError(err)
		private = pem.EncodeToMemory(&pem.Block{
			Type:  "ECDSA PRIVATE KEY",
			Bytes: private,
		})

		public, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
		require.NoError(err)
		public = pem.EncodeToMemory(&pem.Block{
			Type:  "ECDSA PUBLIC KEY",
			Bytes: public,
		})
	} else if strings.Contains(t.Name(), "Ed25519") {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(err)

		private, err = x509.MarshalPKCS8PrivateKey(privateKey)
		require.NoError(err)
		private = pem.EncodeToMemory(&pem.Block{
			Type:  "Ed25519 PRIVATE KEY",
			Bytes: private,
		})

		public, err = x509.MarshalPKIXPublicKey(publicKey)
		require.NoError(err)
		public = pem.EncodeToMemory(&pem.Block{
			Type:  "Ed25519 PUBLIC KEY",
			Bytes: public,
		})
	} else {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)

		private = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})

		public, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
		require.NoError(err)
		public = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: public,
		})
	}

	privateKey := base64.StdEncoding.EncodeToString(private)
	t.Setenv("PRIVATE_AUTH_TOKEN_"+t.Name(), privateKey)
	publicKey := base64.StdEncoding.EncodeToString(public)
	t.Setenv("PUBLIC_AUTH_TOKEN_"+t.Name(), publicKey)
	return privateKey, publicKey
}

func getPrivateAuthToken(t *testing.T) (key interface{}) {
	private, _ := ensureAuthTokens(t)
	data, err := base64.StdEncoding.DecodeString(private)
	require.NoError(t, err)
	if strings.Contains(t.Name(), "ECDSA") {
		key, err = jwt.ParseECPrivateKeyFromPEM(data)
	} else if strings.Contains(t.Name(), "Ed25519") {
		key, err = jwt.ParseEdPrivateKeyFromPEM(data)
	} else {
		key, err = jwt.ParseRSAPrivateKeyFromPEM(data)
	}
	require.NoError(t, err)
	return key
}

func getPublicAuthToken(t *testing.T) (key interface{}) {
	_, public := ensureAuthTokens(t)
	data, err := base64.StdEncoding.DecodeString(public)
	require.NoError(t, err)
	if strings.Contains(t.Name(), "ECDSA") {
		key, err = jwt.ParseECPublicKeyFromPEM(data)
	} else if strings.Contains(t.Name(), "Ed25519") {
		key, err = jwt.ParseEdPublicKeyFromPEM(data)
	} else {
		key, err = jwt.ParseRSAPublicKeyFromPEM(data)
	}
	require.NoError(t, err)
	return key
}

func registerBackendHandler(t *testing.T, router *mux.Router) {
	registerBackendHandlerUrl(t, router, "/")
}

func registerBackendHandlerUrl(t *testing.T, router *mux.Router, url string) {
	handleFunc := validateBackendChecksum(t, func(w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
		switch request.Type {
		case "auth":
			return processAuthRequest(t, w, r, request)
		case "room":
			return processRoomRequest(t, w, r, request)
		case "session":
			return processSessionRequest(t, w, r, request)
		case "ping":
			return processPingRequest(t, w, r, request)
		default:
			require.Fail(t, "Unsupported request received: %+v", request)
			return nil
		}
	})

	router.HandleFunc(url, handleFunc)
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}

	handleCapabilitiesFunc := func(w http.ResponseWriter, r *http.Request) {
		features := []string{
			"foo",
			"bar",
		}
		if strings.Contains(t.Name(), "V3Api") {
			features = append(features, "signaling-v3")
		}
		if strings.Contains(t.Name(), "Federation") {
			features = append(features, "federation-v2")
		}
		signaling := map[string]interface{}{
			"foo": "bar",
			"baz": 42,
		}
		config := map[string]interface{}{
			"signaling": signaling,
		}
		if strings.Contains(t.Name(), "MultiRoom") {
			signaling[ConfigKeySessionPingLimit] = 2
		}
		useV2 := true
		if os.Getenv("SKIP_V2_CAPABILITIES") != "" {
			useV2 = false
		}
		if (strings.Contains(t.Name(), "V2") && useV2) || strings.Contains(t.Name(), "Federation") {
			key := getPublicAuthToken(t)
			public, err := x509.MarshalPKIXPublicKey(key)
			require.NoError(t, err)
			var pemType string
			if strings.Contains(t.Name(), "ECDSA") {
				pemType = "ECDSA PUBLIC KEY"
			} else if strings.Contains(t.Name(), "Ed25519") {
				pemType = "Ed25519 PUBLIC KEY"
			} else {
				pemType = "RSA PUBLIC KEY"
			}

			public = pem.EncodeToMemory(&pem.Block{
				Type:  pemType,
				Bytes: public,
			})
			if strings.Contains(t.Name(), "Ed25519_Nextcloud") {
				// Simulate Nextcloud which returns the Ed25519 key as base64-encoded data.
				encoded := base64.StdEncoding.EncodeToString(key.(ed25519.PublicKey))
				signaling[ConfigKeyHelloV2TokenKey] = encoded
			} else {
				signaling[ConfigKeyHelloV2TokenKey] = string(public)
			}
		}
		spreedCapa, _ := json.Marshal(map[string]interface{}{
			"features": features,
			"config":   config,
		})
		response := &CapabilitiesResponse{
			Version: CapabilitiesVersion{
				Major: 20,
			},
			Capabilities: map[string]json.RawMessage{
				"spreed": spreedCapa,
			},
		}

		data, err := json.Marshal(response)
		assert.NoError(t, err, "Could not marshal %+v", response)

		var ocs OcsResponse
		ocs.Ocs = &OcsBody{
			Meta: OcsMeta{
				Status:     "ok",
				StatusCode: http.StatusOK,
				Message:    http.StatusText(http.StatusOK),
			},
			Data: data,
		}
		data, err = json.Marshal(ocs)
		require.NoError(t, err)
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data) // nolint
	}
	router.HandleFunc(url+"ocs/v2.php/cloud/capabilities", handleCapabilitiesFunc)

	if strings.Contains(t.Name(), "V3Api") {
		router.HandleFunc(url+"ocs/v2.php/apps/spreed/api/v3/signaling/backend", handleFunc)
	} else {
		router.HandleFunc(url+"ocs/v2.php/apps/spreed/api/v1/signaling/backend", handleFunc)
	}
}

func performHousekeeping(hub *Hub, now time.Time) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		hub.performHousekeeping(now)
		wg.Done()
	}()
	return &wg
}

func TestWebsocketFeatures(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, response, err := websocket.DefaultDialer.DialContext(ctx, getWebsocketUrl(server.URL), nil)
	require.NoError(err)
	defer conn.Close() // nolint

	serverHeader := response.Header.Get("Server")
	assert.True(strings.HasPrefix(serverHeader, "nextcloud-spreed-signaling/"), "expected valid server header, got \"%s\"", serverHeader)
	features := response.Header.Get("X-Spreed-Signaling-Features")
	featuresList := make(map[string]bool)
	for _, f := range strings.Split(features, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			_, found := featuresList[f]
			assert.False(found, "duplicate feature id \"%s\" in \"%s\"", f, features)
			featuresList[f] = true
		}
	}
	if len(featuresList) <= 1 {
		assert.Fail("expected valid features header, got \"%s\"", features)
	}
	_, found := featuresList["hello-v2"]
	assert.True(found, "expected feature \"hello-v2\", got \"%s\"", features)

	assert.NoError(conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{}))
}

func TestInitialWelcome(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClientContext(ctx, t, server, hub)
	defer client.CloseWithBye()

	msg, err := client.RunUntilMessage(ctx)
	require.NoError(err)

	assert.Equal("welcome", msg.Type, "%+v", msg)
	if assert.NotNil(msg.Welcome, "%+v", msg) {
		assert.NotEmpty(msg.Welcome.Version, "%+v", msg)
		assert.NotEmpty(msg.Welcome.Features, "%+v", msg)
	}
}

func TestExpectClientHello(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	// The server will send an error and close the connection if no "Hello"
	// is sent.
	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated due to the missing "Hello" request.
	performHousekeeping(hub, time.Now().Add(initialHelloTimeout+time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	message, err := client.RunUntilMessage(ctx)
	require.NoError(checkUnexpectedClose(err))

	message2, err := client.RunUntilMessage(ctx)
	if message2 != nil {
		require.Fail("Received multiple messages, already have %+v, also got %+v", message, message2)
	}
	require.NoError(checkUnexpectedClose(err))

	if err := checkMessageType(message, "bye"); assert.NoError(err) {
		assert.Equal("hello_timeout", message.Bye.Reason, "%+v", message.Bye)
	}
}

func TestExpectClientHelloUnsupportedVersion(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	params := TestBackendClientAuthParams{
		UserId: testDefaultUserId,
	}
	require.NoError(client.SendHelloParams(server.URL, "0.0", "", nil, params))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	message, err := client.RunUntilMessage(ctx)
	require.NoError(checkUnexpectedClose(err))

	if err := checkMessageType(message, "error"); assert.NoError(err) {
		assert.Equal("invalid_hello_version", message.Error.Code)
	}
}

func TestClientHelloV1(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}
}

func TestClientHelloV2(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			require.NoError(client.SendHelloV2(testDefaultUserId))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello, err := client.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)

			data := hub.decodeSessionId(hello.Hello.SessionId, publicSessionName)
			require.NotNil(data, "Could not decode session id: %s", hello.Hello.SessionId)

			hub.mu.RLock()
			session := hub.sessions[data.Sid]
			hub.mu.RUnlock()
			require.NotNil(session, "Could not get session for id %+v", data)

			var userdata map[string]string
			require.NoError(json.Unmarshal(session.UserData(), &userdata))

			assert.Equal("Displayname "+testDefaultUserId, userdata["displayname"])
		})
	}
}

func TestClientHelloV2_IssuedInFuture(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(time.Minute)
			expiresAt := issuedAt.Add(time.Second)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			require.NoError(checkUnexpectedClose(err))

			if err := checkMessageType(message, "error"); assert.NoError(err) {
				assert.Equal("token_not_valid_yet", message.Error.Code, "%+v", message)
			}
		})
	}
}

func TestClientHelloV2_Expired(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(-time.Minute)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, issuedAt.Add(time.Second)))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			require.NoError(checkUnexpectedClose(err))

			if err := checkMessageType(message, "error"); assert.NoError(err) {
				assert.Equal("token_expired", message.Error.Code, "%+v", message)
			}
		})
	}
}

func TestClientHelloV2_IssuedAtMissing(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			var issuedAt time.Time
			expiresAt := time.Now().Add(time.Minute)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			require.NoError(checkUnexpectedClose(err))

			if err := checkMessageType(message, "error"); assert.NoError(err) {
				assert.Equal("token_not_valid_yet", message.Error.Code, "%+v", message)
			}
		})
	}
}

func TestClientHelloV2_ExpiresAtMissing(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(-time.Minute)
			var expiresAt time.Time
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			require.NoError(checkUnexpectedClose(err))

			if err := checkMessageType(message, "error"); assert.NoError(err) {
				assert.Equal("token_expired", message.Error.Code, "%+v", message)
			}
		})
	}
}

func TestClientHelloV2_CachedCapabilities(t *testing.T) {
	CatchLogForTest(t)
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			// Simulate old-style Nextcloud without capabilities for Hello V2.
			t.Setenv("SKIP_V2_CAPABILITIES", "1")

			client1 := NewTestClient(t, server, hub)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHelloV1(testDefaultUserId + "1"))

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId+"1", hello1.Hello.UserId, "%+v", hello1.Hello)
			assert.NotEmpty(hello1.Hello.SessionId, "%+v", hello1.Hello)

			// Simulate updated Nextcloud with capabilities for Hello V2.
			t.Setenv("SKIP_V2_CAPABILITIES", "")

			client2 := NewTestClient(t, server, hub)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId+"2", hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2.Hello)
		})
	}
}

func TestClientHelloWithSpaces(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	userId := "test user with spaces"
	require.NoError(client.SendHello(userId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(userId, hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	}
}

func TestClientHelloAllowAll(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.RemoveOption("backend", "allowed")
		config.AddOption("backend", "allowall", "true")
		return config, nil
	})

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	}
}

func TestClientHelloSessionLimit(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				var router1 *mux.Router
				hub1, _, router1, server1 = CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
					config, err := getTestConfig(server)
					if err != nil {
						return nil, err
					}

					config.RemoveOption("backend", "allowed")
					config.RemoveOption("backend", "secret")
					config.AddOption("backend", "backends", "backend1, backend2")

					config.AddOption("backend1", "url", server.URL+"/one")
					config.AddOption("backend1", "secret", string(testBackendSecret))
					config.AddOption("backend1", "sessionlimit", "1")

					config.AddOption("backend2", "url", server.URL+"/two")
					config.AddOption("backend2", "secret", string(testBackendSecret))
					return config, nil
				})

				registerBackendHandlerUrl(t, router1, "/one")
				registerBackendHandlerUrl(t, router1, "/two")

				hub2 = hub1
				server2 = server1
			} else {
				var router1 *mux.Router
				var router2 *mux.Router
				hub1, hub2, router1, router2, server1, server2 = CreateClusteredHubsForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
					// Make sure all backends use the same server
					if server1 == nil {
						server1 = server
					} else {
						server = server1
					}

					config, err := getTestConfig(server)
					if err != nil {
						return nil, err
					}

					config.RemoveOption("backend", "allowed")
					config.RemoveOption("backend", "secret")
					config.AddOption("backend", "backends", "backend1, backend2")

					config.AddOption("backend1", "url", server.URL+"/one")
					config.AddOption("backend1", "secret", string(testBackendSecret))
					config.AddOption("backend1", "sessionlimit", "1")

					config.AddOption("backend2", "url", server.URL+"/two")
					config.AddOption("backend2", "secret", string(testBackendSecret))
					return config, nil
				})

				registerBackendHandlerUrl(t, router1, "/one")
				registerBackendHandlerUrl(t, router1, "/two")

				registerBackendHandlerUrl(t, router2, "/one")
				registerBackendHandlerUrl(t, router2, "/two")
			}

			client := NewTestClient(t, server1, hub1)
			defer client.CloseWithBye()

			params1 := TestBackendClientAuthParams{
				UserId: testDefaultUserId,
			}
			require.NoError(client.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params1))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
				assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}

			// The second client can't connect as it would exceed the session limit.
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			params2 := TestBackendClientAuthParams{
				UserId: testDefaultUserId + "2",
			}
			require.NoError(client2.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params2))

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				assert.Equal("error", msg.Type, "%+v", msg)
				if assert.NotNil(msg.Error, "%+v", msg) {
					assert.Equal("session_limit_exceeded", msg.Error.Code, "%+v", msg)
				}
			}

			// The client can connect to a different backend.
			require.NoError(client2.SendHelloParams(server1.URL+"/two", HelloVersionV1, "client", nil, params2))

			if hello, err := client2.RunUntilHello(ctx); assert.NoError(err) {
				assert.Equal(testDefaultUserId+"2", hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}

			// If the first client disconnects (and releases the session), a new one can connect.
			client.CloseWithBye()
			assert.NoError(client.WaitForClientRemoved(ctx))

			client3 := NewTestClient(t, server2, hub2)
			defer client3.CloseWithBye()

			params3 := TestBackendClientAuthParams{
				UserId: testDefaultUserId + "3",
			}
			require.NoError(client3.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params3))

			if hello, err := client3.RunUntilHello(ctx); assert.NoError(err) {
				assert.Equal(testDefaultUserId+"3", hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}
		})
	}
}

func TestSessionIdsUnordered(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	publicSessionIds := make([]string, 0)
	for i := 0; i < 20; i++ {
		client := NewTestClient(t, server, hub)
		defer client.CloseWithBye()

		require.NoError(client.SendHello(testDefaultUserId))

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)

			data := hub.decodeSessionId(hello.Hello.SessionId, publicSessionName)
			if !assert.NotNil(data, "Could not decode session id: %s", hello.Hello.SessionId) {
				break
			}

			hub.mu.RLock()
			session := hub.sessions[data.Sid]
			hub.mu.RUnlock()
			if !assert.NotNil(session, "Could not get session for id %+v", data) {
				break
			}

			publicSessionIds = append(publicSessionIds, session.PublicId())
		}
	}

	require.NotEmpty(publicSessionIds, "no session ids decoded")

	larger := 0
	smaller := 0
	prevSid := ""
	for i, sid := range publicSessionIds {
		if i > 0 {
			if sid > prevSid {
				larger++
			} else if sid < prevSid {
				smaller--
			} else {
				assert.Fail("should not have received the same session id twice")
			}
		}
		prevSid = sid
	}

	// Public session ids should not be ordered.
	assert.NotEqual(larger, len(publicSessionIds), "the session ids are all larger than the previous ones")
	assert.NotEqual(smaller, len(publicSessionIds), "the session ids are all smaller than the previous ones")
}

func TestClientHelloResume(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if hello2, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)
	}
}

func TestClientHelloResumeThrottle(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay
	hub.throttler = th

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	timing.expectedSleep = 100 * time.Millisecond
	require.NoError(client.SendHelloResume("this-is-invalid"))

	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId)
	assert.NotEmpty(hello.Hello.SessionId)
	assert.NotEmpty(hello.Hello.ResumeId)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// Perform housekeeping in the future, this will cause the session to be
	// cleaned up after it is expired.
	performHousekeeping(hub, time.Now().Add(sessionExpireDuration+time.Second)).Wait()

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	// Valid but expired resume ids will not be throttled.
	timing.expectedSleep = 0 * time.Millisecond
	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	timing.expectedSleep = 200 * time.Millisecond
	require.NoError(client.SendHelloResume("this-is-invalid"))

	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}
}

func TestClientHelloResumeExpired(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// Perform housekeeping in the future, this will cause the session to be
	// cleaned up after it is expired.
	performHousekeeping(hub, time.Now().Add(sessionExpireDuration+time.Second)).Wait()

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}
}

func TestClientHelloResumeTakeover(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

	// The first client got disconnected with a reason in a "Bye" message.
	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("bye", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Bye, "%+v", msg) {
			assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
		}
	}

	if msg, err := client1.RunUntilMessage(ctx); err == nil {
		assert.Fail("Expected error but received %+v", msg)
	} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		assert.Fail("Expected close error but received %+v", err)
	}
}

func TestClientHelloResumeOtherHub(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// Simulate a restart of the hub.
	hub.sid.Store(0)
	sessions := make([]Session, 0)
	hub.mu.Lock()
	for _, session := range hub.sessions {
		sessions = append(sessions, session)
	}
	hub.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
	hub.mu.Lock()
	count := len(hub.sessions)
	hub.mu.Unlock()
	assert.Equal(0, count, "Should have removed all sessions")

	// The new client will get the same (internal) sid for his session.
	newClient := NewTestClient(t, server, hub)
	defer newClient.CloseWithBye()

	require.NoError(newClient.SendHello(testDefaultUserId))

	hello2, err := newClient.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
	assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2.Hello)
	assert.NotEmpty(hello2.Hello.ResumeId, "%+v", hello2.Hello)

	// The previous session (which had the same internal sid) can't be resumed.
	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloResumePublicId(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	// Test that a client can't resume a "public" session of another user.
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data := "from-1-to-2"
	client1.SendMessage(recipient2, data) // nolint

	var payload string
	var sender *MessageServerMessageSender
	if err := checkReceiveClientMessageWithSender(ctx, client2, "session", hello1.Hello, &payload, &sender); assert.NoError(err) {
		assert.Equal(data, payload)
	}

	client1.Close()
	assert.NoError(client1.WaitForClientRemoved(ctx))

	client1 = NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	// Can't resume a session with the id received from messages of a client.
	require.NoError(client1.SendHelloResume(sender.SessionId))
	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloByeResume(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	require.NoError(client.SendBye())
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageType(message, "bye"))
	}

	client.Close()
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("error", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Error, "%+v", msg) {
			assert.Equal("no_such_session", msg.Error.Code, "%+v", msg)
		}
	}
}

func TestClientHelloResumeAndJoin(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	hello2, err := client.RunUntilHello(ctx)
	require.NoError(err)
	assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)
}

func runGrpcProxyTest(t *testing.T, f func(hub1, hub2 *Hub, server1, server2 *httptest.Server)) {
	t.Helper()

	var hub1 *Hub
	var hub2 *Hub
	var server1 *httptest.Server
	var server2 *httptest.Server
	var router1 *mux.Router
	var router2 *mux.Router
	hub1, hub2, router1, router2, server1, server2 = CreateClusteredHubsForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		// Make sure all backends use the same server
		if server1 == nil {
			server1 = server
		} else {
			server = server1
		}

		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.RemoveOption("backend", "allowed")
		config.RemoveOption("backend", "secret")
		config.AddOption("backend", "backends", "backend1")

		config.AddOption("backend1", "url", server.URL)
		config.AddOption("backend1", "secret", string(testBackendSecret))
		config.AddOption("backend1", "sessionlimit", "1")
		return config, nil
	})

	registerBackendHandlerUrl(t, router1, "/")
	registerBackendHandlerUrl(t, router2, "/")

	f(hub1, hub2, server1, server2)
}

func TestClientHelloResumeProxy(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client1.Close()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client2.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			assert.NoError(client2.RunUntilJoined(ctx, hello.Hello))

			room := hub1.getRoom(roomId)
			require.NotNil(room, "Could not find room %s", roomId)
			room2 := hub2.getRoom(roomId)
			require.Nil(room2, "Should not have gotten room %s", roomId)

			users := []map[string]interface{}{
				{
					"sessionId": "the-session-id",
					"inCall":    1,
				},
			}
			room.PublishUsersInCallChanged(users, users)
			assert.NoError(checkReceiveClientEvent(ctx, client2, "update", nil))
		})
	})
}

func TestClientHelloResumeProxy_Takeover(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

			// The first client got disconnected with a reason in a "Bye" message.
			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				assert.Equal("bye", msg.Type, "%+v", msg)
				if assert.NotNil(msg.Bye, "%+v", msg) {
					assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
				}
			}

			if msg, err := client1.RunUntilMessage(ctx); err == nil {
				assert.Fail("Expected error but received %+v", msg)
			} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				assert.Fail("Expected close error but received %+v", err)
			}

			client3 := NewTestClient(t, server1, hub1)
			defer client3.CloseWithBye()

			require.NoError(client3.SendHelloResume(hello.Hello.ResumeId))
			hello3, err := client3.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello3.Hello.UserId, "%+v", hello3.Hello)
			assert.Equal(hello.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
			assert.Equal(hello.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)

			// The second client got disconnected with a reason in a "Bye" message.
			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				assert.Equal("bye", msg.Type, "%+v", msg)
				if assert.NotNil(msg.Bye, "%+v", msg) {
					assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
				}
			}

			if msg, err := client2.RunUntilMessage(ctx); err == nil {
				assert.Fail("Expected error but received %+v", msg)
			} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				assert.Fail("Expected close error but received %+v", err)
			}
		})
	})
}

func TestClientHelloResumeProxy_Disconnect(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client1.Close()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)
			assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

			// Simulate unclean shutdown of second instance.
			hub2.rpcServer.conn.Stop()

			assert.NoError(client2.WaitForClientRemoved(ctx))
		})
	})
}

func TestClientHelloClient(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloClient(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}
}

func TestClientHelloClient_V3Api(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	params := TestBackendClientAuthParams{
		UserId: testDefaultUserId,
	}
	// The "/api/v1/signaling/" URL will be changed to use "v3" as the "signaling-v3"
	// feature is returned by the capabilities endpoint.
	require.NoError(client.SendHelloParams(server.URL+"/ocs/v2.php/apps/spreed/api/v1/signaling/backend", HelloVersionV1, "client", nil, params))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}
}

func TestClientHelloInternal(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloInternal())

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}
}

func TestClientMessageToSessionId(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			mcu1, err := NewTestMCU()
			require.NoError(err)
			hub1.SetMcu(mcu1)

			if hub1 != hub2 {
				mcu2, err := NewTestMCU()
				require.NoError(err)
				hub2.SetMcu(mcu2)
			}

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

			recipient1 := MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}
			recipient2 := MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}

			data1 := map[string]interface{}{
				"type":    "test",
				"message": "from-1-to-2",
			}
			client1.SendMessage(recipient2, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient1, data2) // nolint

			var payload1 string
			if err := checkReceiveClientMessage(ctx, client1, "session", hello2.Hello, &payload1); assert.NoError(err) {
				assert.Equal(data2, payload1)
			}
			var payload2 map[string]interface{}
			if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload2); assert.NoError(err) {
				assert.Equal(data1, payload2)
			}
		})
	}
}

func TestClientControlToSessionId(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

			recipient1 := MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}
			recipient2 := MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient2, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient1, data2) // nolint

			var payload string
			if err := checkReceiveClientControl(ctx, client1, "session", hello2.Hello, &payload); assert.NoError(err) {
				assert.Equal(data2, payload)
			}
			if err := checkReceiveClientControl(ctx, client2, "session", hello1.Hello, &payload); assert.NoError(err) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientControlMissingPermissions(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 1 may not send control messages (will be ignored).
	session1.SetPermissions([]Permission{
		PERMISSION_MAY_PUBLISH_AUDIO,
		PERMISSION_MAY_PUBLISH_VIDEO,
	})
	// Client 2 may send control messages.
	session2.SetPermissions([]Permission{
		PERMISSION_MAY_PUBLISH_AUDIO,
		PERMISSION_MAY_PUBLISH_VIDEO,
		PERMISSION_MAY_CONTROL,
	})

	recipient1 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendControl(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendControl(recipient1, data2) // nolint

	var payload string
	if err := checkReceiveClientControl(ctx, client1, "session", hello2.Hello, &payload); assert.NoError(err) {
		assert.Equal(data2, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}
}

func TestClientMessageToUserId(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	recipient1 := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello1.Hello.UserId,
	}
	recipient2 := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2) // nolint

	var payload string
	if err := checkReceiveClientMessage(ctx, client1, "user", hello2.Hello, &payload); assert.NoError(err) {
		assert.Equal(data2, payload)
	}

	if err := checkReceiveClientMessage(ctx, client2, "user", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}
}

func TestClientControlToUserId(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	recipient1 := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello1.Hello.UserId,
	}
	recipient2 := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendControl(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendControl(recipient1, data2) // nolint

	var payload string
	if err := checkReceiveClientControl(ctx, client1, "user", hello2.Hello, &payload); assert.NoError(err) {
		assert.Equal(data2, payload)
	}

	if err := checkReceiveClientControl(ctx, client2, "user", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}
}

func TestClientMessageToUserIdMultipleSessions(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2a := NewTestClient(t, server, hub)
	defer client2a.CloseWithBye()
	require.NoError(client2a.SendHello(testDefaultUserId + "2"))
	client2b := NewTestClient(t, server, hub)
	defer client2b.CloseWithBye()
	require.NoError(client2b.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2a, err := client2a.RunUntilHello(ctx)
	require.NoError(err)
	hello2b, err := client2b.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2a.Hello.SessionId)
	require.NotEqual(hello1.Hello.SessionId, hello2b.Hello.SessionId)
	require.NotEqual(hello2a.Hello.SessionId, hello2b.Hello.SessionId)

	require.NotEqual(hello1.Hello.UserId, hello2a.Hello.UserId)
	require.NotEqual(hello1.Hello.UserId, hello2b.Hello.UserId)
	require.Equal(hello2a.Hello.UserId, hello2b.Hello.UserId)

	recipient := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2a.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint

	// Both clients will receive the message as it was sent to the user.
	var payload string
	if err := checkReceiveClientMessage(ctx, client2a, "user", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}
	if err := checkReceiveClientMessage(ctx, client2b, "user", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}
}

func WaitForUsersJoined(ctx context.Context, t *testing.T, client1 *TestClient, hello1 *ServerMessage, client2 *TestClient, hello2 *ServerMessage) {
	// We will receive "joined" events for all clients. The ordering is not
	// defined as messages are processed and sent by asynchronous event handlers.
	assert.NoError(t, client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))
	assert.NoError(t, client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))
}

func TestClientMessageToRoom(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client1.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg, err = client2.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendMessage(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient, data2) // nolint

			var payload string
			if err := checkReceiveClientMessage(ctx, client1, "room", hello2.Hello, &payload); assert.NoError(err) {
				assert.Equal(data2, payload)
			}

			if err := checkReceiveClientMessage(ctx, client2, "room", hello1.Hello, &payload); assert.NoError(err) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientControlToRoom(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client1.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg, err = client2.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient, data2) // nolint

			var payload string
			if err := checkReceiveClientControl(ctx, client1, "room", hello2.Hello, &payload); assert.NoError(err) {
				assert.Equal(data2, payload)
			}

			if err := checkReceiveClientControl(ctx, client2, "room", hello1.Hello, &payload); assert.NoError(err) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestJoinRoom(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Leave room.
	roomMsg, err = client.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)
}

func TestJoinRoomTwice(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)
	require.Equal(string(testRoomProperties), string(roomMsg.Room.Properties))

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	msg := &ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomId + "-" + client.publicId + "-2",
		},
	}
	require.NoError(client.WriteJSON(msg))

	message, err := client.RunUntilMessage(ctx)
	require.NoError(err)
	require.NoError(checkUnexpectedClose(err))

	assert.Equal(msg.Id, message.Id)
	if assert.NoError(checkMessageType(message, "error")) {
		assert.Equal("already_joined", message.Error.Code)
		assert.NotNil(message.Error.Details)
	}

	var roomDetails RoomErrorDetails
	if err := json.Unmarshal(message.Error.Details, &roomDetails); assert.NoError(err) {
		if assert.NotNil(roomDetails.Room, "%+v", message) {
			assert.Equal(roomId, roomDetails.Room.RoomId)
			assert.Equal(string(testRoomProperties), string(roomDetails.Room.Properties))
		}
	}
}

func TestExpectAnonymousJoinRoom(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(authAnonymousUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if assert.NoError(err) {
		assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		if assert.NoError(checkMessageType(message, "bye")) {
			assert.Equal("room_join_timeout", message.Bye.Reason, "%+v", message.Bye)
		}
	}

	// Both the client and the session get removed from the hub.
	assert.NoError(client.WaitForClientRemoved(ctx))
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
}

func TestExpectAnonymousJoinRoomAfterLeave(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(authAnonymousUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if assert.NoError(err) {
		assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Perform housekeeping in the future, this will keep the connection as the
	// session joined a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client.RunUntilMessage(ctx2); err == nil {
		assert.Fail("Expected no message, got %+v", message)
	} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		assert.NoError(err)
	}

	// Leave room
	roomMsg, err = client.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		if assert.NoError(checkMessageType(message, "bye")) {
			assert.Equal("room_join_timeout", message.Bye.Reason, "%+v", message.Bye)
		}
	}

	// Both the client and the session get removed from the hub.
	assert.NoError(client.WaitForClientRemoved(ctx))
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
}

func TestJoinRoomChange(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Change room.
	roomId = "other-test-room"
	roomMsg, err = client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Leave room.
	roomMsg, err = client.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)
}

func TestJoinMultiple(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	// Join room by id (first client).
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	// Join room by id (second client).
	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event for the first and the second client.
	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))
	// The first client will also receive a "joined" event from the second client.
	assert.NoError(client1.RunUntilJoined(ctx, hello2.Hello))

	// Leave room.
	roomMsg, err = client1.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)

	// The second client will now receive a "left" event
	assert.NoError(client2.RunUntilLeft(ctx, hello1.Hello))

	roomMsg, err = client2.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)
}

func TestJoinDisplaynamesPermission(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 2 may not receive display names.
	session2.SetPermissions([]Permission{PERMISSION_HIDE_DISPLAYNAMES})

	// Join room by id (first client).
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	// Join room by id (second client).
	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event for the first and the second client.
	if events, unexpected, err := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello); assert.NoError(err) {
		assert.Empty(unexpected)
		if assert.Len(events, 2) {
			assert.Nil(events[0].User)
			assert.Nil(events[1].User)
		}
	}
	// The first client will also receive a "joined" event from the second client.
	if events, unexpected, err := client1.RunUntilJoinedAndReturn(ctx, hello2.Hello); assert.NoError(err) {
		assert.Empty(unexpected)
		if assert.Len(events, 1) {
			assert.NotNil(events[0].User)
		}
	}
}

func TestInitialRoomPermissions(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId + "1"))

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room-initial-permissions"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	assert.True(session.HasPermission(PERMISSION_MAY_PUBLISH_AUDIO), "Session %s should have %s, got %+v", session.PublicId(), PERMISSION_MAY_PUBLISH_AUDIO, session.permissions)
	assert.False(session.HasPermission(PERMISSION_MAY_PUBLISH_VIDEO), "Session %s should not have %s, got %+v", session.PublicId(), PERMISSION_MAY_PUBLISH_VIDEO, session.permissions)
}

func TestJoinRoomSwitchClient(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room-slow"
	msg := &ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomId + "-" + hello.Hello.SessionId,
		},
	}
	require.NoError(client.WriteJSON(msg))
	// Wait a bit to make sure request is sent before closing client.
	time.Sleep(1 * time.Millisecond)
	client.Close()
	require.NoError(client.WaitForClientRemoved(ctx))

	// The client needs some time to reconnect.
	time.Sleep(200 * time.Millisecond)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
	if hello2, err := client2.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)
	}

	roomMsg, err := client2.RunUntilMessage(ctx)
	require.NoError(err)
	require.NoError(checkUnexpectedClose(err))
	require.NoError(checkMessageType(roomMsg, "room"))
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client2.RunUntilJoined(ctx, hello.Hello))

	// Leave room.
	roomMsg, err = client2.JoinRoom(ctx, "")
	require.NoError(err)
	require.Equal("", roomMsg.Room.RoomId)
}

func TestGetRealUserIP(t *testing.T) {
	testcases := []struct {
		expected string
		headers  http.Header
		trusted  string
		addr     string
	}{
		{
			"192.168.1.2",
			nil,
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"10.11.12.13",
			nil,
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"10.11.12.13"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"2002:db8::1"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"10.11.12.13"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"2002:db8::1"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 2001:db8::1"},
			},
			"192.168.0.0/16, 2001:db8::/48",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 192.168.30.32"},
			},
			"192.168.0.0/16, 2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 2001:db8::2"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		// "X-Real-IP" has preference before "X-Forwarded-For"
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"10.11.12.13"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		// Multiple "X-Forwarded-For" headers are merged.
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"1.2.3.4", "11.12.13.14", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"1.2.3.4", "2.3.4.5", "11.12.13.14", "192.168.31.32", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		// Headers are ignored if coming from untrusted clients.
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"11.12.13.14"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		// X-Forwarded-For is filtered for trusted proxies.
		{
			"1.2.3.4",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"1.2.3.4",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4, 192.168.2.3"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		// Invalid IPs are ignored.
		{
			"192.168.1.2",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"this-is-not-an-ip"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"this-is-not-an-ip"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"this-is-not-an-ip"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32, proxy1"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"192.168.1.2",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"this-is-not-an-ip"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"192.168.2.3",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"this-is-not-an-ip, 192.168.2.3"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
	}

	for _, tc := range testcases {
		trustedProxies, err := ParseAllowedIps(tc.trusted)
		if !assert.NoError(t, err, "invalid trusted proxies in %+v", tc) {
			continue
		}
		request := &http.Request{
			RemoteAddr: tc.addr,
			Header:     tc.headers,
		}
		assert.Equal(t, tc.expected, GetRealUserIP(request, trustedProxies), "failed for %+v", tc)
	}
}

func TestClientMessageToSessionIdWhileDisconnected(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	client2.Close()
	assert.NoError(client2.WaitForClientRemoved(ctx))

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	// The two chat messages should get combined into one when receiving pending messages.
	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data1 map[string]interface{}
	require.NoError(json.Unmarshal([]byte(chat_refresh), &data1))
	client1.SendMessage(recipient2, data1) // nolint
	client1.SendMessage(recipient2, data1) // nolint

	// Simulate some time until client resumes the session.
	time.Sleep(10 * time.Millisecond)

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloResume(hello2.Hello.ResumeId))
	if hello3, err := client2.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId+"2", hello3.Hello.UserId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)
	}

	var payload map[string]interface{}
	if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}
}

func TestRoomParticipantsListUpdateWhileDisconnected(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that somebody joined the call.
	users := []map[string]interface{}{
		{
			"sessionId": "the-session-id",
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users, users)
	assert.NoError(checkReceiveClientEvent(ctx, client2, "update", nil))

	client2.Close()
	assert.NoError(client2.WaitForClientRemoved(ctx))

	room.PublishUsersInCallChanged(users, users)

	// Give asynchronous events some time to be processed.
	time.Sleep(100 * time.Millisecond)

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data1 map[string]interface{}
	require.NoError(json.Unmarshal([]byte(chat_refresh), &data1))
	client1.SendMessage(recipient2, data1) // nolint

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloResume(hello2.Hello.ResumeId))
	if hello3, err := client2.RunUntilHello(ctx); assert.NoError(err) {
		assert.Equal(testDefaultUserId+"2", hello3.Hello.UserId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)
	}

	// The participants list update event is triggered again after the session resume.
	// TODO(jojo): Check contents of message and try with multiple users.
	assert.NoError(checkReceiveClientEvent(ctx, client2, "update", nil))

	var payload map[string]interface{}
	if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload); assert.NoError(err) {
		assert.Equal(data1, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}
}

func TestClientTakeoverRoomSession(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			RunTestClientTakeoverRoomSession(t)
		})
	}
}

func RunTestClientTakeoverRoomSession(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	var hub1 *Hub
	var hub2 *Hub
	var server1 *httptest.Server
	var server2 *httptest.Server
	if isLocalTest(t) {
		hub1, _, _, server1 = CreateHubForTest(t)

		hub2 = hub1
		server2 = server1
	} else {
		hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
	}

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room-takeover-room-session"
	roomSessionid := "room-session-id"
	roomMsg, err := client1.JoinRoomWithRoomSession(ctx, roomId, roomSessionid)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	hubRoom := hub1.getRoom(roomId)
	require.NotNil(hubRoom, "Room %s does not exist", roomId)

	session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
	require.NotNil(session1, "There should be a session %s", hello1.Hello.SessionId)

	client3 := NewTestClient(t, server2, hub2)
	defer client3.CloseWithBye()

	require.NoError(client3.SendHello(testDefaultUserId + "3"))

	hello3, err := client3.RunUntilHello(ctx)
	require.NoError(err)

	roomMsg, err = client3.JoinRoomWithRoomSession(ctx, roomId, roomSessionid+"other")
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Wait until both users have joined.
	WaitForUsersJoined(ctx, t, client1, hello1, client3, hello3)

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	roomMsg, err = client2.JoinRoomWithRoomSession(ctx, roomId, roomSessionid)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// The first client got disconnected with a reason in a "Bye" message.
	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("bye", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Bye, "%+v", msg) {
			assert.Equal("room_session_reconnected", msg.Bye.Reason, "%+v", msg)
		}
	}

	if msg, err := client1.RunUntilMessage(ctx); err == nil {
		assert.Fail("Expected error but received %+v", msg)
	} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		assert.Fail("Expected close error but received %+v", err)
	}

	// The first session has been closed
	session1 = hub1.GetSessionByPublicId(hello1.Hello.SessionId)
	assert.Nil(session1, "The session %s should have been removed", hello1.Hello.SessionId)

	// The new client will receive "joined" events for the existing client3 and
	// himself.
	assert.NoError(client2.RunUntilJoined(ctx, hello3.Hello, hello2.Hello))

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client2.RunUntilMessage(ctx2); err == nil {
		assert.Fail("Expected no message, got %+v", message)
	} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		assert.NoError(err)
	}

	// The permanently connected client will receive a "left" event from the
	// overridden session and a "joined" for the new session. In that order as
	// both were on the same server.
	assert.NoError(client3.RunUntilLeft(ctx, hello1.Hello))
	assert.NoError(client3.RunUntilJoined(ctx, hello2.Hello))
}

func TestClientSendOfferPermissions(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 1 is the moderator
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_MEDIA, PERMISSION_MAY_PUBLISH_SCREEN})
	// Client 2 is a guest participant.
	session2.SetPermissions([]Permission{})

	// Client 2 may not send an offer (he doesn't have the necessary permissions).
	require.NoError(client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "12345",
		RoomType: "screen",
	}))

	msg, err := client2.RunUntilMessage(ctx)
	require.NoError(err)
	require.NoError(checkMessageError(msg, "not_allowed"))

	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "12345",
		RoomType: "screen",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

	// Client 1 may send an offer.
	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "54321",
		RoomType: "screen",
	}))

	// The sender won't get a reply...
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client1.RunUntilMessage(ctx2); err == nil {
		assert.Fail("Expected no message, got %+v", message)
	} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		assert.NoError(err)
	}

	// ...but the other peer will get an offer.
	require.NoError(client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo))
}

func TestClientSendOfferPermissionsAudioOnly(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)

	// Client is allowed to send audio only.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_AUDIO})

	// Client may not send an offer with audio and video.
	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	msg, err := client1.RunUntilMessage(ctx)
	require.NoError(err)
	require.NoError(checkMessageError(msg, "not_allowed"))

	// Client may send an offer (audio only).
	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioOnly,
		},
	}))

	require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioOnly))
}

func TestClientSendOfferPermissionsAudioVideo(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)

	// Client is allowed to send audio and video.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_AUDIO, PERMISSION_MAY_PUBLISH_VIDEO})

	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

	// Client is no longer allowed to send video, this will stop the publisher.
	msg := &BackendServerRoomRequest{
		Type: "participants",
		Participants: &BackendRoomParticipantsRequest{
			Changed: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello1.Hello.SessionId,
					"permissions": []Permission{PERMISSION_MAY_PUBLISH_AUDIO},
				},
			},
			Users: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello1.Hello.SessionId,
					"permissions": []Permission{PERMISSION_MAY_PUBLISH_AUDIO},
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	ctx2, cancel2 := context.WithTimeout(ctx, time.Second)
	defer cancel2()

	pubs := mcu.GetPublishers()
	require.Len(pubs, 1)

loop:
	for {
		if err := ctx2.Err(); err != nil {
			assert.NoError(err, "publisher was not closed")
			break
		}

		for _, pub := range pubs {
			if pub.isClosed() {
				break loop
			}
		}

		// Give some time to async processing.
		time.Sleep(time.Millisecond)
	}
}

func TestClientSendOfferPermissionsAudioVideoMedia(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)

	// Client is allowed to send audio and video.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_MEDIA})

	// Client may send an offer (audio and video).
	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

	// Client is no longer allowed to send video, this will stop the publisher.
	msg := &BackendServerRoomRequest{
		Type: "participants",
		Participants: &BackendRoomParticipantsRequest{
			Changed: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello1.Hello.SessionId,
					"permissions": []Permission{PERMISSION_MAY_PUBLISH_MEDIA, PERMISSION_MAY_CONTROL},
				},
			},
			Users: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello1.Hello.SessionId,
					"permissions": []Permission{PERMISSION_MAY_PUBLISH_MEDIA, PERMISSION_MAY_CONTROL},
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

	pubs := mcu.GetPublishers()
	require.Len(pubs, 1)

loop:
	for {
		if err := ctx2.Err(); err != nil {
			if err != context.DeadlineExceeded {
				assert.Fail("error while waiting for publisher")
			}
			break
		}

		for _, pub := range pubs {
			if !assert.False(pub.isClosed(), "publisher was closed") {
				break loop
			}
		}

		// Give some time to async processing.
		time.Sleep(time.Millisecond)
	}
}

func TestClientRequestOfferNotInRoom(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server
			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			mcu, err := NewTestMCU()
			require.NoError(err)
			require.NoError(mcu.Start(ctx))
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId + "1"))

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client1.JoinRoomWithRoomSession(ctx, roomId, "roomsession1")
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

			require.NoError(client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: "screen",
				Payload: map[string]interface{}{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}))

			require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

			// Client 2 may not request an offer (he is not in the room yet).
			require.NoError(client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg, err := client2.RunUntilMessage(ctx)
			require.NoError(err)
			require.NoError(checkMessageError(msg, "not_allowed"))

			roomMsg, err = client2.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			require.NoError(client1.RunUntilJoined(ctx, hello2.Hello))
			require.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

			// Client 2 may not request an offer (he is not in the call yet).
			require.NoError(client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg, err = client2.RunUntilMessage(ctx)
			require.NoError(err)
			require.NoError(checkMessageError(msg, "not_allowed"))

			// Simulate request from the backend that somebody joined the call.
			users1 := []map[string]interface{}{
				{
					"sessionId": hello2.Hello.SessionId,
					"inCall":    1,
				},
			}
			room2 := hub2.getRoom(roomId)
			require.NotNil(room2, "Could not find room %s", roomId)
			room2.PublishUsersInCallChanged(users1, users1)
			assert.NoError(checkReceiveClientEvent(ctx, client1, "update", nil))
			assert.NoError(checkReceiveClientEvent(ctx, client2, "update", nil))

			// Client 2 may not request an offer (recipient is not in the call yet).
			require.NoError(client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg, err = client2.RunUntilMessage(ctx)
			require.NoError(err)
			require.NoError(checkMessageError(msg, "not_allowed"))

			// Simulate request from the backend that somebody joined the call.
			users2 := []map[string]interface{}{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
			}
			room1 := hub1.getRoom(roomId)
			require.NotNil(room1, "Could not find room %s", roomId)
			room1.PublishUsersInCallChanged(users2, users2)
			assert.NoError(checkReceiveClientEvent(ctx, client1, "update", nil))
			assert.NoError(checkReceiveClientEvent(ctx, client2, "update", nil))

			// Client 2 may request an offer now (both are in the same room and call).
			require.NoError(client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			require.NoError(client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo))

			require.NoError(client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "answer",
				Sid:      "12345",
				RoomType: "screen",
				Payload: map[string]interface{}{
					"sdp": MockSdpAnswerAudioAndVideo,
				},
			}))

			// The sender won't get a reply...
			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			if message, err := client2.RunUntilMessage(ctx2); err == nil {
				assert.Fail("Expected no message, got %+v", message)
			} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				assert.NoError(err)
			}
		})
	}
}

func TestNoSendBetweenSessionsOnDifferentBackends(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	// Clients can't send messages to sessions connected from other backends.
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	require.NoError(client1.SendHelloParams(server.URL+"/one", HelloVersionV1, "client", nil, params1))
	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", HelloVersionV1, "client", nil, params2))
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	recipient1 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2) // nolint

	var payload string
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}
}

func TestNoSameRoomOnDifferentBackends(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	require.NoError(client1.SendHelloParams(server.URL+"/one", HelloVersionV1, "client", nil, params1))
	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", HelloVersionV1, "client", nil, params2))
	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)
	if msg1, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(client1.checkMessageJoined(msg1, hello1.Hello))
	}

	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)
	if msg2, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(client2.checkMessageJoined(msg2, hello2.Hello))
	}

	hub.ru.RLock()
	var rooms []*Room
	for _, room := range hub.rooms {
		defer room.Close()
		rooms = append(rooms, room)
	}
	hub.ru.RUnlock()

	if assert.Len(rooms, 2) {
		if rooms[0].IsEqual(rooms[1]) {
			assert.Fail("Rooms should be different: %+v", rooms)
		}
	}

	recipient := MessageClientMessageRecipient{
		Type: "room",
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient, data2) // nolint

	var payload string
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err == nil {
		assert.Fail("Expected no payload, got %+v", payload)
	} else {
		assert.ErrorIs(err, ErrNoMessageReceived)
	}
}

func TestClientSendOffer(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server
			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			mcu, err := NewTestMCU()
			require.NoError(err)
			require.NoError(mcu.Start(ctx))
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId + "1"))

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client1.JoinRoomWithRoomSession(ctx, roomId, "roomsession1")
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg, err = client2.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			require.NoError(client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "12345",
				RoomType: "video",
				Payload: map[string]interface{}{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}))

			require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

			require.NoError(client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "sendoffer",
				RoomType: "video",
			}))

			// The sender won't get a reply...
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			if message, err := client1.RunUntilMessage(ctx2); err == nil {
				assert.Fail("Expected no message, got %+v", message)
			} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				assert.NoError(err)
			}

			// ...but the other peer will get an offer.
			require.NoError(client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo))
		})
	}
}

func TestClientUnshareScreen(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)

	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "screen",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioOnly,
		},
	}))

	require.NoError(client1.RunUntilAnswer(ctx, MockSdpAnswerAudioOnly))

	publisher := mcu.GetPublisher(hello1.Hello.SessionId)
	require.NotNil(publisher, "No publisher for %s found", hello1.Hello.SessionId)
	require.False(publisher.isClosed(), "Publisher %s should not be closed", hello1.Hello.SessionId)

	old := cleanupScreenPublisherDelay
	cleanupScreenPublisherDelay = time.Millisecond
	defer func() {
		cleanupScreenPublisherDelay = old
	}()

	require.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "unshareScreen",
		Sid:      "54321",
		RoomType: "screen",
	}))

	time.Sleep(10 * time.Millisecond)

	require.True(publisher.isClosed(), "Publisher %s should be closed", hello1.Hello.SessionId)
}

func TestVirtualClientSessions(t *testing.T) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server
			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHello(testDefaultUserId))

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)

			roomId := "test-room"
			_, err = client1.JoinRoom(ctx, roomId)
			require.NoError(err)

			assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloInternal())

			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

			_, err = client2.JoinRoom(ctx, roomId)
			require.NoError(err)

			assert.NoError(client1.RunUntilJoined(ctx, hello2.Hello))

			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				if msg, err := checkMessageParticipantsInCall(msg); assert.NoError(err) {
					if assert.Len(msg.Users, 1) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.Equal(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(3, msg.Users[0]["inCall"], "%+v", msg)
					}
				}
			}

			_, unexpected, err := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello)
			assert.NoError(err)

			if len(unexpected) == 0 {
				if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
					unexpected = append(unexpected, msg)
				}
			}

			require.Len(unexpected, 1)
			if msg, err := checkMessageParticipantsInCall(unexpected[0]); assert.NoError(err) {
				if assert.Len(msg.Users, 1) {
					assert.Equal(true, msg.Users[0]["internal"])
					assert.Equal(hello2.Hello.SessionId, msg.Users[0]["sessionId"])
					assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"])
				}
			}

			calledCtx, calledCancel := context.WithTimeout(ctx, time.Second)

			virtualSessionId := "virtual-session-id"
			virtualUserId := "virtual-user-id"
			generatedSessionId := GetVirtualSessionId(session2, virtualSessionId)

			setSessionRequestHandler(t, func(request *BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("add", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})

			require.NoError(client2.SendInternalAddSession(&AddSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},
				UserId: virtualUserId,
				Flags:  FLAG_MUTED_SPEAKING,
			}))
			<-calledCtx.Done()
			if err := calledCtx.Err(); err != nil {
				require.ErrorIs(err, context.Canceled)
			}

			virtualSessions := session2.GetVirtualSessions()
			for len(virtualSessions) == 0 {
				time.Sleep(time.Millisecond)
				virtualSessions = session2.GetVirtualSessions()
			}

			virtualSession := virtualSessions[0]
			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				assert.NoError(client1.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId))
			}

			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				if msg, err := checkMessageParticipantsInCall(msg); assert.NoError(err) {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.Equal(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.Equal(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				if flags, err := checkMessageParticipantFlags(msg); assert.NoError(err) {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				assert.NoError(client2.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId))
			}

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				if msg, err := checkMessageParticipantsInCall(msg); assert.NoError(err) {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.Equal(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.Equal(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				if flags, err := checkMessageParticipantFlags(msg); assert.NoError(err) {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			updatedFlags := uint32(0)
			require.NoError(client2.SendInternalUpdateSession(&UpdateSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				Flags: &updatedFlags,
			}))

			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				if flags, err := checkMessageParticipantFlags(msg); assert.NoError(err) {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(0, flags.Flags)
				}
			}

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				if flags, err := checkMessageParticipantFlags(msg); assert.NoError(err) {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(0, flags.Flags)
				}
			}

			calledCtx, calledCancel = context.WithTimeout(ctx, time.Second)

			setSessionRequestHandler(t, func(request *BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("remove", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})

			// Messages to virtual sessions are sent to the associated client session.
			virtualRecipient := MessageClientMessageRecipient{
				Type:      "session",
				SessionId: virtualSession.PublicId(),
			}

			data := "message-to-virtual"
			client1.SendMessage(virtualRecipient, data) // nolint

			var payload string
			var sender *MessageServerMessageSender
			var recipient *MessageClientMessageRecipient
			if err := checkReceiveClientMessageWithSenderAndRecipient(ctx, client2, "session", hello1.Hello, &payload, &sender, &recipient); assert.NoError(err) {
				assert.Equal(virtualSessionId, recipient.SessionId, "%+v", recipient)
				assert.Equal(data, payload)
			}

			data = "control-to-virtual"
			client1.SendControl(virtualRecipient, data) // nolint

			if err := checkReceiveClientControlWithSenderAndRecipient(ctx, client2, "session", hello1.Hello, &payload, &sender, &recipient); assert.NoError(err) {
				assert.Equal(virtualSessionId, recipient.SessionId, "%+v", recipient)
				assert.Equal(data, payload)
			}

			require.NoError(client2.SendInternalRemoveSession(&RemoveSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				UserId: virtualUserId,
			}))
			<-calledCtx.Done()
			if err := calledCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				require.NoError(err)
			}

			if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
				assert.NoError(client1.checkMessageRoomLeaveSession(msg, virtualSession.PublicId()))
			}

			if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
				assert.NoError(client1.checkMessageRoomLeaveSession(msg, virtualSession.PublicId()))
			}
		})
	}
}

func DoTestSwitchToOne(t *testing.T, details map[string]interface{}) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			roomSessionId1 := "roomsession1"
			roomId1 := "test-room"
			roomMsg, err := client1.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId1)
			require.NoError(err)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			roomSessionId2 := "roomsession2"
			roomMsg, err = client2.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId2)
			require.NoError(err)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))
			assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			if details != nil {
				sessions, err = json.Marshal(map[string]interface{}{
					roomSessionId1: details,
				})
				require.NoError(err)
			} else {
				sessions, err = json.Marshal([]string{
					roomSessionId1,
				})
				require.NoError(err)
			}

			// Notify first client to switch to different room.
			msg := &BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &BackendRoomSwitchToMessageRequest{
					RoomId:   roomId2,
					Sessions: sessions,
				},
			}

			data, err := json.Marshal(msg)
			require.NoError(err)
			res, err := performBackendRequest(server2.URL+"/api/v1/room/"+roomId1, data)
			require.NoError(err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			assert.NoError(err)
			assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

			var detailsData json.RawMessage
			if details != nil {
				detailsData, err = json.Marshal(details)
				require.NoError(err)
			}
			_, err = client1.RunUntilSwitchTo(ctx, roomId2, detailsData)
			assert.NoError(err)

			// The other client will not receive a message.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			if message, err := client2.RunUntilMessage(ctx2); err == nil {
				assert.Fail("Expected no message, got %+v", message)
			} else if err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				assert.NoError(err)
			}
		})
	}
}

func TestSwitchToOneMap(t *testing.T) {
	DoTestSwitchToOne(t, map[string]interface{}{
		"foo": "bar",
	})
}

func TestSwitchToOneList(t *testing.T) {
	DoTestSwitchToOne(t, nil)
}

func DoTestSwitchToMultiple(t *testing.T, details1 map[string]interface{}, details2 map[string]interface{}) {
	CatchLogForTest(t)
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHello(testDefaultUserId + "1"))
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHello(testDefaultUserId + "2"))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			require.NoError(err)
			hello2, err := client2.RunUntilHello(ctx)
			require.NoError(err)

			roomSessionId1 := "roomsession1"
			roomId1 := "test-room"
			roomMsg, err := client1.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId1)
			require.NoError(err)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			roomSessionId2 := "roomsession2"
			roomMsg, err = client2.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId2)
			require.NoError(err)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))
			assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			if details1 != nil || details2 != nil {
				sessions, err = json.Marshal(map[string]interface{}{
					roomSessionId1: details1,
					roomSessionId2: details2,
				})
				require.NoError(err)
			} else {
				sessions, err = json.Marshal([]string{
					roomSessionId1,
					roomSessionId2,
				})
				require.NoError(err)
			}

			msg := &BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &BackendRoomSwitchToMessageRequest{
					RoomId:   roomId2,
					Sessions: sessions,
				},
			}

			data, err := json.Marshal(msg)
			require.NoError(err)
			res, err := performBackendRequest(server2.URL+"/api/v1/room/"+roomId1, data)
			require.NoError(err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			assert.NoError(err)
			assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

			var detailsData1 json.RawMessage
			if details1 != nil {
				detailsData1, err = json.Marshal(details1)
				require.NoError(err)
			}
			_, err = client1.RunUntilSwitchTo(ctx, roomId2, detailsData1)
			assert.NoError(err)

			var detailsData2 json.RawMessage
			if details2 != nil {
				detailsData2, err = json.Marshal(details2)
				require.NoError(err)
			}
			_, err = client2.RunUntilSwitchTo(ctx, roomId2, detailsData2)
			assert.NoError(err)
		})
	}
}

func TestSwitchToMultipleMap(t *testing.T) {
	DoTestSwitchToMultiple(t, map[string]interface{}{
		"foo": "bar",
	}, map[string]interface{}{
		"bar": "baz",
	})
}

func TestSwitchToMultipleList(t *testing.T) {
	DoTestSwitchToMultiple(t, nil, nil)
}

func TestSwitchToMultipleMixed(t *testing.T) {
	DoTestSwitchToMultiple(t, map[string]interface{}{
		"foo": "bar",
	}, nil)
}

func TestGeoipOverrides(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	country1 := "DE"
	country2 := "IT"
	country3 := "site1"
	hub, _, _, _ := CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		conf, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		conf.AddOption("geoip-overrides", "10.1.0.0/16", country1)
		conf.AddOption("geoip-overrides", "10.2.0.0/16", country2)
		conf.AddOption("geoip-overrides", "192.168.10.20", country3)
		return conf, err
	})

	assert.Equal(loopback, hub.OnLookupCountry(&Client{addr: "127.0.0.1"}))
	assert.Equal(unknownCountry, hub.OnLookupCountry(&Client{addr: "8.8.8.8"}))
	assert.Equal(country1, hub.OnLookupCountry(&Client{addr: "10.1.1.2"}))
	assert.Equal(country2, hub.OnLookupCountry(&Client{addr: "10.2.1.2"}))
	assert.Equal(strings.ToUpper(country3), hub.OnLookupCountry(&Client{addr: "192.168.10.20"}))
}

func TestDialoutStatus(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	internalClient := NewTestClient(t, server, hub)
	defer internalClient.CloseWithBye()
	require.NoError(internalClient.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := internalClient.RunUntilHello(ctx)
	require.NoError(err)

	roomId := "12345"
	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	_, err = client.JoinRoom(ctx, roomId)
	require.NoError(err)

	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	callId := "call-123"

	stopped := make(chan struct{})
	go func(client *TestClient) {
		defer close(stopped)

		msg, err := client.RunUntilMessage(ctx)
		if !assert.NoError(err) {
			return
		}

		if !assert.Equal("internal", msg.Type, "%+v", msg) ||
			!assert.NotNil(msg.Internal, "%+v", msg) ||
			!assert.Equal("dialout", msg.Internal.Type, "%+v", msg) ||
			!assert.NotNil(msg.Internal.Dialout, "%+v", msg) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)

		response := &ClientMessage{
			Id:   msg.Id,
			Type: "internal",
			Internal: &InternalClientMessage{
				Type: "dialout",
				Dialout: &DialoutInternalClientMessage{
					Type:   "status",
					RoomId: msg.Internal.Dialout.RoomId,
					Status: &DialoutStatusInternalClientMessage{
						Status: "accepted",
						CallId: callId,
					},
				},
			},
		}
		assert.NoError(client.WriteJSON(response))
	}(internalClient)

	defer func() {
		<-stopped
	}()

	msg := &BackendServerRoomRequest{
		Type: "dialout",
		Dialout: &BackendRoomDialoutRequest{
			Number: "+1234567890",
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	require.Equal(http.StatusOK, res.StatusCode, "Expected success, got %s", string(body))

	var response BackendServerRoomResponse
	if assert.NoError(json.Unmarshal(body, &response)) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) {
			assert.Nil(response.Dialout.Error, "expected dialout success, got %s", string(body))
			assert.Equal(callId, response.Dialout.CallId)
		}
	}

	key := "callstatus_" + callId
	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "accepted",
		}, nil))
	}

	require.NoError(internalClient.SendInternalDialout(&DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "ringing",
		},
	}))

	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "ringing",
		}, map[string]interface{}{
			"callid": callId,
			"status": "accepted",
		}))
	}

	old := removeCallStatusTTL
	defer func() {
		removeCallStatusTTL = old
	}()
	removeCallStatusTTL = 500 * time.Millisecond

	clearedCause := "cleared-call"
	require.NoError(internalClient.SendInternalDialout(&DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "cleared",
			Cause:  clearedCause,
		},
	}))

	if msg, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		}, map[string]interface{}{
			"callid": callId,
			"status": "ringing",
		}))
	}

	ctx2, cancel := context.WithTimeout(ctx, removeCallStatusTTL*2)
	defer cancel()

	if msg, err := client.RunUntilMessage(ctx2); assert.NoError(err) {
		assert.NoError(checkMessageTransientRemove(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		}))
	}
}

func TestGracefulShutdownInitial(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	hub, _, _, _ := CreateHubForTest(t)

	hub.ScheduleShutdown()
	<-hub.ShutdownChannel()
}

func TestGracefulShutdownOnBye(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	_, err := client.RunUntilHello(ctx)
	require.NoError(err)

	hub.ScheduleShutdown()
	select {
	case <-hub.ShutdownChannel():
		assert.Fail("should not have shutdown")
	case <-time.After(100 * time.Millisecond):
	}

	client.CloseWithBye()

	select {
	case <-hub.ShutdownChannel():
	case <-time.After(100 * time.Millisecond):
		assert.Fail("should have shutdown")
	}
}

func TestGracefulShutdownOnExpiration(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	_, err := client.RunUntilHello(ctx)
	require.NoError(err)

	hub.ScheduleShutdown()
	select {
	case <-hub.ShutdownChannel():
		assert.Fail("should not have shutdown")
	case <-time.After(100 * time.Millisecond):
	}

	client.Close()
	select {
	case <-hub.ShutdownChannel():
		assert.Fail("should not have shutdown")
	case <-time.After(100 * time.Millisecond):
	}

	performHousekeeping(hub, time.Now().Add(sessionExpireDuration+time.Second))

	select {
	case <-hub.ShutdownChannel():
	case <-time.After(100 * time.Millisecond):
		assert.Fail("should have shutdown")
	}
}
