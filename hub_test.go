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
	"fmt"
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
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/container"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
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

func getTestConfigWithMultipleUrls(server *httptest.Server) (*goconf.ConfigFile, error) {
	config, err := getTestConfig(server)
	if err != nil {
		return nil, err
	}

	config.RemoveOption("backend", "allowed")
	config.RemoveOption("backend", "secret")
	config.AddOption("backend", "backends", "backend1")

	config.AddOption("backend1", "urls", strings.Join([]string{server.URL + "/one", server.URL + "/two/"}, ","))
	config.AddOption("backend1", "secret", string(testBackendSecret))
	return config, nil
}

func CreateHubForTestWithConfig(t *testing.T, getConfigFunc func(*httptest.Server) (*goconf.ConfigFile, error)) (*Hub, AsyncEvents, *mux.Router, *httptest.Server) {
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
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
	h, err := NewHub(ctx, config, events, nil, nil, nil, r, "no-version")
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, h, "no-version")
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

func CreateHubWithMultipleUrlsForTest(t *testing.T) (*Hub, AsyncEvents, *mux.Router, *httptest.Server) {
	h, events, r, server := CreateHubForTestWithConfig(t, getTestConfigWithMultipleUrls)
	registerBackendHandlerUrl(t, r, "/one")
	registerBackendHandlerUrl(t, r, "/two")
	return h, events, r, server
}

func CreateClusteredHubsForTestWithConfig(t *testing.T, getConfigFunc func(*httptest.Server) (*goconf.ConfigFile, error)) (*Hub, *Hub, *mux.Router, *mux.Router, *httptest.Server, *httptest.Server) {
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
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

	nats1, _ := nats.StartLocalServer(t)
	var nats2 *server.Server
	if strings.Contains(t.Name(), "Federation") {
		nats2, _ = nats.StartLocalServer(t)
	} else {
		nats2 = nats1
	}
	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	if strings.Contains(t.Name(), "Federation") {
		// Signaling servers should not form a cluster in federation tests.
		addr1, addr2 = addr2, addr1
	}

	events1, err := NewAsyncEvents(ctx, nats1.ClientURL())
	require.NoError(err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(events1.Close(ctx))
	})
	config1, err := getConfigFunc(server1)
	require.NoError(err)
	client1, _ := NewGrpcClientsForTest(t, addr2, nil)
	h1, err := NewHub(ctx, config1, events1, grpcServer1, client1, nil, r1, "no-version")
	require.NoError(err)
	b1, err := NewBackendServer(ctx, config1, h1, "no-version")
	require.NoError(err)
	events2, err := NewAsyncEvents(ctx, nats2.ClientURL())
	require.NoError(err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(events2.Close(ctx))
	})
	config2, err := getConfigFunc(server2)
	require.NoError(err)
	client2, _ := NewGrpcClientsForTest(t, addr1, nil)
	h2, err := NewHub(ctx, config2, events2, grpcServer2, client2, nil, r2, "no-version")
	require.NoError(err)
	b2, err := NewBackendServer(ctx, config2, h2, "no-version")
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
		federatedSessions := len(h.federatedSessions)
		federationClients := len(h.federatedSessions)
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
			federatedSessions == 0 &&
			federationClients == 0 &&
			readActive == 0 &&
			writeActive == 0 {
			break
		}

		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.ru.Lock()
			test.DumpGoroutines("", os.Stderr)
			assert.Fail(t, "Error waiting for hub to terminate", "clients %+v / rooms %+v / sessions %+v / remoteSessions %v / federatedSessions %v / federationClients %v / %d read / %d write: %s",
				h.clients,
				h.rooms,
				h.sessions,
				remoteSessions,
				federatedSessions,
				federationClients,
				readActive,
				writeActive,
				ctx.Err(),
			)
			h.ru.Unlock()
			h.mu.Unlock()
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func validateBackendChecksum(t *testing.T, f func(http.ResponseWriter, *http.Request, *talk.BackendClientRequest) *talk.BackendClientResponse) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		assert := assert.New(t)
		body, err := io.ReadAll(r.Body)
		assert.NoError(err)

		rnd := r.Header.Get(talk.HeaderBackendSignalingRandom)
		checksum := r.Header.Get(talk.HeaderBackendSignalingChecksum)
		if rnd == "" || checksum == "" {
			assert.Fail("No checksum headers found", "request to %s", r.URL)
		}

		if verify := talk.CalculateBackendChecksum(rnd, body, testBackendSecret); verify != checksum {
			assert.Fail("Backend checksum verification failed", "request to %s", r.URL)
		}

		var request talk.BackendClientRequest
		assert.NoError(json.Unmarshal(body, &request))

		response := f(w, r, &request)
		if response == nil {
			// Function already returned a response.
			return
		}

		data, err := json.Marshal(response)
		assert.NoError(err)

		if r.Header.Get("OCS-APIRequest") != "" {
			var ocs talk.OcsResponse
			ocs.Ocs = &talk.OcsBody{
				Meta: talk.OcsMeta{
					Status:     "ok",
					StatusCode: http.StatusOK,
					Message:    http.StatusText(http.StatusOK),
				},
				Data: data,
			}
			data, err = json.Marshal(ocs)
			assert.NoError(err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data) // nolint
	}
}

func processAuthRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *talk.BackendClientRequest) *talk.BackendClientResponse {
	require := require.New(t)
	if request.Type != "auth" || request.Auth == nil {
		require.Fail("Expected an auth backend request", "received %+v", request)
	}

	var params TestBackendClientAuthParams
	if len(request.Auth.Params) > 0 {
		require.NoError(json.Unmarshal(request.Auth.Params, &params))
	}
	switch params.UserId {
	case "":
		params.UserId = testDefaultUserId
	case authAnonymousUserId:
		params.UserId = ""
	}

	response := &talk.BackendClientResponse{
		Type: "auth",
		Auth: &talk.BackendClientAuthResponse{
			Version: talk.BackendVersion,
			UserId:  params.UserId,
		},
	}
	userdata := map[string]string{
		"displayname": "Displayname " + params.UserId,
	}
	data, _ := json.Marshal(userdata)
	response.Auth.User = data
	return response
}

func processRoomRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *talk.BackendClientRequest) *talk.BackendClientResponse {
	require := require.New(t)
	assert := assert.New(t)
	if request.Type != "room" || request.Room == nil {
		require.Fail("Expected an room backend request", "received %+v", request)
	}

	switch request.Room.RoomId {
	case "test-room-slow":
		time.Sleep(100 * time.Millisecond)
	case "test-room-takeover-room-session":
		// Additional checks for testcase "TestClientTakeoverRoomSession"
		if request.Room.Action == "leave" && request.Room.UserId == "test-userid1" {
			assert.Fail("Should not receive \"leave\" event for first user", "received %+v", request.Room)
		}
	case "test-invalid-room":
		response := &talk.BackendClientResponse{
			Type: "error",
			Error: &api.Error{
				Code:    "no_such_room",
				Message: "The user is not invited to this room.",
			},
		}
		return response
	}

	if strings.Contains(t.Name(), "Federation") {
		// Check additional fields present for federated sessions.
		if strings.Contains(string(request.Room.SessionId), "@federated") {
			assert.Equal(api.ActorTypeFederatedUsers, request.Room.ActorType)
			assert.NotEmpty(request.Room.ActorId)
		} else {
			assert.Empty(request.Room.ActorType)
			assert.Empty(request.Room.ActorId)
		}
	} else if strings.Contains(t.Name(), "VirtualSessionActorInformation") && request.Room.UserId == "user1" {
		if request.Room.Action == "" || request.Room.Action == "join" || request.Room.Action == "leave" {
			assert.Equal("actor-type", request.Room.ActorType, "failed for %+v", request.Room)
			assert.Equal("actor-id", request.Room.ActorId, "failed for %+v", request.Room)
		}
	}

	// Allow joining any room.
	response := &talk.BackendClientResponse{
		Type: "room",
		Room: &talk.BackendClientRoomResponse{
			Version:    talk.BackendVersion,
			RoomId:     request.Room.RoomId,
			Properties: testRoomProperties,
		},
	}
	switch request.Room.RoomId {
	case "test-room-with-sessiondata":
		data := map[string]string{
			"userid": "userid-from-sessiondata",
		}
		tmp, _ := json.Marshal(data)
		response.Room.Session = tmp
	case "test-room-initial-permissions":
		permissions := []api.Permission{api.PERMISSION_MAY_PUBLISH_AUDIO}
		response.Room.Permissions = &permissions
	}
	return response
}

var (
	sessionRequestHander struct {
		sync.Mutex
		// +checklocks:Mutex
		handlers map[*testing.T]func(*talk.BackendClientSessionRequest)
	}
)

func setSessionRequestHandler(t *testing.T, f func(*talk.BackendClientSessionRequest)) {
	sessionRequestHander.Lock()
	defer sessionRequestHander.Unlock()
	if sessionRequestHander.handlers == nil {
		sessionRequestHander.handlers = make(map[*testing.T]func(*talk.BackendClientSessionRequest))
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

func processSessionRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *talk.BackendClientRequest) *talk.BackendClientResponse {
	if request.Type != "session" || request.Session == nil {
		require.Fail(t, "Expected an session backend request", "received %+v", request)
	}

	sessionRequestHander.Lock()
	defer sessionRequestHander.Unlock()
	if f, found := sessionRequestHander.handlers[t]; found {
		f(request.Session)
	}

	response := &talk.BackendClientResponse{
		Type: "session",
		Session: &talk.BackendClientSessionResponse{
			Version: talk.BackendVersion,
			RoomId:  request.Session.RoomId,
		},
	}
	return response
}

var (
	pingRequests internal.TestStorage[[]*talk.BackendClientRequest]
)

func getPingRequests(t *testing.T) []*talk.BackendClientRequest {
	entries, _ := pingRequests.Get(t)
	return entries
}

func clearPingRequests(t *testing.T) {
	pingRequests.Del(t)
}

func storePingRequest(t *testing.T, request *talk.BackendClientRequest) {
	entries, _ := pingRequests.Get(t)
	pingRequests.Set(t, append(entries, request))
}

func processPingRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *talk.BackendClientRequest) *talk.BackendClientResponse {
	if request.Type != "ping" || request.Ping == nil {
		require.Fail(t, "Expected an ping backend request", "received %+v", request)
	}

	if request.Ping.RoomId == "test-room-with-sessiondata" {
		if entries := request.Ping.Entries; assert.Len(t, entries, 1) {
			assert.Empty(t, entries[0].UserId)
		}
	}

	storePingRequest(t, request)

	response := &talk.BackendClientResponse{
		Type: "ping",
		Ping: &talk.BackendClientRingResponse{
			Version: talk.BackendVersion,
			RoomId:  request.Ping.RoomId,
		},
	}
	return response
}

type testAuthToken struct {
	PrivateKey string
	PublicKey  string
}

var (
	authTokens internal.TestStorage[testAuthToken]
)

func ensureAuthTokens(t *testing.T) (string, string) {
	require := require.New(t)

	if tokens, found := authTokens.Get(t); found {
		return tokens.PrivateKey, tokens.PublicKey
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
	publicKey := base64.StdEncoding.EncodeToString(public)

	authTokens.Set(t, testAuthToken{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	})
	return privateKey, publicKey
}

func getPrivateAuthToken(t *testing.T) (key any) {
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

func getPublicAuthToken(t *testing.T) (key any) {
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

var (
	skipV2Capabilities internal.TestStorage[bool]
)

func registerBackendHandlerUrl(t *testing.T, router *mux.Router, url string) {
	handleFunc := validateBackendChecksum(t, func(w http.ResponseWriter, r *http.Request, request *talk.BackendClientRequest) *talk.BackendClientResponse {
		assert.Regexp(t, "/ocs/v2\\.php/apps/spreed/api/v[\\d]/signaling/backend$", r.URL.Path, "invalid url for backend request %+v", request)

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
			require.Fail(t, "Unsupported request", "received: %+v", request)
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
		signaling := api.StringMap{
			"foo": "bar",
			"baz": 42,
		}
		config := api.StringMap{
			"signaling": signaling,
		}
		if strings.Contains(t.Name(), "MultiRoom") {
			signaling[talk.ConfigKeySessionPingLimit] = 2
		}
		skipV2, _ := skipV2Capabilities.Get(t)
		if (strings.Contains(t.Name(), "V2") && !skipV2) || strings.Contains(t.Name(), "Federation") {
			key := getPublicAuthToken(t)
			public, err := x509.MarshalPKIXPublicKey(key)
			assert.NoError(t, err)
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
				signaling[talk.ConfigKeyHelloV2TokenKey] = encoded
			} else {
				signaling[talk.ConfigKeyHelloV2TokenKey] = string(public)
			}
		}
		spreedCapa, err := json.Marshal(api.StringMap{
			"features": features,
			"config":   config,
		})
		assert.NoError(t, err)
		response := &talk.CapabilitiesResponse{
			Version: talk.CapabilitiesVersion{
				Major: 20,
			},
			Capabilities: map[string]json.RawMessage{
				"spreed": spreedCapa,
			},
		}

		data, err := json.Marshal(response)
		assert.NoError(t, err, "Could not marshal %+v", response)

		var ocs talk.OcsResponse
		ocs.Ocs = &talk.OcsBody{
			Meta: talk.OcsMeta{
				Status:     "ok",
				StatusCode: http.StatusOK,
				Message:    http.StatusText(http.StatusOK),
			},
			Data: data,
		}
		data, err = json.Marshal(ocs)
		assert.NoError(t, err)
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

func Benchmark_DecodePrivateSessionIdCached(b *testing.B) {
	require := require.New(b)
	decodeCaches := make([]*container.LruCache[*SessionIdData], 0, numDecodeCaches)
	for range numDecodeCaches {
		decodeCaches = append(decodeCaches, container.NewLruCache[*SessionIdData](decodeCacheSize))
	}
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	sid, err := codec.EncodePrivate(data)
	require.NoError(err, "could not create session id")
	hub := &Hub{
		sessionIds:   codec,
		decodeCaches: decodeCaches,
	}
	// Decode once to populate cache.
	require.NotNil(hub.decodePrivateSessionId(sid))
	for b.Loop() {
		hub.decodePrivateSessionId(sid)
	}
}

func Benchmark_DecodePublicSessionIdCached(b *testing.B) {
	require := require.New(b)
	decodeCaches := make([]*container.LruCache[*SessionIdData], 0, numDecodeCaches)
	for range numDecodeCaches {
		decodeCaches = append(decodeCaches, container.NewLruCache[*SessionIdData](decodeCacheSize))
	}
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	sid, err := codec.EncodePublic(data)
	require.NoError(err, "could not create session id")
	hub := &Hub{
		sessionIds:   codec,
		decodeCaches: decodeCaches,
	}
	// Decode once to populate cache.
	require.NotNil(hub.decodePublicSessionId(sid))
	for b.Loop() {
		hub.decodePublicSessionId(sid)
	}
}

func TestWebsocketFeatures(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, response, err := testClientDialer.DialContext(ctx, getWebsocketUrl(server.URL), nil)
	require.NoError(err)
	defer conn.Close() // nolint

	serverHeader := response.Header.Get("Server")
	assert.True(strings.HasPrefix(serverHeader, "nextcloud-spreed-signaling/"), "expected valid server header, got \"%s\"", serverHeader)
	features := response.Header.Get("X-Spreed-Signaling-Features")
	featuresList := make(map[string]bool)
	for f := range internal.SplitEntries(features, ",") {
		_, found := featuresList[f]
		assert.False(found, "duplicate feature id \"%s\" in \"%s\"", f, features)
		featuresList[f] = true
	}
	if len(featuresList) <= 1 {
		assert.Fail("expected valid features header", "received \"%s\"", features)
	}
	_, found := featuresList["hello-v2"]
	assert.True(found, "expected feature \"hello-v2\"", "received \"%s\"", features)

	assert.NoError(conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{}))
}

func TestInitialWelcome(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClientContext(ctx, t, server, hub)
	defer client.CloseWithBye()

	if msg, ok := client.RunUntilMessage(ctx); ok {
		assert.Equal("welcome", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Welcome, "%+v", msg) {
			assert.NotEmpty(msg.Welcome.Version, "%+v", msg)
			assert.NotEmpty(msg.Welcome.Features, "%+v", msg)
		}
	}
}

func TestExpectClientHello(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	// The server will send an error and close the connection if no "Hello"
	// is sent.
	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated due to the missing "Hello" request.
	hub.performHousekeeping(time.Now().Add(initialHelloTimeout + time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if message, ok := client.RunUntilMessage(ctx); ok {
		if checkMessageType(t, message, "bye") {
			assert.Equal("hello_timeout", message.Bye.Reason, "%+v", message.Bye)
		}
	}

	client.RunUntilClosed(ctx)
}

func TestExpectClientHelloUnsupportedVersion(t *testing.T) {
	t.Parallel()
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

	if message, ok := client.RunUntilMessage(ctx); ok {
		if checkMessageType(t, message, "error") {
			assert.Equal("invalid_hello_version", message.Error.Code)
		}
	}
}

func TestClientHelloV1(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
}

func TestClientHelloV2(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			require.NoError(client.SendHelloV2(testDefaultUserId))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if hello, ok := client.RunUntilHello(ctx); ok {
				assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)

				data := hub.decodePublicSessionId(hello.Hello.SessionId)
				require.NotNil(data, "Could not decode session id: %s", hello.Hello.SessionId)

				hub.mu.RLock()
				session := hub.sessions[data.Sid]
				hub.mu.RUnlock()
				require.NotNil(session, "Could not get session for id %+v", data)

				var userdata map[string]string
				require.NoError(json.Unmarshal(session.UserData(), &userdata))

				assert.Equal("Displayname "+testDefaultUserId, userdata["displayname"])
			}
		})
	}
}

func TestClientHelloV2_IssuedInFuture(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(tokenLeeway / 2)
			expiresAt := issuedAt.Add(time.Second)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if hello, ok := client.RunUntilHello(ctx); ok {
				assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}
		})
	}
}

func TestClientHelloV2_IssuedFarInFuture(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(tokenLeeway * 2)
			expiresAt := issuedAt.Add(time.Second)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client.RunUntilError(ctx, "token_not_valid_yet") // nolint
		})
	}
}

func TestClientHelloV2_Expired(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(-tokenLeeway * 3)
			expiresAt := time.Now().Add(-tokenLeeway * 2)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client.RunUntilError(ctx, "token_expired") // nolint
		})
	}
}

func TestClientHelloV2_IssuedAtMissing(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			var issuedAt time.Time
			expiresAt := time.Now().Add(time.Minute)
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client.RunUntilError(ctx, "token_not_valid_yet") // nolint
		})
	}
}

func TestClientHelloV2_ExpiresAtMissing(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now()
			var expiresAt time.Time
			require.NoError(client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client.RunUntilError(ctx, "token_expired") // nolint
		})
	}
}

func TestClientHelloV2_CachedCapabilities(t *testing.T) {
	t.Parallel()
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			// Simulate old-style Nextcloud without capabilities for Hello V2.
			skipV2Capabilities.Set(t, true)

			client1 := NewTestClient(t, server, hub)
			defer client1.CloseWithBye()

			require.NoError(client1.SendHelloV1(testDefaultUserId + "1"))

			hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
			assert.Equal(testDefaultUserId+"1", hello1.Hello.UserId, "%+v", hello1.Hello)
			assert.NotEmpty(hello1.Hello.SessionId, "%+v", hello1.Hello)

			// Simulate updated Nextcloud with capabilities for Hello V2.
			skipV2Capabilities.Set(t, false)

			client2 := NewTestClient(t, server, hub)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
			assert.Equal(testDefaultUserId+"2", hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2.Hello)
		})
	}
}

func TestClientHelloWithSpaces(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	userId := "test user with spaces"

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, hello := NewTestClientWithHello(ctx, t, server, hub, userId)
	assert.Equal(userId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
}

func TestClientHelloAllowAll(t *testing.T) {
	t.Parallel()
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

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
}

func TestClientHelloSessionLimit(t *testing.T) {
	t.Parallel()
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
			require.NoError(client.SendHelloParams(server1.URL+"/one", api.HelloVersionV1, "client", nil, params1))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if hello, ok := client.RunUntilHello(ctx); ok {
				assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}

			// The second client can't connect as it would exceed the session limit.
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			params2 := TestBackendClientAuthParams{
				UserId: testDefaultUserId + "2",
			}
			require.NoError(client2.SendHelloParams(server1.URL+"/one", api.HelloVersionV1, "client", nil, params2))

			client2.RunUntilError(ctx, "session_limit_exceeded") //nolint

			// The client can connect to a different backend.
			require.NoError(client2.SendHelloParams(server1.URL+"/two", api.HelloVersionV1, "client", nil, params2))

			if hello, ok := client2.RunUntilHello(ctx); ok {
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
			require.NoError(client3.SendHelloParams(server1.URL+"/one", api.HelloVersionV1, "client", nil, params3))

			if hello, ok := client3.RunUntilHello(ctx); ok {
				assert.Equal(testDefaultUserId+"3", hello.Hello.UserId, "%+v", hello.Hello)
				assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			}
		})
	}
}

func TestSessionIdsUnordered(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	var mu sync.Mutex
	var publicSessionIds []api.PublicSessionId
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			_, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId) // nolint:testifylint
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)

			data := hub.decodePublicSessionId(hello.Hello.SessionId)
			if !assert.NotNil(data, "Could not decode session id: %s", hello.Hello.SessionId) {
				return
			}

			hub.mu.RLock()
			session := hub.sessions[data.Sid]
			hub.mu.RUnlock()
			if !assert.NotNil(session, "Could not get session for id %+v", data) {
				return
			}

			mu.Lock()
			publicSessionIds = append(publicSessionIds, session.PublicId())
			mu.Unlock()
		}()
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(publicSessionIds, "no session ids decoded")

	larger := 0
	smaller := 0
	var prevSid api.PublicSessionId
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
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if hello2, ok := client.RunUntilHello(ctx); ok {
		assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)
	}
}

type throttlerTiming struct {
	t *testing.T

	now           time.Time
	expectedSleep time.Duration
}

func (t *throttlerTiming) getNow() time.Time {
	return t.now
}

func (t *throttlerTiming) doDelay(ctx context.Context, duration time.Duration) {
	t.t.Helper()
	assert.Equal(t.t, t.expectedSleep, duration)
}

func TestClientHelloResumeThrottle(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	throttler, err := async.NewCustomMemoryThrottler(timing.getNow, timing.doDelay)
	require.NoError(err)
	t.Cleanup(func() {
		throttler.Close()
	})
	hub.throttler = throttler

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	timing.expectedSleep = 100 * time.Millisecond
	require.NoError(client.SendHelloResume("this-is-invalid"))

	client.RunUntilError(ctx, "no_such_session") //nolint

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId)
	assert.NotEmpty(hello.Hello.SessionId)
	assert.NotEmpty(hello.Hello.ResumeId)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// Perform housekeeping in the future, this will cause the session to be
	// cleaned up after it is expired.
	hub.performHousekeeping(time.Now().Add(sessionExpireDuration + time.Second))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	// Valid but expired resume ids will not be throttled.
	timing.expectedSleep = 0 * time.Millisecond
	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	client.RunUntilError(ctx, "no_such_session") //nolint

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	timing.expectedSleep = 200 * time.Millisecond
	require.NoError(client.SendHelloResume("this-is-invalid"))

	client.RunUntilError(ctx, "no_such_session") //nolint
}

func TestClientHelloResumeExpired(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// Perform housekeeping in the future, this will cause the session to be
	// cleaned up after it is expired.
	hub.performHousekeeping(time.Now().Add(sessionExpireDuration + time.Second))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if assert.NoError(client.SendHelloResume(hello.Hello.ResumeId)) {
		client.RunUntilError(ctx, "no_such_session") //nolint
	}
}

func TestClientHelloResumeTakeover(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
	assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
	assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

	// The first client got disconnected with a reason in a "Bye" message.
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		assert.Equal("bye", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Bye, "%+v", msg) {
			assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
		}
	}

	client1.RunUntilClosed(ctx)
}

func TestClientHelloResumeOtherHub(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
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
	_, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
	assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2.Hello)
	assert.NotEmpty(hello2.Hello.ResumeId, "%+v", hello2.Hello)

	// The previous session (which had the same internal sid) can't be resumed.
	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	client.RunUntilError(ctx, "no_such_session") //nolint

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloResumePublicId(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	// Test that a client can't resume a "public" session of another user.
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data := "from-1-to-2"
	client1.SendMessage(recipient2, data) // nolint

	var payload string
	var sender *api.MessageServerMessageSender
	if checkReceiveClientMessageWithSender(ctx, t, client2, "session", hello1.Hello, &payload, &sender) {
		assert.Equal(data, payload)
	}

	client1.Close()
	assert.NoError(client1.WaitForClientRemoved(ctx))

	client1 = NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	// Can't resume a session with the id received from messages of a client.
	require.NoError(client1.SendHelloResume(api.PrivateSessionId(sender.SessionId)))
	client1.RunUntilError(ctx, "no_such_session") // nolint

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloByeResume(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	require.NoError(client.SendBye())
	if message, ok := client.RunUntilMessage(ctx); ok {
		checkMessageType(t, message, "bye")
	}

	client.Close()
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	client.RunUntilError(ctx, "no_such_session") //nolint
}

func TestClientHelloResumeAndJoin(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if hello2, ok := client.RunUntilHello(ctx); ok && hello != nil {
		assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)
	}

	// Join room by id.
	roomId := "test-room"
	if roomMsg, ok := client.JoinRoom(ctx, roomId); ok {
		assert.Equal(roomId, roomMsg.Room.RoomId)
	}
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

func TestClientHelloResumeProxy(t *testing.T) { // nolint:paralleltest
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1, hello := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client1.Close()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
			assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			client2.RunUntilJoined(ctx, hello.Hello)

			room := hub1.getRoom(roomId)
			require.NotNil(room, "Could not find room %s", roomId)
			room2 := hub2.getRoom(roomId)
			require.Nil(room2, "Should not have gotten room %s", roomId)

			users := []api.StringMap{
				{
					"sessionId": "the-session-id",
					"inCall":    1,
				},
			}
			room.PublishUsersInCallChanged(users, users)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)
		})
	})
}

func TestClientHelloResumeProxy_Takeover(t *testing.T) { // nolint:paralleltest
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1, hello := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			require.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
			assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
			assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)

			// The first client got disconnected with a reason in a "Bye" message.
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				assert.Equal("bye", msg.Type, "%+v", msg)
				if assert.NotNil(msg.Bye, "%+v", msg) {
					assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
				}
			}

			client1.RunUntilClosed(ctx)

			client3 := NewTestClient(t, server1, hub1)
			defer client3.CloseWithBye()

			require.NoError(client3.SendHelloResume(hello.Hello.ResumeId))
			hello3 := MustSucceed1(t, client3.RunUntilHello, ctx)
			assert.Equal(testDefaultUserId, hello3.Hello.UserId, "%+v", hello3.Hello)
			assert.Equal(hello.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
			assert.Equal(hello.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)

			// The second client got disconnected with a reason in a "Bye" message.
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				assert.Equal("bye", msg.Type, "%+v", msg)
				if assert.NotNil(msg.Bye, "%+v", msg) {
					assert.Equal("session_resumed", msg.Bye.Reason, "%+v", msg)
				}
			}

			client2.RunUntilClosed(ctx)
		})
	})
}

func TestClientHelloResumeProxy_Disconnect(t *testing.T) { // nolint:paralleltest
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		runGrpcProxyTest(t, func(hub1, hub2 *Hub, server1, server2 *httptest.Server) {
			require := require.New(t)
			assert := assert.New(t)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1, hello := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId)
			assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
			assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

			client1.Close()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloResume(hello.Hello.ResumeId))
			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
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
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
}

func TestClientHelloClient_V3Api(t *testing.T) {
	t.Parallel()
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
	require.NoError(client.SendHelloParams(server.URL, api.HelloVersionV1, "client", nil, params))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, ok := client.RunUntilHello(ctx); ok {
		assert.Equal(testDefaultUserId, hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}
}

func TestClientHelloInternal(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHelloInternal())

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, ok := client.RunUntilHello(ctx); ok {
		assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
		assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)
	}
}

func TestClientMessageToSessionId(t *testing.T) {
	t.Parallel()
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

			mcu1 := NewTestMCU(t)
			hub1.SetMcu(mcu1)

			if hub1 != hub2 {
				mcu2 := NewTestMCU(t)
				hub2.SetMcu(mcu2)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

			// Make sure the session subscription events are processed.
			waitForAsyncEventsFlushed(ctx, t, hub1.events)
			waitForAsyncEventsFlushed(ctx, t, hub2.events)

			recipient1 := api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}
			recipient2 := api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}

			data1 := api.StringMap{
				"type":    "test",
				"message": "from-1-to-2",
			}
			client1.SendMessage(recipient2, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient1, data2) // nolint

			var payload1 string
			if checkReceiveClientMessage(ctx, t, client1, "session", hello2.Hello, &payload1) {
				assert.Equal(data2, payload1)
			}
			var payload2 api.StringMap
			if checkReceiveClientMessage(ctx, t, client2, "session", hello1.Hello, &payload2) {
				assert.Equal(data1, payload2)
			}
		})
	}
}

func TestClientControlToSessionId(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

			// Make sure the session subscription events are processed.
			waitForAsyncEventsFlushed(ctx, t, hub1.events)
			waitForAsyncEventsFlushed(ctx, t, hub2.events)

			recipient1 := api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}
			recipient2 := api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient2, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient1, data2) // nolint

			var payload string
			if checkReceiveClientControl(ctx, t, client1, "session", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}
			if checkReceiveClientControl(ctx, t, client2, "session", hello1.Hello, &payload) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientControlMissingPermissions(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 1 may not send control messages (will be ignored).
	session1.SetPermissions([]api.Permission{
		api.PERMISSION_MAY_PUBLISH_AUDIO,
		api.PERMISSION_MAY_PUBLISH_VIDEO,
	})
	// Client 2 may send control messages.
	session2.SetPermissions([]api.Permission{
		api.PERMISSION_MAY_PUBLISH_AUDIO,
		api.PERMISSION_MAY_PUBLISH_VIDEO,
		api.PERMISSION_MAY_CONTROL,
	})

	recipient1 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendControl(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendControl(recipient1, data2) // nolint

	var payload string
	if checkReceiveClientControl(ctx, t, client1, "session", hello2.Hello, &payload) {
		assert.Equal(data2, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client2.RunUntilErrorIs(ctx2, context.DeadlineExceeded)
}

func TestClientMessageToUserId(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	recipient1 := api.MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello1.Hello.UserId,
	}
	recipient2 := api.MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2) // nolint

	var payload string
	if checkReceiveClientMessage(ctx, t, client1, "user", hello2.Hello, &payload) {
		assert.Equal(data2, payload)
	}

	if checkReceiveClientMessage(ctx, t, client2, "user", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestClientControlToUserId(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	recipient1 := api.MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello1.Hello.UserId,
	}
	recipient2 := api.MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendControl(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendControl(recipient1, data2) // nolint

	var payload string
	if checkReceiveClientControl(ctx, t, client1, "user", hello2.Hello, &payload) {
		assert.Equal(data2, payload)
	}

	if checkReceiveClientControl(ctx, t, client2, "user", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestClientMessageToUserIdMultipleSessions(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2a, hello2a := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	client2b, hello2b := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	require.NotEqual(hello1.Hello.SessionId, hello2a.Hello.SessionId)
	require.NotEqual(hello1.Hello.SessionId, hello2b.Hello.SessionId)
	require.NotEqual(hello2a.Hello.SessionId, hello2b.Hello.SessionId)

	require.NotEqual(hello1.Hello.UserId, hello2a.Hello.UserId)
	require.NotEqual(hello1.Hello.UserId, hello2b.Hello.UserId)
	require.Equal(hello2a.Hello.UserId, hello2b.Hello.UserId)

	recipient := api.MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2a.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint

	// Both clients will receive the message as it was sent to the user.
	var payload string
	if checkReceiveClientMessage(ctx, t, client2a, "user", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
	if checkReceiveClientMessage(ctx, t, client2b, "user", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestClientMessageToRoom(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := api.MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendMessage(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient, data2) // nolint

			var payload string
			if checkReceiveClientMessage(ctx, t, client1, "room", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}

			if checkReceiveClientMessage(ctx, t, client2, "room", hello1.Hello, &payload) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientControlToRoom(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := api.MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient, data2) // nolint

			var payload string
			if checkReceiveClientControl(ctx, t, client1, "room", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}

			if checkReceiveClientControl(ctx, t, client2, "room", hello1.Hello, &payload) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientMessageToCall(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			// Simulate request from the backend that somebody joined the call.
			users := []api.StringMap{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
			}
			room1 := hub1.getRoom(roomId)
			require.NotNil(room1, "Could not find room %s", roomId)
			room1.PublishUsersInCallChanged(users, users)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			recipient := api.MessageClientMessageRecipient{
				Type: "call",
			}

			data1 := "from-1-to-2"
			client1.SendMessage(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient, data2) // nolint

			var payload string
			if checkReceiveClientMessage(ctx, t, client1, "call", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}

			// The second client is not in the call yet, so will not receive the message.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

			// Simulate request from the backend that somebody joined the call.
			users = []api.StringMap{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
				{
					"sessionId": hello2.Hello.SessionId,
					"inCall":    1,
				},
			}
			room2 := hub2.getRoom(roomId)
			require.NotNil(room2, "Could not find room %s", roomId)
			room2.PublishUsersInCallChanged(users, users)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			client1.SendMessage(recipient, data1) // nolint
			client2.SendMessage(recipient, data2) // nolint

			if checkReceiveClientMessage(ctx, t, client1, "call", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}
			if checkReceiveClientMessage(ctx, t, client2, "call", hello1.Hello, &payload) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestClientControlToCall(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
			require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			// Simulate request from the backend that somebody joined the call.
			users := []api.StringMap{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
			}
			room1 := hub1.getRoom(roomId)
			require.NotNil(room1, "Could not find room %s", roomId)
			room1.PublishUsersInCallChanged(users, users)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			recipient := api.MessageClientMessageRecipient{
				Type: "call",
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient, data2) // nolint

			var payload string
			if checkReceiveClientControl(ctx, t, client1, "call", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}

			// The second client is not in the call yet, so will not receive the message.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

			// Simulate request from the backend that somebody joined the call.
			users = []api.StringMap{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
				{
					"sessionId": hello2.Hello.SessionId,
					"inCall":    1,
				},
			}
			room2 := hub2.getRoom(roomId)
			require.NotNil(room2, "Could not find room %s", roomId)
			room2.PublishUsersInCallChanged(users, users)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			client1.SendControl(recipient, data1) // nolint
			client2.SendControl(recipient, data2) // nolint

			if checkReceiveClientControl(ctx, t, client1, "call", hello2.Hello, &payload) {
				assert.Equal(data2, payload)
			}
			if checkReceiveClientControl(ctx, t, client2, "call", hello1.Hello, &payload) {
				assert.Equal(data1, payload)
			}
		})
	}
}

func TestJoinRoom(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	assert.Nil(roomMsg.Room.Bandwidth)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Leave room.
	roomMsg = MustSucceed2(t, client.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)
}

func TestJoinRoomBackendBandwidth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.AddOption("backend", "maxstreambitrate", "1000")
		config.AddOption("backend", "maxscreenbitrate", "2000")
		return config, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	if bw := roomMsg.Room.Bandwidth; assert.NotNil(bw) {
		assert.EqualValues(1000, bw.MaxStreamBitrate)
		assert.EqualValues(2000, bw.MaxScreenBitrate)
	}

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)
}

func TestJoinRoomMcuBandwidth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)
	mcu := NewTestMCU(t)
	hub.SetMcu(mcu)

	mcu.SetBandwidthLimits(1000, 2000)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	if bw := roomMsg.Room.Bandwidth; assert.NotNil(bw) {
		assert.EqualValues(1000, bw.MaxStreamBitrate)
		assert.EqualValues(2000, bw.MaxScreenBitrate)
	}

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)
}

func TestJoinRoomPreferMcuBandwidth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.AddOption("backend", "maxstreambitrate", "1000")
		config.AddOption("backend", "maxscreenbitrate", "2000")
		return config, nil
	})

	mcu := NewTestMCU(t)
	hub.SetMcu(mcu)

	// The MCU bandwidth limits overwrite any backend limits.
	mcu.SetBandwidthLimits(100, 200)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	if bw := roomMsg.Room.Bandwidth; assert.NotNil(bw) {
		assert.EqualValues(100, bw.MaxStreamBitrate)
		assert.EqualValues(200, bw.MaxScreenBitrate)
	}

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)
}

func TestJoinInvalidRoom(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-invalid-room"
	msg := &api.ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    roomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId)),
		},
	}
	require.NoError(client.WriteJSON(msg))

	if message, ok := client.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		if checkMessageType(t, message, "error") {
			assert.Equal("no_such_room", message.Error.Code)
		}
	}
}

func TestJoinRoomTwice(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	require.Equal(string(testRoomProperties), string(roomMsg.Room.Properties))

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	msg := &api.ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    roomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s-2", roomId, client.publicId)),
		},
	}
	require.NoError(client.WriteJSON(msg))

	if message, ok := client.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		if checkMessageType(t, message, "error") {
			assert.Equal("already_joined", message.Error.Code)
			if assert.NotEmpty(message.Error.Details) {
				var roomDetails api.RoomErrorDetails
				if err := json.Unmarshal(message.Error.Details, &roomDetails); assert.NoError(err) {
					if assert.NotNil(roomDetails.Room, "%+v", message) {
						assert.Equal(roomId, roomDetails.Room.RoomId)
						assert.Equal(string(testRoomProperties), string(roomDetails.Room.Properties))
					}
				}
			}
		}
	}
}

func TestExpectAnonymousJoinRoom(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, authAnonymousUserId)
	assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	hub.performHousekeeping(time.Now().Add(anonmyousJoinRoomTimeout + time.Second))

	if message, ok := client.RunUntilMessage(ctx); ok {
		if checkMessageType(t, message, "bye") {
			assert.Equal("room_join_timeout", message.Bye.Reason, "%+v", message.Bye)
		}
	}

	// Both the client and the session get removed from the hub.
	assert.NoError(client.WaitForClientRemoved(ctx))
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
}

func TestExpectAnonymousJoinRoomAfterLeave(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, authAnonymousUserId)
	assert.Empty(hello.Hello.UserId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.SessionId, "%+v", hello.Hello)
	assert.NotEmpty(hello.Hello.ResumeId, "%+v", hello.Hello)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Perform housekeeping in the future, this will keep the connection as the
	// session joined a room.
	hub.performHousekeeping(time.Now().Add(anonmyousJoinRoomTimeout + time.Second))

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	client.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

	// Leave room
	roomMsg = MustSucceed2(t, client.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	hub.performHousekeeping(time.Now().Add(anonmyousJoinRoomTimeout + time.Second))

	if message, ok := client.RunUntilMessage(ctx); ok {
		if checkMessageType(t, message, "bye") {
			assert.Equal("room_join_timeout", message.Bye.Reason, "%+v", message.Bye)
		}
	}

	// Both the client and the session get removed from the hub.
	assert.NoError(client.WaitForClientRemoved(ctx))
	assert.NoError(client.WaitForSessionRemoved(ctx, hello.Hello.SessionId))
}

func TestJoinRoomChange(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Change room.
	roomId = "other-test-room"
	roomMsg = MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Leave room.
	roomMsg = MustSucceed2(t, client.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)
}

func TestJoinMultiple(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	// Join room by id (first client).
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client1.RunUntilJoined(ctx, hello1.Hello)

	// Join room by id (second client).
	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event for the first and the second client.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
	// The first client will also receive a "joined" event from the second client.
	client1.RunUntilJoined(ctx, hello2.Hello)

	// Leave room.
	roomMsg = MustSucceed2(t, client1.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)

	// The second client will now receive a "left" event
	client2.RunUntilLeft(ctx, hello1.Hello)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)
}

func TestJoinDisplaynamesPermission(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 2 may not receive display names.
	session2.SetPermissions([]api.Permission{api.PERMISSION_HIDE_DISPLAYNAMES})

	// Join room by id (first client).
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client1.RunUntilJoined(ctx, hello1.Hello)

	// Join room by id (second client).
	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event for the first and the second client.
	if events, unexpected, ok := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello); ok {
		assert.Empty(unexpected)
		if assert.Len(events, 2) {
			assert.Nil(events[0].User)
			assert.Nil(events[1].User)
		}
	}
	// The first client will also receive a "joined" event from the second client.
	if events, unexpected, ok := client1.RunUntilJoinedAndReturn(ctx, hello2.Hello); ok {
		assert.Empty(unexpected)
		if assert.Len(events, 1) {
			assert.NotNil(events[0].User)
		}
	}
}

func TestInitialRoomPermissions(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room-initial-permissions"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	assert.True(session.HasPermission(api.PERMISSION_MAY_PUBLISH_AUDIO), "Session %s should have %s, got %+v", session.PublicId(), api.PERMISSION_MAY_PUBLISH_AUDIO, session.GetPermissions())
	assert.False(session.HasPermission(api.PERMISSION_MAY_PUBLISH_VIDEO), "Session %s should not have %s, got %+v", session.PublicId(), api.PERMISSION_MAY_PUBLISH_VIDEO, session.GetPermissions())
}

func TestJoinRoomSwitchClient(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room-slow"
	msg := &api.ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    roomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId)),
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
	if hello2, ok := client2.RunUntilHello(ctx); ok {
		assert.Equal(testDefaultUserId, hello2.Hello.UserId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId, "%+v", hello2.Hello)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId, "%+v", hello2.Hello)
	}

	roomMsg := MustSucceed1(t, client2.RunUntilMessage, ctx)
	if checkMessageType(t, roomMsg, "room") {
		assert.Equal(roomId, roomMsg.Room.RoomId)
	}

	// We will receive a "joined" event.
	client2.RunUntilJoined(ctx, hello.Hello)

	// Leave room.
	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)
}

func TestGetRealUserIP(t *testing.T) {
	t.Parallel()
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
		trustedProxies, err := container.ParseIPList(tc.trusted)
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
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	client2.Close()
	assert.NoError(client2.WaitForClientRemoved(ctx))

	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	chat_refresh := "{\"type\":\"foo\",\"foo\":{\"testing\":true}}"
	var data1 api.StringMap
	require.NoError(json.Unmarshal([]byte(chat_refresh), &data1))
	client1.SendMessage(recipient2, data1) // nolint

	// Simulate some time until client resumes the session.
	time.Sleep(10 * time.Millisecond)

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloResume(hello2.Hello.ResumeId))
	if hello3, ok := client2.RunUntilHello(ctx); ok {
		assert.Equal(testDefaultUserId+"2", hello3.Hello.UserId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)
	}

	var payload api.StringMap
	if checkReceiveClientMessage(ctx, t, client2, "session", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestCombineChatRefreshWhileDisconnected(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)

	client.Close()
	assert.NoError(client.WaitForClientRemoved(ctx))

	// The two chat messages should get combined into one when receiving pending messages.
	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data api.StringMap
	require.NoError(json.Unmarshal([]byte(chat_refresh), &data))

	// Simulate requests from the backend.
	room.processAsyncMessage(&AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "message",
			Message: &talk.BackendRoomMessageRequest{
				Data: json.RawMessage(chat_refresh),
			},
		},
	})
	room.processAsyncMessage(&AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "message",
			Message: &talk.BackendRoomMessageRequest{
				Data: json.RawMessage(chat_refresh),
			},
		},
	})

	// Simulate some time until client resumes the session.
	time.Sleep(10 * time.Millisecond)

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloResume(hello.Hello.ResumeId))
	if hello2, ok := client.RunUntilHello(ctx); ok {
		assert.Equal(hello.Hello.UserId, hello2.Hello.UserId)
		assert.Equal(hello.Hello.SessionId, hello2.Hello.SessionId)
		assert.Equal(hello.Hello.ResumeId, hello2.Hello.ResumeId)
	}

	if msg, ok := client.RunUntilRoomMessage(ctx); ok {
		assert.Equal(roomId, msg.RoomId)
		var payload api.StringMap
		if err := json.Unmarshal(msg.Data, &payload); assert.NoError(err) {
			assert.Equal(data, payload)
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
}

func TestRoomParticipantsListUpdateWhileDisconnected(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that somebody joined the call.
	users := []api.StringMap{
		{
			"sessionId": "the-session-id",
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users, users)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	client2.Close()
	assert.NoError(client2.WaitForClientRemoved(ctx))

	room.PublishUsersInCallChanged(users, users)

	// Give asynchronous events some time to be processed.
	time.Sleep(100 * time.Millisecond)

	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data1 api.StringMap
	require.NoError(json.Unmarshal([]byte(chat_refresh), &data1))
	client1.SendMessage(recipient2, data1) // nolint

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloResume(hello2.Hello.ResumeId))
	if hello3, ok := client2.RunUntilHello(ctx); ok {
		assert.Equal(testDefaultUserId+"2", hello3.Hello.UserId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
		assert.Equal(hello2.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)
	}

	// The participants list update event is triggered again after the session resume.
	// TODO(jojo): Check contents of message and try with multiple users.
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	var payload api.StringMap
	if checkReceiveClientMessage(ctx, t, client2, "session", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
}

func TestClientTakeoverRoomSession(t *testing.T) {
	t.Parallel()
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

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")

	// Join room by id.
	roomId := "test-room-takeover-room-session"
	roomSessionid := api.RoomSessionId("room-session-id")
	roomMsg := MustSucceed3(t, client1.JoinRoomWithRoomSession, ctx, roomId, roomSessionid)
	require.Equal(roomId, roomMsg.Room.RoomId)

	hubRoom := hub1.getRoom(roomId)
	require.NotNil(hubRoom, "Room %s does not exist", roomId)

	session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
	require.NotNil(session1, "There should be a session %s", hello1.Hello.SessionId)

	client3, hello3 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"3")

	roomMsg = MustSucceed3(t, client3.JoinRoomWithRoomSession, ctx, roomId, roomSessionid+"other")
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Wait until both users have joined.
	WaitForUsersJoined(ctx, t, client1, hello1, client3, hello3)

	client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

	roomMsg = MustSucceed3(t, client2.JoinRoomWithRoomSession, ctx, roomId, roomSessionid)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// The first client got disconnected with a reason in a "Bye" message.
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		assert.Equal("bye", msg.Type, "%+v", msg)
		if assert.NotNil(msg.Bye, "%+v", msg) {
			assert.Equal("room_session_reconnected", msg.Bye.Reason, "%+v", msg)
		}
	}

	client1.RunUntilClosed(ctx)

	// The first session has been closed
	session1 = hub1.GetSessionByPublicId(hello1.Hello.SessionId)
	assert.Nil(session1, "The session %s should have been removed", hello1.Hello.SessionId)

	// The new client will receive "joined" events for the existing client3 and
	// himself.
	client2.RunUntilJoined(ctx, hello3.Hello, hello2.Hello)

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

	// The permanently connected client will receive a "left" event from the
	// overridden session and a "joined" for the new session. In that order as
	// both were on the same server.
	client3.RunUntilLeft(ctx, hello1.Hello)
	client3.RunUntilJoined(ctx, hello2.Hello)
}

func TestClientSendOfferPermissions(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 1 is the moderator
	session1.SetPermissions([]api.Permission{api.PERMISSION_MAY_PUBLISH_MEDIA, api.PERMISSION_MAY_PUBLISH_SCREEN})
	// Client 2 is a guest participant.
	session2.SetPermissions([]api.Permission{})

	// Client 2 may not send an offer (he doesn't have the necessary permissions).
	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "12345",
		RoomType: "screen",
	}))

	msg := MustSucceed1(t, client2.RunUntilMessage, ctx)
	require.True(checkMessageError(t, msg, "not_allowed"))

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "12345",
		RoomType: "screen",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	// Client 1 may send an offer.
	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "54321",
		RoomType: "screen",
	}))

	// The sender won't get a reply...
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	client1.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

	// ...but the other peer will get an offer.
	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)
}

func TestClientSendOfferPermissionsAudioOnly(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	// Client is allowed to send audio only.
	session.SetPermissions([]api.Permission{api.PERMISSION_MAY_PUBLISH_AUDIO})

	// Client may not send an offer with audio and video.
	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	msg := MustSucceed1(t, client.RunUntilMessage, ctx)
	require.True(checkMessageError(t, msg, "not_allowed"))

	// Client may send an offer (audio only).
	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioOnly,
		},
	}))

	client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioOnly)
}

func TestClientSendOfferPermissionsAudioVideo(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	// Client is allowed to send audio and video.
	session.SetPermissions([]api.Permission{api.PERMISSION_MAY_PUBLISH_AUDIO, api.PERMISSION_MAY_PUBLISH_VIDEO})

	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	// Client is no longer allowed to send video, this will stop the publisher.
	msg := &talk.BackendServerRoomRequest{
		Type: "participants",
		Participants: &talk.BackendRoomParticipantsRequest{
			Changed: []api.StringMap{
				{
					"sessionId":   fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId),
					"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_AUDIO},
				},
			},
			Users: []api.StringMap{
				{
					"sessionId":   fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId),
					"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_AUDIO},
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
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	// Client is allowed to send audio and video.
	session.SetPermissions([]api.Permission{api.PERMISSION_MAY_PUBLISH_MEDIA})

	// Client may send an offer (audio and video).
	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	// Client is no longer allowed to send video, this will stop the publisher.
	msg := &talk.BackendServerRoomRequest{
		Type: "participants",
		Participants: &talk.BackendRoomParticipantsRequest{
			Changed: []api.StringMap{
				{
					"sessionId":   fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId),
					"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_MEDIA, api.PERMISSION_MAY_CONTROL},
				},
			},
			Users: []api.StringMap{
				{
					"sessionId":   fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId),
					"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_MEDIA, api.PERMISSION_MAY_CONTROL},
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
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
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

			mcu := NewTestMCU(t)
			require.NoError(mcu.Start(ctx))
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed3(t, client1.JoinRoomWithRoomSession, ctx, roomId, "roomsession1")
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			client1.RunUntilJoined(ctx, hello1.Hello)

			require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: "screen",
				Payload: api.StringMap{
					"sdp": mock.MockSdpOfferAudioAndVideo,
				},
			}))

			require.True(client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

			// Client 2 may not request an offer (he is not in the room yet).
			require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg := MustSucceed1(t, client2.RunUntilMessage, ctx)
			require.True(checkMessageError(t, msg, "not_allowed"))

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			require.True(client1.RunUntilJoined(ctx, hello2.Hello))
			require.True(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

			// Client 2 may not request an offer (he is not in the call yet).
			require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg = MustSucceed1(t, client2.RunUntilMessage, ctx)
			require.True(checkMessageError(t, msg, "not_allowed"))

			// Simulate request from the backend that somebody joined the call.
			users1 := []api.StringMap{
				{
					"sessionId": hello2.Hello.SessionId,
					"inCall":    1,
				},
			}
			room2 := hub2.getRoom(roomId)
			require.NotNil(room2, "Could not find room %s", roomId)
			room2.PublishUsersInCallChanged(users1, users1)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			// Client 2 may not request an offer (recipient is not in the call yet).
			require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			msg = MustSucceed1(t, client2.RunUntilMessage, ctx)
			require.True(checkMessageError(t, msg, "not_allowed"))

			// Simulate request from the backend that somebody joined the call.
			users2 := []api.StringMap{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
			}
			room1 := hub1.getRoom(roomId)
			require.NotNil(room1, "Could not find room %s", roomId)
			room1.PublishUsersInCallChanged(users2, users2)
			checkReceiveClientEvent(ctx, t, client1, "update", nil)
			checkReceiveClientEvent(ctx, t, client2, "update", nil)

			// Client 2 may request an offer now (both are in the same room and call).
			require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}))

			require.True(client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo))

			require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "answer",
				Sid:      "12345",
				RoomType: "screen",
				Payload: api.StringMap{
					"sdp": mock.MockSdpAnswerAudioAndVideo,
				},
			}))

			// The sender won't get a reply...
			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
		})
	}
}

func TestNoSendBetweenSessionsOnDifferentBackends(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	// Clients can't send messages to sessions connected from other backends.
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	require.NoError(client1.SendHelloParams(server.URL+"/one", api.HelloVersionV1, "client", nil, params1))
	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", api.HelloVersionV1, "client", nil, params2))
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	recipient1 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2) // nolint

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client1.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()

	client2.RunUntilErrorIs(ctx3, ErrNoMessageReceived, context.DeadlineExceeded)
}

func TestSendBetweenDifferentUrls(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubWithMultipleUrlsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	require.NoError(client1.SendHelloParams(server.URL+"/one", api.HelloVersionV1, "client", nil, params1))
	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", api.HelloVersionV1, "client", nil, params2))
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	recipient1 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2) // nolint

	var payload string
	if checkReceiveClientMessage(ctx, t, client1, "session", hello2.Hello, &payload) {
		assert.Equal(data2, payload)
	}
	if checkReceiveClientMessage(ctx, t, client2, "session", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestNoSameRoomOnDifferentBackends(t *testing.T) {
	t.Parallel()
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
	require.NoError(client1.SendHelloParams(server.URL+"/one", api.HelloVersionV1, "client", nil, params1))
	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", api.HelloVersionV1, "client", nil, params2))
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	if msg1, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkMessageJoined(msg1, hello1.Hello)
	}

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	if msg2, ok := client2.RunUntilMessage(ctx); ok {
		client2.checkMessageJoined(msg2, hello2.Hello)
	}

	hub.ru.RLock()
	var rooms []*Room
	for _, room := range hub.rooms {
		defer room.Close()
		rooms = append(rooms, room)
	}
	hub.ru.RUnlock()

	if assert.Len(rooms, 2) {
		assert.False(rooms[0].IsEqual(rooms[1]), "Rooms should be different: %+v", rooms)
	}

	recipient := api.MessageClientMessageRecipient{
		Type: "room",
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient, data2) // nolint

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client1.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()

	client2.RunUntilErrorIs(ctx3, ErrNoMessageReceived, context.DeadlineExceeded)
}

func TestSameRoomOnDifferentUrls(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubWithMultipleUrlsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	require.NoError(client1.SendHelloParams(server.URL+"/one", api.HelloVersionV1, "client", nil, params1))
	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	require.NoError(client2.SendHelloParams(server.URL+"/two", api.HelloVersionV1, "client", nil, params2))
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	hub.ru.RLock()
	var rooms []*Room
	for _, room := range hub.rooms {
		defer room.Close()
		rooms = append(rooms, room)
	}
	hub.ru.RUnlock()

	assert.Len(rooms, 1)

	recipient := api.MessageClientMessageRecipient{
		Type: "room",
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint
	data2 := "from-2-to-1"
	client2.SendMessage(recipient, data2) // nolint

	var payload string
	if checkReceiveClientMessage(ctx, t, client1, "room", hello2.Hello, &payload) {
		assert.Equal(data2, payload)
	}
	if checkReceiveClientMessage(ctx, t, client2, "room", hello1.Hello, &payload) {
		assert.Equal(data1, payload)
	}
}

func TestClientSendOffer(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
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

			mcu := NewTestMCU(t)
			require.NoError(mcu.Start(ctx))
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed3(t, client1.JoinRoomWithRoomSession, ctx, roomId, "roomsession1")
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "offer",
				Sid:      "12345",
				RoomType: "video",
				Payload: api.StringMap{
					"sdp": mock.MockSdpOfferAudioAndVideo,
				},
			}))

			require.True(client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

			require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "sendoffer",
				RoomType: "video",
			}))

			// The sender won't get a reply...
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			client1.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

			// ...but the other peer will get an offer.
			client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)
		})
	}
}

func TestClientUnshareScreen(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "screen",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioOnly,
		},
	}))

	client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioOnly)

	publisher := mcu.GetPublisher(hello.Hello.SessionId)
	require.NotNil(publisher, "No publisher for %s found", hello.Hello.SessionId)
	require.False(publisher.isClosed(), "Publisher %s should not be closed", hello.Hello.SessionId)

	old := cleanupScreenPublisherDelay
	cleanupScreenPublisherDelay = time.Millisecond
	defer func() {
		cleanupScreenPublisherDelay = old
	}()

	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "unshareScreen",
		Sid:      "54321",
		RoomType: "screen",
	}))

	time.Sleep(10 * time.Millisecond)

	require.True(publisher.isClosed(), "Publisher %s should be closed", hello.Hello.SessionId)
}

func TestVirtualClientSessions(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId)
			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)

			client1.RunUntilJoined(ctx, hello1.Hello)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloInternal())

			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

			MustSucceed2(t, client2.JoinRoom, ctx, roomId)

			client1.RunUntilJoined(ctx, hello2.Hello)

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 1) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(3, msg.Users[0]["inCall"], "%+v", msg)
					}
				}
			}

			_, unexpected, _ := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello)

			if len(unexpected) == 0 {
				if msg, ok := client2.RunUntilMessage(ctx); ok {
					unexpected = append(unexpected, msg)
				}
			}

			require.Len(unexpected, 1)
			if msg, ok := checkMessageParticipantsInCall(t, unexpected[0]); ok {
				if assert.Len(msg.Users, 1) {
					assert.Equal(true, msg.Users[0]["internal"])
					assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"])
					assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"])
				}
			}

			calledCtx, calledCancel := context.WithTimeout(ctx, time.Second)

			virtualSessionId := api.PublicSessionId("virtual-session-id")
			virtualUserId := "virtual-user-id"
			generatedSessionId := GetVirtualSessionId(session2, virtualSessionId)

			setSessionRequestHandler(t, func(request *talk.BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("add", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})

			require.NoError(client2.SendInternalAddSession(&api.AddSessionInternalClientMessage{
				CommonSessionInternalClientMessage: api.CommonSessionInternalClientMessage{
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

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				client1.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId)
			}

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				client2.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId)
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			updatedFlags := uint32(0)
			require.NoError(client2.SendInternalUpdateSession(&api.UpdateSessionInternalClientMessage{
				CommonSessionInternalClientMessage: api.CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				Flags: &updatedFlags,
			}))

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(0, flags.Flags)
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(0, flags.Flags)
				}
			}

			calledCtx, calledCancel = context.WithTimeout(ctx, time.Second)

			setSessionRequestHandler(t, func(request *talk.BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("remove", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})

			// Messages to virtual sessions are sent to the associated client session.
			virtualRecipient := api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: virtualSession.PublicId(),
			}

			data := "message-to-virtual"
			client1.SendMessage(virtualRecipient, data) // nolint

			var payload string
			var sender *api.MessageServerMessageSender
			var recipient *api.MessageClientMessageRecipient
			if checkReceiveClientMessageWithSenderAndRecipient(ctx, t, client2, "session", hello1.Hello, &payload, &sender, &recipient) {
				assert.Equal(virtualSessionId, recipient.SessionId, "%+v", recipient)
				assert.Equal(data, payload)
			}

			data = "control-to-virtual"
			client1.SendControl(virtualRecipient, data) // nolint

			if checkReceiveClientControlWithSenderAndRecipient(ctx, t, client2, "session", hello1.Hello, &payload, &sender, &recipient) {
				assert.Equal(virtualSessionId, recipient.SessionId, "%+v", recipient)
				assert.Equal(data, payload)
			}

			require.NoError(client2.SendInternalRemoveSession(&api.RemoveSessionInternalClientMessage{
				CommonSessionInternalClientMessage: api.CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				UserId: virtualUserId,
			}))
			<-calledCtx.Done()
			if err := calledCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				require.NoError(err)
			}

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				client1.checkMessageRoomLeaveSession(msg, virtualSession.PublicId())
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				client1.checkMessageRoomLeaveSession(msg, virtualSession.PublicId())
			}
		})
	}
}

func TestDuplicateVirtualSessions(t *testing.T) {
	t.Parallel()
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId)

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)

			client1.RunUntilJoined(ctx, hello1.Hello)

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			require.NoError(client2.SendHelloInternal())

			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

			MustSucceed2(t, client2.JoinRoom, ctx, roomId)

			client1.RunUntilJoined(ctx, hello2.Hello)

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 1) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(3, msg.Users[0]["inCall"], "%+v", msg)
					}
				}
			}

			_, unexpected, _ := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello)

			if len(unexpected) == 0 {
				if msg, ok := client2.RunUntilMessage(ctx); ok {
					unexpected = append(unexpected, msg)
				}
			}

			require.Len(unexpected, 1)
			if msg, ok := checkMessageParticipantsInCall(t, unexpected[0]); ok {
				if assert.Len(msg.Users, 1) {
					assert.Equal(true, msg.Users[0]["internal"])
					assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"])
					assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"])
				}
			}

			calledCtx, calledCancel := context.WithTimeout(ctx, time.Second)

			virtualSessionId := api.PublicSessionId("virtual-session-id")
			virtualUserId := "virtual-user-id"
			generatedSessionId := GetVirtualSessionId(session2, virtualSessionId)

			setSessionRequestHandler(t, func(request *talk.BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("add", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})

			require.NoError(client2.SendInternalAddSession(&api.AddSessionInternalClientMessage{
				CommonSessionInternalClientMessage: api.CommonSessionInternalClientMessage{
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
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				client1.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId)
			}

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				client2.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId)
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 2) {
						assert.Equal(true, msg.Users[0]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[0]["inCall"], "%+v", msg)

						assert.Equal(true, msg.Users[1]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[1]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[1]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if flags, ok := checkMessageParticipantFlags(t, msg); ok {
					assert.Equal(roomId, flags.RoomId)
					assert.Equal(virtualSession.PublicId(), flags.SessionId)
					assert.EqualValues(FLAG_MUTED_SPEAKING, flags.Flags)
				}
			}

			msg := &talk.BackendServerRoomRequest{
				Type: "incall",
				InCall: &talk.BackendRoomInCallRequest{
					InCall: []byte("0"),
					Users: []api.StringMap{
						{
							"sessionId":              virtualSession.PublicId(),
							"participantPermissions": 246,
							"participantType":        4,
							"lastPing":               123456789,
						},
						{
							// Request is coming from Nextcloud, so use its session id (which is our "room session id").
							"sessionId":              fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
							"participantPermissions": 254,
							"participantType":        1,
							"lastPing":               234567890,
						},
					},
				},
			}

			data, err := json.Marshal(msg)
			require.NoError(err)
			res, err := performBackendRequest(server2.URL+"/api/v1/room/"+roomId, data)
			require.NoError(err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			assert.NoError(err)
			assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 3) {
						assert.Equal(true, msg.Users[0]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[0]["inCall"], "%+v", msg)
						assert.EqualValues(246, msg.Users[0]["participantPermissions"], "%+v", msg)
						assert.EqualValues(4, msg.Users[0]["participantType"], "%+v", msg)

						assert.EqualValues(hello1.Hello.SessionId, msg.Users[1]["sessionId"], "%+v", msg)
						assert.Nil(msg.Users[1]["inCall"], "%+v", msg)
						assert.EqualValues(254, msg.Users[1]["participantPermissions"], "%+v", msg)
						assert.EqualValues(1, msg.Users[1]["participantType"], "%+v", msg)

						assert.Equal(true, msg.Users[2]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[2]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[2]["inCall"], "%+v", msg)
					}
				}
			}

			if msg, ok := client2.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 3) {
						assert.Equal(true, msg.Users[0]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[0]["inCall"], "%+v", msg)
						assert.EqualValues(246, msg.Users[0]["participantPermissions"], "%+v", msg)
						assert.EqualValues(4, msg.Users[0]["participantType"], "%+v", msg)

						assert.EqualValues(hello1.Hello.SessionId, msg.Users[1]["sessionId"], "%+v", msg)
						assert.Nil(msg.Users[1]["inCall"], "%+v", msg)
						assert.EqualValues(254, msg.Users[1]["participantPermissions"], "%+v", msg)
						assert.EqualValues(1, msg.Users[1]["participantType"], "%+v", msg)

						assert.Equal(true, msg.Users[2]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[2]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[2]["inCall"], "%+v", msg)
					}
				}
			}

			client1.Close()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			client3 := NewTestClient(t, server1, hub1)
			defer client3.CloseWithBye()

			require.NoError(client3.SendHelloResume(hello1.Hello.ResumeId))
			if hello3, ok := client3.RunUntilHello(ctx); ok {
				assert.Equal(testDefaultUserId, hello3.Hello.UserId, "%+v", hello3.Hello)
				assert.Equal(hello1.Hello.SessionId, hello3.Hello.SessionId, "%+v", hello3.Hello)
				assert.Equal(hello1.Hello.ResumeId, hello3.Hello.ResumeId, "%+v", hello3.Hello)
			}

			if msg, ok := client3.RunUntilMessage(ctx); ok {
				if msg, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msg.Users, 3) {
						assert.Equal(true, msg.Users[0]["virtual"], "%+v", msg)
						assert.EqualValues(virtualSession.PublicId(), msg.Users[0]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithPhone, msg.Users[0]["inCall"], "%+v", msg)
						assert.EqualValues(246, msg.Users[0]["participantPermissions"], "%+v", msg)
						assert.EqualValues(4, msg.Users[0]["participantType"], "%+v", msg)

						assert.EqualValues(hello1.Hello.SessionId, msg.Users[1]["sessionId"], "%+v", msg)
						assert.Nil(msg.Users[1]["inCall"], "%+v", msg)
						assert.EqualValues(254, msg.Users[1]["participantPermissions"], "%+v", msg)
						assert.EqualValues(1, msg.Users[1]["participantType"], "%+v", msg)

						assert.Equal(true, msg.Users[2]["internal"], "%+v", msg)
						assert.EqualValues(hello2.Hello.SessionId, msg.Users[2]["sessionId"], "%+v", msg)
						assert.EqualValues(FlagInCall|FlagWithAudio, msg.Users[2]["inCall"], "%+v", msg)
					}
				}
			}

			setSessionRequestHandler(t, func(request *talk.BackendClientSessionRequest) {
				defer calledCancel()
				assert.Equal("remove", request.Action, "%+v", request)
				assert.Equal(roomId, request.RoomId, "%+v", request)
				assert.NotEqual(generatedSessionId, request.SessionId, "%+v", request)
				assert.Equal(virtualUserId, request.UserId, "%+v", request)
			})
		})
	}
}

func DoTestSwitchToOne(t *testing.T, details api.StringMap) {
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

			roomSessionId1 := api.RoomSessionId("roomsession1")
			roomId1 := "test-room"
			roomMsg := MustSucceed3(t, client1.JoinRoomWithRoomSession, ctx, roomId1, roomSessionId1)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			roomSessionId2 := api.RoomSessionId("roomsession2")
			roomMsg = MustSucceed3(t, client2.JoinRoomWithRoomSession, ctx, roomId1, roomSessionId2)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			var err error
			if details != nil {
				sessions, err = json.Marshal(map[api.RoomSessionId]any{
					roomSessionId1: details,
				})
				require.NoError(err)
			} else {
				sessions, err = json.Marshal([]api.RoomSessionId{
					roomSessionId1,
				})
				require.NoError(err)
			}

			// Notify first client to switch to different room.
			msg := &talk.BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &talk.BackendRoomSwitchToMessageRequest{
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
			client1.RunUntilSwitchTo(ctx, roomId2, detailsData)

			// The other client will not receive a message.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
		})
	}
}

func TestSwitchToOneMap(t *testing.T) {
	t.Parallel()
	DoTestSwitchToOne(t, api.StringMap{
		"foo": "bar",
	})
}

func TestSwitchToOneList(t *testing.T) {
	t.Parallel()
	DoTestSwitchToOne(t, nil)
}

func DoTestSwitchToMultiple(t *testing.T, details1 api.StringMap, details2 api.StringMap) {
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

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomSessionId1 := api.RoomSessionId("roomsession1")
			roomId1 := "test-room"
			roomMsg := MustSucceed3(t, client1.JoinRoomWithRoomSession, ctx, roomId1, roomSessionId1)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			roomSessionId2 := api.RoomSessionId("roomsession2")
			roomMsg = MustSucceed3(t, client2.JoinRoomWithRoomSession, ctx, roomId1, roomSessionId2)
			require.Equal(roomId1, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			var err error
			if details1 != nil || details2 != nil {
				sessions, err = json.Marshal(map[api.RoomSessionId]any{
					roomSessionId1: details1,
					roomSessionId2: details2,
				})
				require.NoError(err)
			} else {
				sessions, err = json.Marshal([]api.RoomSessionId{
					roomSessionId1,
					roomSessionId2,
				})
				require.NoError(err)
			}

			msg := &talk.BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &talk.BackendRoomSwitchToMessageRequest{
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
			client1.RunUntilSwitchTo(ctx, roomId2, detailsData1)

			var detailsData2 json.RawMessage
			if details2 != nil {
				detailsData2, err = json.Marshal(details2)
				require.NoError(err)
			}
			client2.RunUntilSwitchTo(ctx, roomId2, detailsData2)
		})
	}
}

func TestSwitchToMultipleMap(t *testing.T) {
	t.Parallel()
	DoTestSwitchToMultiple(t, api.StringMap{
		"foo": "bar",
	}, api.StringMap{
		"bar": "baz",
	})
}

func TestSwitchToMultipleList(t *testing.T) {
	t.Parallel()
	DoTestSwitchToMultiple(t, nil, nil)
}

func TestSwitchToMultipleMixed(t *testing.T) {
	t.Parallel()
	DoTestSwitchToMultiple(t, api.StringMap{
		"foo": "bar",
	}, nil)
}

func TestGeoipOverrides(t *testing.T) {
	t.Parallel()
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

	assert.Equal(geoip.Loopback, hub.OnLookupCountry(&Client{addr: "127.0.0.1"}))
	assert.Equal(geoip.UnknownCountry, hub.OnLookupCountry(&Client{addr: "8.8.8.8"}))
	assert.EqualValues(country1, hub.OnLookupCountry(&Client{addr: "10.1.1.2"}))
	assert.EqualValues(country2, hub.OnLookupCountry(&Client{addr: "10.2.1.2"}))
	assert.EqualValues(strings.ToUpper(country3), hub.OnLookupCountry(&Client{addr: "192.168.10.20"}))
}

func TestDialoutStatus(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	internalClient := NewTestClient(t, server, hub)
	defer internalClient.CloseWithBye()
	require.NoError(internalClient.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, internalClient.RunUntilHello, ctx)

	roomId := "12345"
	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	MustSucceed2(t, client.JoinRoom, ctx, roomId)
	client.RunUntilJoined(ctx, hello.Hello)

	callId := "call-123"

	stopped := make(chan struct{})
	go func(client *TestClient) {
		defer close(stopped)

		msg, ok := client.RunUntilMessage(ctx)
		if !ok {
			return
		}

		if !assert.Equal("internal", msg.Type, "%+v", msg) ||
			!assert.NotNil(msg.Internal, "%+v", msg) ||
			!assert.Equal("dialout", msg.Internal.Type, "%+v", msg) ||
			!assert.NotNil(msg.Internal.Dialout, "%+v", msg) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)

		response := &api.ClientMessage{
			Id:   msg.Id,
			Type: "internal",
			Internal: &api.InternalClientMessage{
				Type: "dialout",
				Dialout: &api.DialoutInternalClientMessage{
					Type:   "status",
					RoomId: msg.Internal.Dialout.RoomId,
					Status: &api.DialoutStatusInternalClientMessage{
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

	msg := &talk.BackendServerRoomRequest{
		Type: "dialout",
		Dialout: &talk.BackendRoomDialoutRequest{
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

	var response talk.BackendServerRoomResponse
	if assert.NoError(json.Unmarshal(body, &response)) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) {
			assert.Nil(response.Dialout.Error, "expected dialout success, got %s", string(body))
			assert.Equal(callId, response.Dialout.CallId)
		}
	}

	key := "callstatus_" + callId
	if msg, ok := client.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, key, api.StringMap{
			"callid": callId,
			"status": "accepted",
		}, nil)
	}

	require.NoError(internalClient.SendInternalDialout(&api.DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &api.DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "ringing",
		},
	}))

	if msg, ok := client.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, key, api.StringMap{
			"callid": callId,
			"status": "ringing",
		}, api.StringMap{
			"callid": callId,
			"status": "accepted",
		})
	}

	old := removeCallStatusTTL
	defer func() {
		removeCallStatusTTL = old
	}()
	removeCallStatusTTL = 500 * time.Millisecond

	clearedCause := "cleared-call"
	require.NoError(internalClient.SendInternalDialout(&api.DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &api.DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "cleared",
			Cause:  clearedCause,
		},
	}))

	if msg, ok := client.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, key, api.StringMap{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		}, api.StringMap{
			"callid": callId,
			"status": "ringing",
		})
	}

	ctx2, cancel := context.WithTimeout(ctx, removeCallStatusTTL*2)
	defer cancel()

	if msg, ok := client.RunUntilMessage(ctx2); ok {
		checkMessageTransientRemove(t, msg, key, api.StringMap{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		})
	}
}

func TestGracefulShutdownInitial(t *testing.T) {
	t.Parallel()
	hub, _, _, _ := CreateHubForTest(t)

	hub.ScheduleShutdown()
	<-hub.ShutdownChannel()
}

func TestGracefulShutdownOnBye(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, _ := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

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
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, _ := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

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

	hub.performHousekeeping(time.Now().Add(sessionExpireDuration + time.Second))

	select {
	case <-hub.ShutdownChannel():
	case <-time.After(100 * time.Millisecond):
		assert.Fail("should have shutdown")
	}
}
