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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
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
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	events := getAsyncEventsForTest(t)
	config, err := getConfigFunc(server)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHub(config, events, nil, nil, nil, r, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, h, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(r); err != nil {
		t.Fatal(err)
	}

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

	nats := startLocalNatsServer(t)
	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	events1, err := NewAsyncEvents(nats)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		events1.Close()
	})
	config1, err := getConfigFunc(server1)
	if err != nil {
		t.Fatal(err)
	}
	client1, _ := NewGrpcClientsForTest(t, addr2)
	h1, err := NewHub(config1, events1, grpcServer1, client1, nil, r1, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	b1, err := NewBackendServer(config1, h1, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	events2, err := NewAsyncEvents(nats)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		events2.Close()
	})
	config2, err := getConfigFunc(server2)
	if err != nil {
		t.Fatal(err)
	}
	client2, _ := NewGrpcClientsForTest(t, addr1)
	h2, err := NewHub(config2, events2, grpcServer2, client2, nil, r2, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := NewBackendServer(config2, h2, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b1.Start(r1); err != nil {
		t.Fatal(err)
	}
	if err := b2.Start(r2); err != nil {
		t.Fatal(err)
	}

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
		h.mu.Unlock()
		h.ru.Lock()
		rooms := len(h.rooms)
		h.ru.Unlock()
		readActive := h.readPumpActive.Load()
		writeActive := h.writePumpActive.Load()
		if clients == 0 && rooms == 0 && sessions == 0 && readActive == 0 && writeActive == 0 {
			break
		}

		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.ru.Lock()
			dumpGoroutines("", os.Stderr)
			t.Errorf("Error waiting for clients %+v / rooms %+v / sessions %+v / %d read / %d write to terminate: %s", h.clients, h.rooms, h.sessions, readActive, writeActive, ctx.Err())
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal("Error reading body: ", err)
		}

		rnd := r.Header.Get(HeaderBackendSignalingRandom)
		checksum := r.Header.Get(HeaderBackendSignalingChecksum)
		if rnd == "" || checksum == "" {
			t.Fatalf("No checksum headers found in request to %s", r.URL)
		}

		if verify := CalculateBackendChecksum(rnd, body, testBackendSecret); verify != checksum {
			t.Fatalf("Backend checksum verification failed for request to %s", r.URL)
		}

		var request BackendClientRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}

		response := f(w, r, &request)
		if response == nil {
			// Function already returned a response.
			return
		}

		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}

		if r.Header.Get("OCS-APIRequest") != "" {
			var ocs OcsResponse
			ocs.Ocs = &OcsBody{
				Meta: OcsMeta{
					Status:     "ok",
					StatusCode: http.StatusOK,
					Message:    http.StatusText(http.StatusOK),
				},
				Data: (*json.RawMessage)(&data),
			}
			if data, err = json.Marshal(ocs); err != nil {
				t.Fatal(err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data) // nolint
	}
}

func processAuthRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "auth" || request.Auth == nil {
		t.Fatalf("Expected an auth backend request, got %+v", request)
	}

	var params TestBackendClientAuthParams
	if request.Auth.Params != nil && len(*request.Auth.Params) > 0 {
		if err := json.Unmarshal(*request.Auth.Params, &params); err != nil {
			t.Fatal(err)
		}
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
	if data, err := json.Marshal(userdata); err != nil {
		t.Fatal(err)
	} else {
		response.Auth.User = (*json.RawMessage)(&data)
	}
	return response
}

func processRoomRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "room" || request.Room == nil {
		t.Fatalf("Expected an room backend request, got %+v", request)
	}

	switch request.Room.RoomId {
	case "test-room-slow":
		time.Sleep(100 * time.Millisecond)
	case "test-room-takeover-room-session":
		// Additional checks for testcase "TestClientTakeoverRoomSession"
		if request.Room.Action == "leave" && request.Room.UserId == "test-userid1" {
			t.Errorf("Should not receive \"leave\" event for first user, received %+v", request.Room)
		}
	}

	// Allow joining any room.
	response := &BackendClientResponse{
		Type: "room",
		Room: &BackendClientRoomResponse{
			Version:    BackendVersion,
			RoomId:     request.Room.RoomId,
			Properties: (*json.RawMessage)(&testRoomProperties),
		},
	}
	switch request.Room.RoomId {
	case "test-room-with-sessiondata":
		data := map[string]string{
			"userid": "userid-from-sessiondata",
		}
		tmp, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("Could not marshal %+v: %s", data, err)
		}
		response.Room.Session = (*json.RawMessage)(&tmp)
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
		t.Fatalf("Expected an session backend request, got %+v", request)
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
		t.Fatalf("Expected an ping backend request, got %+v", request)
	}

	if request.Ping.RoomId == "test-room-with-sessiondata" {
		if entries := request.Ping.Entries; len(entries) != 1 {
			t.Errorf("Expected one entry, got %+v", entries)
		} else {
			if entries[0].UserId != "" {
				t.Errorf("Expected empty userid, got %+v", entries[0])
			}
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
	if privateKey := os.Getenv("PRIVATE_AUTH_TOKEN_" + t.Name()); privateKey != "" {
		publicKey := os.Getenv("PUBLIC_AUTH_TOKEN_" + t.Name())
		if publicKey == "" {
			// should not happen, always both keys are created
			t.Fatal("public key is empty")
		}
		return privateKey, publicKey
	}

	var private []byte
	var public []byte

	if strings.Contains(t.Name(), "ECDSA") {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}

		private, err = x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		private = pem.EncodeToMemory(&pem.Block{
			Type:  "ECDSA PRIVATE KEY",
			Bytes: private,
		})

		public, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		public = pem.EncodeToMemory(&pem.Block{
			Type:  "ECDSA PUBLIC KEY",
			Bytes: public,
		})
	} else if strings.Contains(t.Name(), "Ed25519") {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}

		private, err = x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		private = pem.EncodeToMemory(&pem.Block{
			Type:  "Ed25519 PRIVATE KEY",
			Bytes: private,
		})

		public, err = x509.MarshalPKIXPublicKey(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		public = pem.EncodeToMemory(&pem.Block{
			Type:  "Ed25519 PUBLIC KEY",
			Bytes: public,
		})
	} else {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}

		private = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})

		public, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(t.Name(), "ECDSA") {
		key, err = jwt.ParseECPrivateKeyFromPEM(data)
	} else if strings.Contains(t.Name(), "Ed25519") {
		key, err = jwt.ParseEdPrivateKeyFromPEM(data)
	} else {
		key, err = jwt.ParseRSAPrivateKeyFromPEM(data)
	}
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func getPublicAuthToken(t *testing.T) (key interface{}) {
	_, public := ensureAuthTokens(t)
	data, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(t.Name(), "ECDSA") {
		key, err = jwt.ParseECPublicKeyFromPEM(data)
	} else if strings.Contains(t.Name(), "Ed25519") {
		key, err = jwt.ParseEdPublicKeyFromPEM(data)
	} else {
		key, err = jwt.ParseRSAPublicKeyFromPEM(data)
	}
	if err != nil {
		t.Fatal(err)
	}
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
			t.Fatalf("Unsupported request received: %+v", request)
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
		if strings.Contains(t.Name(), "V2") && useV2 {
			key := getPublicAuthToken(t)
			public, err := x509.MarshalPKIXPublicKey(key)
			if err != nil {
				t.Fatal(err)
			}
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
			Capabilities: map[string]*json.RawMessage{
				"spreed": (*json.RawMessage)(&spreedCapa),
			},
		}

		data, err := json.Marshal(response)
		if err != nil {
			t.Errorf("Could not marshal %+v: %s", response, err)
		}

		var ocs OcsResponse
		ocs.Ocs = &OcsBody{
			Meta: OcsMeta{
				Status:     "ok",
				StatusCode: http.StatusOK,
				Message:    http.StatusText(http.StatusOK),
			},
			Data: (*json.RawMessage)(&data),
		}
		if data, err = json.Marshal(ocs); err != nil {
			t.Fatal(err)
		}
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

func TestInitialWelcome(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClientContext(ctx, t, server, hub)
	defer client.CloseWithBye()

	msg, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Type != "welcome" {
		t.Errorf("Expected \"welcome\" message, got %+v", msg)
	} else if msg.Welcome.Version == "" {
		t.Errorf("Expected welcome version, got %+v", msg)
	} else if len(msg.Welcome.Features) == 0 {
		t.Errorf("Expected welcome features, got %+v", msg)
	}
}

func TestExpectClientHello(t *testing.T) {
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
	if err := checkUnexpectedClose(err); err != nil {
		t.Fatal(err)
	}

	message2, err := client.RunUntilMessage(ctx)
	if message2 != nil {
		t.Fatalf("Received multiple messages, already have %+v, also got %+v", message, message2)
	}
	if err := checkUnexpectedClose(err); err != nil {
		t.Fatal(err)
	}

	if err := checkMessageType(message, "bye"); err != nil {
		t.Error(err)
	} else if message.Bye.Reason != "hello_timeout" {
		t.Errorf("Expected \"hello_timeout\" reason, got %+v", message.Bye)
	}
}

func TestExpectClientHelloUnsupportedVersion(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	params := TestBackendClientAuthParams{
		UserId: testDefaultUserId,
	}
	if err := client.SendHelloParams(server.URL, "0.0", "", nil, params); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	message, err := client.RunUntilMessage(ctx)
	if err := checkUnexpectedClose(err); err != nil {
		t.Fatal(err)
	}

	if err := checkMessageType(message, "error"); err != nil {
		t.Error(err)
	} else if message.Error.Code != "invalid_hello_version" {
		t.Errorf("Expected \"invalid_hello_version\" reason, got %+v", message.Error)
	}
}

func TestClientHelloV1(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
	}
}

func TestClientHelloV2(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			if err := client.SendHelloV2(testDefaultUserId); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello, err := client.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if hello.Hello.UserId != testDefaultUserId {
				t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
			}
			if hello.Hello.SessionId == "" {
				t.Errorf("Expected session id, got %+v", hello.Hello)
			}

			data := hub.decodeSessionId(hello.Hello.SessionId, publicSessionName)
			if data == nil {
				t.Fatalf("Could not decode session id: %s", hello.Hello.SessionId)
			}

			hub.mu.RLock()
			session := hub.sessions[data.Sid]
			hub.mu.RUnlock()
			if session == nil {
				t.Fatalf("Could not get session for id %+v", data)
			}

			var userdata map[string]string
			if err := json.Unmarshal(*session.UserData(), &userdata); err != nil {
				t.Fatal(err)
			}

			if expected := "Displayname " + testDefaultUserId; userdata["displayname"] != expected {
				t.Errorf("Expected displayname %s, got %s", expected, userdata["displayname"])
			}
		})
	}
}

func TestClientHelloV2_IssuedInFuture(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(time.Minute)
			expiresAt := issuedAt.Add(time.Second)
			if err := client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			if err := checkUnexpectedClose(err); err != nil {
				t.Fatal(err)
			}

			if err := checkMessageType(message, "error"); err != nil {
				t.Error(err)
			} else if message.Error.Code != "token_not_valid_yet" {
				t.Errorf("Expected \"token_not_valid_yet\" reason, got %+v", message.Error)
			}
		})
	}
}

func TestClientHelloV2_Expired(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(-time.Minute)
			if err := client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, issuedAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			if err := checkUnexpectedClose(err); err != nil {
				t.Fatal(err)
			}

			if err := checkMessageType(message, "error"); err != nil {
				t.Error(err)
			} else if message.Error.Code != "token_expired" {
				t.Errorf("Expected \"token_expired\" reason, got %+v", message.Error)
			}
		})
	}
}

func TestClientHelloV2_IssuedAtMissing(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			var issuedAt time.Time
			expiresAt := time.Now().Add(time.Minute)
			if err := client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			if err := checkUnexpectedClose(err); err != nil {
				t.Fatal(err)
			}

			if err := checkMessageType(message, "error"); err != nil {
				t.Error(err)
			} else if message.Error.Code != "token_not_valid_yet" {
				t.Errorf("Expected \"token_not_valid_yet\" reason, got %+v", message.Error)
			}
		})
	}
}

func TestClientHelloV2_ExpiresAtMissing(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			issuedAt := time.Now().Add(-time.Minute)
			var expiresAt time.Time
			if err := client.SendHelloV2WithTimes(testDefaultUserId, issuedAt, expiresAt); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			message, err := client.RunUntilMessage(ctx)
			if err := checkUnexpectedClose(err); err != nil {
				t.Fatal(err)
			}

			if err := checkMessageType(message, "error"); err != nil {
				t.Error(err)
			} else if message.Error.Code != "token_expired" {
				t.Errorf("Expected \"token_expired\" reason, got %+v", message.Error)
			}
		})
	}
}

func TestClientHelloV2_CachedCapabilities(t *testing.T) {
	for _, algo := range testHelloV2Algorithms {
		t.Run(algo, func(t *testing.T) {
			hub, _, _, server := CreateHubForTest(t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			// Simulate old-style Nextcloud without capabilities for Hello V2.
			t.Setenv("SKIP_V2_CAPABILITIES", "1")

			client1 := NewTestClient(t, server, hub)
			defer client1.CloseWithBye()

			if err := client1.SendHelloV1(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if hello1.Hello.UserId != testDefaultUserId+"1" {
				t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"1", hello1.Hello)
			}
			if hello1.Hello.SessionId == "" {
				t.Errorf("Expected session id, got %+v", hello1.Hello)
			}

			// Simulate updated Nextcloud with capabilities for Hello V2.
			t.Setenv("SKIP_V2_CAPABILITIES", "")

			client2 := NewTestClient(t, server, hub)
			defer client2.CloseWithBye()

			if err := client2.SendHelloV2(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if hello2.Hello.UserId != testDefaultUserId+"2" {
				t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"2", hello2.Hello)
			}
			if hello2.Hello.SessionId == "" {
				t.Errorf("Expected session id, got %+v", hello2.Hello)
			}
		})
	}
}

func TestClientHelloWithSpaces(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	userId := "test user with spaces"
	if err := client.SendHello(userId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != userId {
			t.Errorf("Expected \"%s\", got %+v", userId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
	}
}

func TestClientHelloAllowAll(t *testing.T) {
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

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
	}
}

func TestClientHelloSessionLimit(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params1); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if hello, err := client.RunUntilHello(ctx); err != nil {
				t.Error(err)
			} else {
				if hello.Hello.UserId != testDefaultUserId {
					t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
				}
				if hello.Hello.SessionId == "" {
					t.Errorf("Expected session id, got %+v", hello.Hello)
				}
			}

			// The second client can't connect as it would exceed the session limit.
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			params2 := TestBackendClientAuthParams{
				UserId: testDefaultUserId + "2",
			}
			if err := client2.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params2); err != nil {
				t.Fatal(err)
			}

			msg, err := client2.RunUntilMessage(ctx)
			if err != nil {
				t.Error(err)
			} else {
				if msg.Type != "error" || msg.Error == nil {
					t.Errorf("Expected error message, got %+v", msg)
				} else if msg.Error.Code != "session_limit_exceeded" {
					t.Errorf("Expected error \"session_limit_exceeded\", got %+v", msg.Error.Code)
				}
			}

			// The client can connect to a different backend.
			if err := client2.SendHelloParams(server1.URL+"/two", HelloVersionV1, "client", nil, params2); err != nil {
				t.Fatal(err)
			}

			if hello, err := client2.RunUntilHello(ctx); err != nil {
				t.Error(err)
			} else {
				if hello.Hello.UserId != testDefaultUserId+"2" {
					t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"2", hello.Hello)
				}
				if hello.Hello.SessionId == "" {
					t.Errorf("Expected session id, got %+v", hello.Hello)
				}
			}

			// If the first client disconnects (and releases the session), a new one can connect.
			client.CloseWithBye()
			if err := client.WaitForClientRemoved(ctx); err != nil {
				t.Error(err)
			}

			client3 := NewTestClient(t, server2, hub2)
			defer client3.CloseWithBye()

			params3 := TestBackendClientAuthParams{
				UserId: testDefaultUserId + "3",
			}
			if err := client3.SendHelloParams(server1.URL+"/one", HelloVersionV1, "client", nil, params3); err != nil {
				t.Fatal(err)
			}

			if hello, err := client3.RunUntilHello(ctx); err != nil {
				t.Error(err)
			} else {
				if hello.Hello.UserId != testDefaultUserId+"3" {
					t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"3", hello.Hello)
				}
				if hello.Hello.SessionId == "" {
					t.Errorf("Expected session id, got %+v", hello.Hello)
				}
			}
		})
	}
}

func TestSessionIdsUnordered(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	publicSessionIds := make([]string, 0)
	for i := 0; i < 20; i++ {
		client := NewTestClient(t, server, hub)
		defer client.CloseWithBye()

		if err := client.SendHello(testDefaultUserId); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		if hello, err := client.RunUntilHello(ctx); err != nil {
			t.Error(err)
		} else {
			if hello.Hello.UserId != testDefaultUserId {
				t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
				break
			}
			if hello.Hello.SessionId == "" {
				t.Errorf("Expected session id, got %+v", hello.Hello)
				break
			}

			data := hub.decodeSessionId(hello.Hello.SessionId, publicSessionName)
			if data == nil {
				t.Errorf("Could not decode session id: %s", hello.Hello.SessionId)
				break
			}

			hub.mu.RLock()
			session := hub.sessions[data.Sid]
			hub.mu.RUnlock()
			if session == nil {
				t.Errorf("Could not get session for id %+v", data)
				break
			}

			publicSessionIds = append(publicSessionIds, session.PublicId())
		}
	}

	if len(publicSessionIds) == 0 {
		t.Fatal("no session ids decoded")
	}

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
				t.Error("should not have received the same session id twice")
			}
		}
		prevSid = sid
	}

	// Public session ids should not be ordered.
	if len(publicSessionIds) == larger {
		t.Error("the session ids are all larger than the previous ones")
	} else if len(publicSessionIds) == smaller {
		t.Error("the session ids are all smaller than the previous ones")
	}
}

func TestClientHelloResume(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	client.Close()
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello2, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello2.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello2.Hello)
		}
		if hello2.Hello.SessionId != hello.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello.Hello.SessionId, hello2.Hello)
		}
		if hello2.Hello.ResumeId != hello.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello.Hello.ResumeId, hello2.Hello)
		}
	}
}

func TestClientHelloResumeExpired(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	client.Close()
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	// Perform housekeeping in the future, this will cause the session to be
	// cleaned up after it is expired.
	performHousekeeping(hub, time.Now().Add(sessionExpireDuration+time.Second)).Wait()

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	msg, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "error" || msg.Error == nil {
			t.Errorf("Expected error message, got %+v", msg)
		} else if msg.Error.Code != "no_such_session" {
			t.Errorf("Expected error \"no_such_session\", got %+v", msg.Error.Code)
		}
	}
}

func TestClientHelloResumeTakeover(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	if err := client2.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello2.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello2.Hello)
		}
		if hello2.Hello.SessionId != hello.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello.Hello.SessionId, hello2.Hello)
		}
		if hello2.Hello.ResumeId != hello.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello.Hello.ResumeId, hello2.Hello)
		}
	}

	// The first client got disconnected with a reason in a "Bye" message.
	msg, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "bye" || msg.Bye == nil {
			t.Errorf("Expected bye message, got %+v", msg)
		} else if msg.Bye.Reason != "session_resumed" {
			t.Errorf("Expected reason \"session_resumed\", got %+v", msg.Bye.Reason)
		}
	}

	if msg, err := client1.RunUntilMessage(ctx); err == nil {
		t.Errorf("Expected error but received %+v", msg)
	} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		t.Errorf("Expected close error but received %+v", err)
	}
}

func TestClientHelloResumeOtherHub(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	client.Close()
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

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
	if count > 0 {
		t.Errorf("Should have removed all sessions (still has %d)", count)
	}

	// The new client will get the same (internal) sid for his session.
	newClient := NewTestClient(t, server, hub)
	defer newClient.CloseWithBye()

	if err := newClient.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	if hello, err := newClient.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	// The previous session (which had the same internal sid) can't be resumed.
	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	msg, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "error" || msg.Error == nil {
			t.Errorf("Expected error message, got %+v", msg)
		} else if msg.Error.Code != "no_such_session" {
			t.Errorf("Expected error \"no_such_session\", got %+v", msg.Error.Code)
		}
	}

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloResumePublicId(t *testing.T) {
	// Test that a client can't resume a "public" session of another user.
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	}

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data := "from-1-to-2"
	client1.SendMessage(recipient2, data) // nolint

	var payload string
	var sender *MessageServerMessageSender
	if err := checkReceiveClientMessageWithSender(ctx, client2, "session", hello1.Hello, &payload, &sender); err != nil {
		t.Error(err)
	} else if payload != data {
		t.Errorf("Expected payload %s, got %s", data, payload)
	}

	client1.Close()
	if err := client1.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	client1 = NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	// Can't resume a session with the id received from messages of a client.
	if err := client1.SendHelloResume(sender.SessionId); err != nil {
		t.Fatal(err)
	}
	msg, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "error" || msg.Error == nil {
			t.Errorf("Expected error message, got %+v", msg)
		} else if msg.Error.Code != "no_such_session" {
			t.Errorf("Expected error \"no_such_session\", got %+v", msg.Error.Code)
		}
	}

	// Expire old sessions
	hub.performHousekeeping(time.Now().Add(2 * sessionExpireDuration))
}

func TestClientHelloByeResume(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	if err := client.SendBye(); err != nil {
		t.Fatal(err)
	}
	if message, err := client.RunUntilMessage(ctx); err != nil {
		t.Error(err)
	} else {
		if err := checkMessageType(message, "bye"); err != nil {
			t.Error(err)
		}
	}

	client.Close()
	if err := client.WaitForSessionRemoved(ctx, hello.Hello.SessionId); err != nil {
		t.Error(err)
	}
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	msg, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "error" || msg.Error == nil {
			t.Errorf("Expected \"error\", got %+v", *msg)
		} else if msg.Error.Code != "no_such_session" {
			t.Errorf("Expected error \"no_such_session\", got %+v", *msg)
		}
	}
}

func TestClientHelloResumeAndJoin(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	client.Close()
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	client = NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello2, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello2.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello2.Hello)
		}
		if hello2.Hello.SessionId != hello.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello.Hello.SessionId, hello2.Hello)
		}
		if hello2.Hello.ResumeId != hello.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello.Hello.ResumeId, hello2.Hello)
		}
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}
}

func TestClientHelloClient(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloClient(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}
}

func TestClientHelloClient_V3Api(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	params := TestBackendClientAuthParams{
		UserId: testDefaultUserId,
	}
	// The "/api/v1/signaling/" URL will be changed to use "v3" as the "signaling-v3"
	// feature is returned by the capabilities endpoint.
	if err := client.SendHelloParams(server.URL+"/ocs/v2.php/apps/spreed/api/v1/signaling/backend", HelloVersionV1, "client", nil, params); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}
}

func TestClientHelloInternal(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHelloInternal(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if hello, err := client.RunUntilHello(ctx); err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != "" {
			t.Errorf("Expected empty user id, got %+v", hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}
}

func TestClientMessageToSessionId(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if hello1.Hello.SessionId == hello2.Hello.SessionId {
				t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
			}

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
			if err := checkReceiveClientMessage(ctx, client1, "session", hello2.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data2 {
				t.Errorf("Expected payload %s, got %s", data2, payload)
			}
			if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data1 {
				t.Errorf("Expected payload %s, got %s", data1, payload)
			}
		})
	}
}

func TestClientControlToSessionId(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if hello1.Hello.SessionId == hello2.Hello.SessionId {
				t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
			}

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
			if err := checkReceiveClientControl(ctx, client1, "session", hello2.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data2 {
				t.Errorf("Expected payload %s, got %s", data2, payload)
			}
			if err := checkReceiveClientControl(ctx, client2, "session", hello1.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data1 {
				t.Errorf("Expected payload %s, got %s", data1, payload)
			}
		})
	}
}

func TestClientControlMissingPermissions(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	}

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	if session2 == nil {
		t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
	}

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
	if err := checkReceiveClientControl(ctx, client1, "session", hello2.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data2 {
		t.Errorf("Expected payload %s, got %s", data2, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestClientMessageToUserId(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	} else if hello1.Hello.UserId == hello2.Hello.UserId {
		t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
	}

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
	if err := checkReceiveClientMessage(ctx, client1, "user", hello2.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data2 {
		t.Errorf("Expected payload %s, got %s", data2, payload)
	}

	if err := checkReceiveClientMessage(ctx, client2, "user", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data1 {
		t.Errorf("Expected payload %s, got %s", data1, payload)
	}
}

func TestClientControlToUserId(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	} else if hello1.Hello.UserId == hello2.Hello.UserId {
		t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
	}

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
	if err := checkReceiveClientControl(ctx, client1, "user", hello2.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data2 {
		t.Errorf("Expected payload %s, got %s", data2, payload)
	}

	if err := checkReceiveClientControl(ctx, client2, "user", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data1 {
		t.Errorf("Expected payload %s, got %s", data1, payload)
	}
}

func TestClientMessageToUserIdMultipleSessions(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2a := NewTestClient(t, server, hub)
	defer client2a.CloseWithBye()
	if err := client2a.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}
	client2b := NewTestClient(t, server, hub)
	defer client2b.CloseWithBye()
	if err := client2b.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2a, err := client2a.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2b, err := client2b.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2a.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	} else if hello1.Hello.SessionId == hello2b.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	} else if hello2a.Hello.SessionId == hello2b.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello2a.Hello.SessionId)
	}
	if hello1.Hello.UserId == hello2a.Hello.UserId {
		t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
	} else if hello1.Hello.UserId == hello2b.Hello.UserId {
		t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
	} else if hello2a.Hello.UserId != hello2b.Hello.UserId {
		t.Fatalf("Expected the same user ids, got %s and %s", hello2a.Hello.UserId, hello2b.Hello.UserId)
	}

	recipient := MessageClientMessageRecipient{
		Type:   "user",
		UserId: hello2a.Hello.UserId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1) // nolint

	// Both clients will receive the message as it was sent to the user.
	var payload string
	if err := checkReceiveClientMessage(ctx, client2a, "user", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data1 {
		t.Errorf("Expected payload %s, got %s", data1, payload)
	}
	if err := checkReceiveClientMessage(ctx, client2b, "user", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if payload != data1 {
		t.Errorf("Expected payload %s, got %s", data1, payload)
	}
}

func WaitForUsersJoined(ctx context.Context, t *testing.T, client1 *TestClient, hello1 *ServerMessage, client2 *TestClient, hello2 *ServerMessage) {
	// We will receive "joined" events for all clients. The ordering is not
	// defined as messages are processed and sent by asynchronous event handlers.
	if err := client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
		t.Error(err)
	}
	if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
		t.Error(err)
	}
}

func TestClientMessageToRoom(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if hello1.Hello.SessionId == hello2.Hello.SessionId {
				t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
			} else if hello1.Hello.UserId == hello2.Hello.UserId {
				t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
			}

			// Join room by id.
			roomId := "test-room"
			if room, err := client1.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			if room, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendMessage(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendMessage(recipient, data2) // nolint

			var payload string
			if err := checkReceiveClientMessage(ctx, client1, "room", hello2.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data2 {
				t.Errorf("Expected payload %s, got %s", data2, payload)
			}

			if err := checkReceiveClientMessage(ctx, client2, "room", hello1.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data1 {
				t.Errorf("Expected payload %s, got %s", data1, payload)
			}
		})
	}
}

func TestClientControlToRoom(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if hello1.Hello.SessionId == hello2.Hello.SessionId {
				t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
			} else if hello1.Hello.UserId == hello2.Hello.UserId {
				t.Fatalf("Expected different user ids, got %s twice", hello1.Hello.UserId)
			}

			// Join room by id.
			roomId := "test-room"
			if room, err := client1.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			if room, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			recipient := MessageClientMessageRecipient{
				Type: "room",
			}

			data1 := "from-1-to-2"
			client1.SendControl(recipient, data1) // nolint
			data2 := "from-2-to-1"
			client2.SendControl(recipient, data2) // nolint

			var payload string
			if err := checkReceiveClientControl(ctx, client1, "room", hello2.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data2 {
				t.Errorf("Expected payload %s, got %s", data2, payload)
			}

			if err := checkReceiveClientControl(ctx, client2, "room", hello1.Hello, &payload); err != nil {
				t.Error(err)
			} else if payload != data1 {
				t.Errorf("Expected payload %s, got %s", data1, payload)
			}
		})
	}
}

func TestJoinRoom(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Leave room.
	if room, err := client.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected empty room, got %s", room.Room.RoomId)
	}
}

func TestJoinRoomTwice(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	} else if !bytes.Equal(testRoomProperties, *room.Room.Properties) {
		t.Fatalf("Expected room properties %s, got %s", string(testRoomProperties), string(*room.Room.Properties))
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	msg := &ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomId + "-" + client.publicId + "-2",
		},
	}
	if err := client.WriteJSON(msg); err != nil {
		t.Fatal(err)
	}

	message, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkUnexpectedClose(err); err != nil {
		t.Fatal(err)
	}

	if msg.Id != message.Id {
		t.Errorf("expected message id %s, got %s", msg.Id, message.Id)
	} else if err := checkMessageType(message, "error"); err != nil {
		t.Fatal(err)
	} else if expected := "already_joined"; message.Error.Code != expected {
		t.Errorf("expected error %s, got %s", expected, message.Error.Code)
	} else if message.Error.Details == nil {
		t.Fatal("expected error details")
	}

	var roomMsg RoomErrorDetails
	if err := json.Unmarshal(message.Error.Details, &roomMsg); err != nil {
		t.Fatal(err)
	} else if roomMsg.Room == nil {
		t.Fatalf("expected room details, got %+v", message)
	}

	if roomMsg.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %+v", roomId, roomMsg.Room)
	} else if !bytes.Equal(testRoomProperties, *roomMsg.Room.Properties) {
		t.Fatalf("Expected room properties %s, got %s", string(testRoomProperties), string(*roomMsg.Room.Properties))
	}
}

func TestExpectAnonymousJoinRoom(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(authAnonymousUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != "" {
			t.Errorf("Expected an anonymous user, got %+v", hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	message, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := checkMessageType(message, "bye"); err != nil {
		t.Error(err)
	} else if message.Bye.Reason != "room_join_timeout" {
		t.Errorf("Expected \"room_join_timeout\" reason, got %+v", message.Bye)
	}

	// Both the client and the session get removed from the hub.
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}
	if err := client.WaitForSessionRemoved(ctx, hello.Hello.SessionId); err != nil {
		t.Error(err)
	}
}

func TestExpectAnonymousJoinRoomAfterLeave(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(authAnonymousUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello.Hello.UserId != "" {
			t.Errorf("Expected an anonymous user, got %+v", hello.Hello)
		}
		if hello.Hello.SessionId == "" {
			t.Errorf("Expected session id, got %+v", hello.Hello)
		}
		if hello.Hello.ResumeId == "" {
			t.Errorf("Expected resume id, got %+v", hello.Hello)
		}
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Perform housekeeping in the future, this will keep the connection as the
	// session joined a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		t.Error(err)
	} else if message != nil {
		t.Errorf("Expected no message, got %+v", message)
	}

	// Leave room
	if room, err := client.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected room %s, got %s", "", room.Room.RoomId)
	}

	// Perform housekeeping in the future, this will cause the connection to
	// be terminated because the anonymous client didn't join a room.
	performHousekeeping(hub, time.Now().Add(anonmyousJoinRoomTimeout+time.Second))

	message, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := checkMessageType(message, "bye"); err != nil {
		t.Error(err)
	} else if message.Bye.Reason != "room_join_timeout" {
		t.Errorf("Expected \"room_join_timeout\" reason, got %+v", message.Bye)
	}

	// Both the client and the session get removed from the hub.
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}
	if err := client.WaitForSessionRemoved(ctx, hello.Hello.SessionId); err != nil {
		t.Error(err)
	}
}

func TestJoinRoomChange(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Change room.
	roomId = "other-test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Leave room.
	if room, err := client.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected empty room, got %s", room.Room.RoomId)
	}
}

func TestJoinMultiple(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	}

	// Join room by id (first client).
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	// Join room by id (second client).
	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event for the first and the second client.
	if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
		t.Error(err)
	}
	// The first client will also receive a "joined" event from the second client.
	if err := client1.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}

	// Leave room.
	if room, err := client1.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected empty room, got %s", room.Room.RoomId)
	}

	// The second client will now receive a "left" event
	if err := client2.RunUntilLeft(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	if room, err := client2.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected empty room, got %s", room.Room.RoomId)
	}
}

func TestJoinDisplaynamesPermission(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	if session2 == nil {
		t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
	}

	// Client 2 may not receive display names.
	session2.SetPermissions([]Permission{PERMISSION_HIDE_DISPLAYNAMES})

	// Join room by id (first client).
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	// Join room by id (second client).
	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event for the first and the second client.
	if events, unexpected, err := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello); err != nil {
		t.Error(err)
	} else {
		if len(unexpected) > 0 {
			t.Errorf("Received unexpected messages: %+v", unexpected)
		} else if len(events) != 2 {
			t.Errorf("Expected two event, got %+v", events)
		} else if events[0].User != nil {
			t.Errorf("Expected empty userdata for first event, got %+v", events[0].User)
		} else if events[1].User != nil {
			t.Errorf("Expected empty userdata for second event, got %+v", events[1].User)
		}
	}
	// The first client will also receive a "joined" event from the second client.
	if events, unexpected, err := client1.RunUntilJoinedAndReturn(ctx, hello2.Hello); err != nil {
		t.Error(err)
	} else {
		if len(unexpected) > 0 {
			t.Errorf("Received unexpected messages: %+v", unexpected)
		} else if len(events) != 1 {
			t.Errorf("Expected one event, got %+v", events)
		} else if events[0].User == nil {
			t.Errorf("Expected userdata for first event, got nothing")
		}
	}
}

func TestInitialRoomPermissions(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room-initial-permissions"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	if session == nil {
		t.Fatalf("Session %s does not exist", hello.Hello.SessionId)
	}

	if !session.HasPermission(PERMISSION_MAY_PUBLISH_AUDIO) {
		t.Errorf("Session %s should have %s, got %+v", session.PublicId(), PERMISSION_MAY_PUBLISH_AUDIO, session.permissions)
	}
	if session.HasPermission(PERMISSION_MAY_PUBLISH_VIDEO) {
		t.Errorf("Session %s should not have %s, got %+v", session.PublicId(), PERMISSION_MAY_PUBLISH_VIDEO, session.permissions)
	}
}

func TestJoinRoomSwitchClient(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

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
	if err := client.WriteJSON(msg); err != nil {
		t.Fatal(err)
	}
	// Wait a bit to make sure request is sent before closing client.
	time.Sleep(1 * time.Millisecond)
	client.Close()
	if err := client.WaitForClientRemoved(ctx); err != nil {
		t.Fatal(err)
	}

	// The client needs some time to reconnect.
	time.Sleep(200 * time.Millisecond)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHelloResume(hello.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello2.Hello.UserId != testDefaultUserId {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId, hello2.Hello)
		}
		if hello2.Hello.SessionId != hello.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello.Hello.SessionId, hello2.Hello)
		}
		if hello2.Hello.ResumeId != hello.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello.Hello.ResumeId, hello2.Hello)
		}
	}

	room, err := client2.RunUntilMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkUnexpectedClose(err); err != nil {
		t.Fatal(err)
	}
	if err := checkMessageType(room, "room"); err != nil {
		t.Fatal(err)
	}
	if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client2.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Leave room.
	if room, err := client2.JoinRoom(ctx, ""); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != "" {
		t.Fatalf("Expected empty room, got %s", room.Room.RoomId)
	}
}

func TestGetRealUserIP(t *testing.T) {
	REMOTE_ATTR := "192.168.1.2"

	request := &http.Request{
		RemoteAddr: REMOTE_ATTR,
	}
	if ip := getRealUserIP(request); ip != REMOTE_ATTR {
		t.Errorf("Expected %s but got %s", REMOTE_ATTR, ip)
	}

	X_REAL_IP := "192.168.10.11"
	request.Header = http.Header{
		http.CanonicalHeaderKey("x-real-ip"): []string{X_REAL_IP},
	}
	if ip := getRealUserIP(request); ip != X_REAL_IP {
		t.Errorf("Expected %s but got %s", X_REAL_IP, ip)
	}

	// "X-Real-IP" has preference before "X-Forwarded-For"
	X_FORWARDED_FOR_IP := "192.168.20.21"
	X_FORWARDED_FOR := X_FORWARDED_FOR_IP + ", 192.168.30.32"
	request.Header = http.Header{
		http.CanonicalHeaderKey("x-real-ip"):       []string{X_REAL_IP},
		http.CanonicalHeaderKey("x-forwarded-for"): []string{X_FORWARDED_FOR},
	}
	if ip := getRealUserIP(request); ip != X_REAL_IP {
		t.Errorf("Expected %s but got %s", X_REAL_IP, ip)
	}

	request.Header = http.Header{
		http.CanonicalHeaderKey("x-forwarded-for"): []string{X_FORWARDED_FOR},
	}
	if ip := getRealUserIP(request); ip != X_FORWARDED_FOR_IP {
		t.Errorf("Expected %s but got %s", X_FORWARDED_FOR_IP, ip)
	}
}

func TestClientMessageToSessionIdWhileDisconnected(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	}

	client2.Close()
	if err := client2.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	// The two chat messages should get combined into one when receiving pending messages.
	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data1 map[string]interface{}
	if err := json.Unmarshal([]byte(chat_refresh), &data1); err != nil {
		t.Fatal(err)
	}
	client1.SendMessage(recipient2, data1) // nolint
	client1.SendMessage(recipient2, data1) // nolint

	// Simulate some time until client resumes the session.
	time.Sleep(10 * time.Millisecond)

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHelloResume(hello2.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello3, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello3.Hello.UserId != testDefaultUserId+"2" {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"2", hello3.Hello)
		}
		if hello3.Hello.SessionId != hello2.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, hello3.Hello)
		}
		if hello3.Hello.ResumeId != hello2.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello2.Hello.ResumeId, hello3.Hello)
		}
	}

	var payload map[string]interface{}
	if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if !reflect.DeepEqual(payload, data1) {
		t.Errorf("Expected payload %+v, got %+v", data1, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestRoomParticipantsListUpdateWhileDisconnected(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}
	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if hello1.Hello.SessionId == hello2.Hello.SessionId {
		t.Fatalf("Expected different session ids, got %s twice", hello1.Hello.SessionId)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that somebody joined the call.
	users := []map[string]interface{}{
		{
			"sessionId": "the-session-id",
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	if room == nil {
		t.Fatalf("Could not find room %s", roomId)
	}
	room.PublishUsersInCallChanged(users, users)
	if err := checkReceiveClientEvent(ctx, client2, "update", nil); err != nil {
		t.Error(err)
	}

	client2.Close()
	if err := client2.WaitForClientRemoved(ctx); err != nil {
		t.Error(err)
	}

	room.PublishUsersInCallChanged(users, users)

	// Give asynchronous events some time to be processed.
	time.Sleep(100 * time.Millisecond)

	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	chat_refresh := "{\"type\":\"chat\",\"chat\":{\"refresh\":true}}"
	var data1 map[string]interface{}
	if err := json.Unmarshal([]byte(chat_refresh), &data1); err != nil {
		t.Fatal(err)
	}
	client1.SendMessage(recipient2, data1) // nolint

	client2 = NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHelloResume(hello2.Hello.ResumeId); err != nil {
		t.Fatal(err)
	}
	hello3, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if hello3.Hello.UserId != testDefaultUserId+"2" {
			t.Errorf("Expected \"%s\", got %+v", testDefaultUserId+"2", hello3.Hello)
		}
		if hello3.Hello.SessionId != hello2.Hello.SessionId {
			t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, hello3.Hello)
		}
		if hello3.Hello.ResumeId != hello2.Hello.ResumeId {
			t.Errorf("Expected resume id %s, got %+v", hello2.Hello.ResumeId, hello3.Hello)
		}
	}

	// The participants list update event is triggered again after the session resume.
	// TODO(jojo): Check contents of message and try with multiple users.
	if err := checkReceiveClientEvent(ctx, client2, "update", nil); err != nil {
		t.Error(err)
	}

	var payload map[string]interface{}
	if err := checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload); err != nil {
		t.Error(err)
	} else if !reflect.DeepEqual(payload, data1) {
		t.Errorf("Expected payload %+v, got %+v", data1, payload)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if err := checkReceiveClientMessage(ctx2, client2, "session", hello1.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestClientTakeoverRoomSession(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			RunTestClientTakeoverRoomSession(t)
		})
	}
}

func RunTestClientTakeoverRoomSession(t *testing.T) {
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

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room-takeover-room-session"
	roomSessionid := "room-session-id"
	if room, err := client1.JoinRoomWithRoomSession(ctx, roomId, roomSessionid); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if hubRoom := hub1.getRoom(roomId); hubRoom == nil {
		t.Fatalf("Room %s does not exist", roomId)
	}

	if session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId); session1 == nil {
		t.Fatalf("There should be a session %s", hello1.Hello.SessionId)
	}

	client3 := NewTestClient(t, server2, hub2)
	defer client3.CloseWithBye()

	if err := client3.SendHello(testDefaultUserId + "3"); err != nil {
		t.Fatal(err)
	}

	hello3, err := client3.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if room, err := client3.JoinRoomWithRoomSession(ctx, roomId, roomSessionid+"other"); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// Wait until both users have joined.
	WaitForUsersJoined(ctx, t, client1, hello1, client3, hello3)

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()

	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if room, err := client2.JoinRoomWithRoomSession(ctx, roomId, roomSessionid); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// The first client got disconnected with a reason in a "Bye" message.
	msg, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	} else {
		if msg.Type != "bye" || msg.Bye == nil {
			t.Errorf("Expected bye message, got %+v", msg)
		} else if msg.Bye.Reason != "room_session_reconnected" {
			t.Errorf("Expected reason \"room_session_reconnected\", got %+v", msg.Bye.Reason)
		}
	}

	if msg, err := client1.RunUntilMessage(ctx); err == nil {
		t.Errorf("Expected error but received %+v", msg)
	} else if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		t.Errorf("Expected close error but received %+v", err)
	}

	// The first session has been closed
	if session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId); session1 != nil {
		t.Errorf("The session %s should have been removed", hello1.Hello.SessionId)
	}

	// The new client will receive "joined" events for the existing client3 and
	// himself.
	if err := client2.RunUntilJoined(ctx, hello3.Hello, hello2.Hello); err != nil {
		t.Error(err)
	}

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client2.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		t.Error(err)
	} else if message != nil {
		t.Errorf("Expected no message, got %+v", message)
	}

	// The permanently connected client will receive a "left" event from the
	// overridden session and a "joined" for the new session. In that order as
	// both were on the same server.
	if err := client3.RunUntilLeft(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}
	if err := client3.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}
}

func TestClientSendOfferPermissions(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
		t.Fatal(err)
	}

	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	if session2 == nil {
		t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
	}

	// Client 1 is the moderator
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_MEDIA, PERMISSION_MAY_PUBLISH_SCREEN})
	// Client 2 is a guest participant.
	session2.SetPermissions([]Permission{})

	// Client 2 may not send an offer (he doesn't have the necessary permissions).
	if err := client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "12345",
		RoomType: "screen",
	}); err != nil {
		t.Fatal(err)
	}

	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "not_allowed"); err != nil {
			t.Fatal(err)
		}
	}

	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "12345",
		RoomType: "screen",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
		t.Fatal(err)
	}

	// Client 1 may send an offer.
	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "sendoffer",
		Sid:      "54321",
		RoomType: "screen",
	}); err != nil {
		t.Fatal(err)
	}

	// The sender won't get a reply...
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client1.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
		t.Error(err)
	} else if message != nil {
		t.Errorf("Expected no message, got %+v", message)
	}

	// ...but the other peer will get an offer.
	if err := client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo); err != nil {
		t.Fatal(err)
	}
}

func TestClientSendOfferPermissionsAudioOnly(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}

	// Client is allowed to send audio only.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_AUDIO})

	// Client may not send an offer with audio and video.
	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "not_allowed"); err != nil {
			t.Fatal(err)
		}
	}

	// Client may send an offer (audio only).
	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioOnly,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioOnly); err != nil {
		t.Fatal(err)
	}
}

func TestClientSendOfferPermissionsAudioVideo(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}

	// Client is allowed to send audio and video.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_AUDIO, PERMISSION_MAY_PUBLISH_VIDEO})

	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatal(err)
	}
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
	}

	ctx2, cancel2 := context.WithTimeout(ctx, time.Second)
	defer cancel2()

	pubs := mcu.GetPublishers()
	if len(pubs) != 1 {
		t.Fatalf("expected one publisher, got %+v", pubs)
	}

loop:
	for {
		if err := ctx2.Err(); err != nil {
			t.Errorf("publisher was not closed: %s", err)
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
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}

	// Client is allowed to send audio and video.
	session1.SetPermissions([]Permission{PERMISSION_MAY_PUBLISH_MEDIA})

	// Client may send an offer (audio and video).
	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatal(err)
	}
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

	pubs := mcu.GetPublishers()
	if len(pubs) != 1 {
		t.Fatalf("expected one publisher, got %+v", pubs)
	}

loop:
	for {
		if err := ctx2.Err(); err != nil {
			if err != context.DeadlineExceeded {
				t.Errorf("error while waiting for publisher: %s", err)
			}
			break
		}

		for _, pub := range pubs {
			if pub.isClosed() {
				t.Errorf("publisher was closed")
				break loop
			}
		}

		// Give some time to async processing.
		time.Sleep(time.Millisecond)
	}
}

func TestClientRequestOfferNotInRoom(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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

			mcu, err := NewTestMCU()
			if err != nil {
				t.Fatal(err)
			} else if err := mcu.Start(); err != nil {
				t.Fatal(err)
			}
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Join room by id.
			roomId := "test-room"
			if room, err := client1.JoinRoomWithRoomSession(ctx, roomId, "roomsession1"); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// We will receive a "joined" event.
			if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
				t.Error(err)
			}

			if err := client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: "screen",
				Payload: map[string]interface{}{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}); err != nil {
				t.Fatal(err)
			}

			if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
				t.Fatal(err)
			}

			// Client 2 may not request an offer (he is not in the room yet).
			if err := client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}); err != nil {
				t.Fatal(err)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Fatal(err)
			} else {
				if err := checkMessageError(msg, "not_allowed"); err != nil {
					t.Fatal(err)
				}
			}

			if room, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// We will receive a "joined" event.
			if err := client1.RunUntilJoined(ctx, hello2.Hello); err != nil {
				t.Error(err)
			}
			if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
				t.Error(err)
			}

			// Client 2 may not request an offer (he is not in the call yet).
			if err := client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}); err != nil {
				t.Fatal(err)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Fatal(err)
			} else {
				if err := checkMessageError(msg, "not_allowed"); err != nil {
					t.Fatal(err)
				}
			}

			// Simulate request from the backend that somebody joined the call.
			users1 := []map[string]interface{}{
				{
					"sessionId": hello2.Hello.SessionId,
					"inCall":    1,
				},
			}
			room2 := hub2.getRoom(roomId)
			if room2 == nil {
				t.Fatalf("Could not find room %s", roomId)
			}
			room2.PublishUsersInCallChanged(users1, users1)
			if err := checkReceiveClientEvent(ctx, client1, "update", nil); err != nil {
				t.Error(err)
			}
			if err := checkReceiveClientEvent(ctx, client2, "update", nil); err != nil {
				t.Error(err)
			}

			// Client 2 may not request an offer (recipient is not in the call yet).
			if err := client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}); err != nil {
				t.Fatal(err)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Fatal(err)
			} else {
				if err := checkMessageError(msg, "not_allowed"); err != nil {
					t.Fatal(err)
				}
			}

			// Simulate request from the backend that somebody joined the call.
			users2 := []map[string]interface{}{
				{
					"sessionId": hello1.Hello.SessionId,
					"inCall":    1,
				},
			}
			room1 := hub1.getRoom(roomId)
			if room1 == nil {
				t.Fatalf("Could not find room %s", roomId)
			}
			room1.PublishUsersInCallChanged(users2, users2)
			if err := checkReceiveClientEvent(ctx, client1, "update", nil); err != nil {
				t.Error(err)
			}
			if err := checkReceiveClientEvent(ctx, client2, "update", nil); err != nil {
				t.Error(err)
			}

			// Client 2 may request an offer now (both are in the same room and call).
			if err := client2.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "requestoffer",
				Sid:      "12345",
				RoomType: "screen",
			}); err != nil {
				t.Fatal(err)
			}

			if err := client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoSendBetweenSessionsOnDifferentBackends(t *testing.T) {
	// Clients can't send messages to sessions connected from other backends.
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	if err := client1.SendHelloParams(server.URL+"/one", HelloVersionV1, "client", nil, params1); err != nil {
		t.Fatal(err)
	}
	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	if err := client2.SendHelloParams(server.URL+"/two", HelloVersionV1, "client", nil, params2); err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

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
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestNoSameRoomOnDifferentBackends(t *testing.T) {
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	if err := client1.SendHelloParams(server.URL+"/one", HelloVersionV1, "client", nil, params1); err != nil {
		t.Fatal(err)
	}
	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	params2 := TestBackendClientAuthParams{
		UserId: "user2",
	}
	if err := client2.SendHelloParams(server.URL+"/two", HelloVersionV1, "client", nil, params2); err != nil {
		t.Fatal(err)
	}
	hello2, err := client2.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}
	msg1, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	if err := client1.checkMessageJoined(msg1, hello1.Hello); err != nil {
		t.Error(err)
	}

	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}
	msg2, err := client2.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	if err := client2.checkMessageJoined(msg2, hello2.Hello); err != nil {
		t.Error(err)
	}

	hub.ru.RLock()
	var rooms []*Room
	for _, room := range hub.rooms {
		defer room.Close()
		rooms = append(rooms, room)
	}
	hub.ru.RUnlock()

	if len(rooms) != 2 {
		t.Errorf("Expected 2 rooms, got %+v", rooms)
	}

	if rooms[0].IsEqual(rooms[1]) {
		t.Errorf("Rooms should be different: %+v", rooms)
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
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err != nil {
		if err != ErrNoMessageReceived {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestClientSendOffer(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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

			mcu, err := NewTestMCU()
			if err != nil {
				t.Fatal(err)
			} else if err := mcu.Start(); err != nil {
				t.Fatal(err)
			}
			defer mcu.Stop()

			hub1.SetMcu(mcu)
			hub2.SetMcu(mcu)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()

			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Join room by id.
			roomId := "test-room"
			if room, err := client1.JoinRoomWithRoomSession(ctx, roomId, "roomsession1"); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			if room, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			if err := client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello1.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "12345",
				RoomType: "video",
				Payload: map[string]interface{}{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}); err != nil {
				t.Fatal(err)
			}

			if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
				t.Fatal(err)
			}

			if err := client1.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello2.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "sendoffer",
				RoomType: "video",
			}); err != nil {
				t.Fatal(err)
			}

			// The sender won't get a reply...
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			if message, err := client1.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
			}

			// ...but the other peer will get an offer.
			if err := client2.RunUntilOffer(ctx, MockSdpOfferAudioAndVideo); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientUnshareScreen(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	hello1, err := client1.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client1.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}

	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "screen",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioOnly,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client1.RunUntilAnswer(ctx, MockSdpAnswerAudioOnly); err != nil {
		t.Fatal(err)
	}

	publisher := mcu.GetPublisher(hello1.Hello.SessionId)
	if publisher == nil {
		t.Fatalf("No publisher for %s found", hello1.Hello.SessionId)
	} else if publisher.isClosed() {
		t.Fatalf("Publisher %s should not be closed", hello1.Hello.SessionId)
	}

	old := cleanupScreenPublisherDelay
	cleanupScreenPublisherDelay = time.Millisecond
	defer func() {
		cleanupScreenPublisherDelay = old
	}()

	if err := client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "unshareScreen",
		Sid:      "54321",
		RoomType: "screen",
	}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)

	if !publisher.isClosed() {
		t.Fatalf("Publisher %s should be closed", hello1.Hello.SessionId)
	}
}

func TestVirtualClientSessions(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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

			if err := client1.SendHello(testDefaultUserId); err != nil {
				t.Fatal(err)
			}

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			roomId := "test-room"
			if _, err := client1.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			}

			if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
				t.Error(err)
			}

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()

			if err := client2.SendHelloInternal(); err != nil {
				t.Fatal(err)
			}

			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			if session2 == nil {
				t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
			}

			if _, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			}

			if err := client1.RunUntilJoined(ctx, hello2.Hello); err != nil {
				t.Error(err)
			}

			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if msg, err := checkMessageParticipantsInCall(msg); err != nil {
				t.Error(err)
			} else if len(msg.Users) != 1 {
				t.Errorf("Expected one user, got %+v", msg)
			} else if v, ok := msg.Users[0]["internal"].(bool); !ok || !v {
				t.Errorf("Expected internal flag, got %+v", msg)
			} else if v, ok := msg.Users[0]["sessionId"].(string); !ok || v != hello2.Hello.SessionId {
				t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, msg)
			} else if v, ok := msg.Users[0]["inCall"].(float64); !ok || v != 3 {
				t.Errorf("Expected inCall flag 3, got %+v", msg)
			}

			_, unexpected, err := client2.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello)
			if err != nil {
				t.Error(err)
			}

			if len(unexpected) == 0 {
				if msg, err := client2.RunUntilMessage(ctx); err != nil {
					t.Error(err)
				} else {
					unexpected = append(unexpected, msg)
				}
			}

			if len(unexpected) != 1 {
				t.Fatalf("expected one message, got %+v", unexpected)
			}

			if msg, err := checkMessageParticipantsInCall(unexpected[0]); err != nil {
				t.Error(err)
			} else if len(msg.Users) != 1 {
				t.Errorf("Expected one user, got %+v", msg)
			} else if v, ok := msg.Users[0]["internal"].(bool); !ok || !v {
				t.Errorf("Expected internal flag, got %+v", msg)
			} else if v, ok := msg.Users[0]["sessionId"].(string); !ok || v != hello2.Hello.SessionId {
				t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, msg)
			} else if v, ok := msg.Users[0]["inCall"].(float64); !ok || v != FlagInCall|FlagWithAudio {
				t.Errorf("Expected inCall flag %d, got %+v", FlagInCall|FlagWithAudio, msg)
			}

			calledCtx, calledCancel := context.WithTimeout(ctx, time.Second)

			virtualSessionId := "virtual-session-id"
			virtualUserId := "virtual-user-id"
			generatedSessionId := GetVirtualSessionId(session2, virtualSessionId)

			setSessionRequestHandler(t, func(request *BackendClientSessionRequest) {
				defer calledCancel()
				if request.Action != "add" {
					t.Errorf("Expected action add, got %+v", request)
				} else if request.RoomId != roomId {
					t.Errorf("Expected room id %s, got %+v", roomId, request)
				} else if request.SessionId == generatedSessionId {
					t.Errorf("Expected generated session id %s, got %+v", generatedSessionId, request)
				} else if request.UserId != virtualUserId {
					t.Errorf("Expected session id %s, got %+v", virtualUserId, request)
				}
			})

			if err := client2.SendInternalAddSession(&AddSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},
				UserId: virtualUserId,
				Flags:  FLAG_MUTED_SPEAKING,
			}); err != nil {
				t.Fatal(err)
			}
			<-calledCtx.Done()
			if err := calledCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}

			virtualSessions := session2.GetVirtualSessions()
			for len(virtualSessions) == 0 {
				time.Sleep(time.Millisecond)
				virtualSessions = session2.GetVirtualSessions()
			}

			virtualSession := virtualSessions[0]
			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if err := client1.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId); err != nil {
				t.Error(err)
			}

			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if msg, err := checkMessageParticipantsInCall(msg); err != nil {
				t.Error(err)
			} else if len(msg.Users) != 2 {
				t.Errorf("Expected two users, got %+v", msg)
			} else if v, ok := msg.Users[0]["internal"].(bool); !ok || !v {
				t.Errorf("Expected internal flag, got %+v", msg)
			} else if v, ok := msg.Users[0]["sessionId"].(string); !ok || v != hello2.Hello.SessionId {
				t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, msg)
			} else if v, ok := msg.Users[0]["inCall"].(float64); !ok || v != FlagInCall|FlagWithAudio {
				t.Errorf("Expected inCall flag %d, got %+v", FlagInCall|FlagWithAudio, msg)
			} else if v, ok := msg.Users[1]["virtual"].(bool); !ok || !v {
				t.Errorf("Expected virtual flag, got %+v", msg)
			} else if v, ok := msg.Users[1]["sessionId"].(string); !ok || v != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if v, ok := msg.Users[1]["inCall"].(float64); !ok || v != FlagInCall|FlagWithPhone {
				t.Errorf("Expected inCall flag %d, got %+v", FlagInCall|FlagWithPhone, msg)
			}

			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if flags, err := checkMessageParticipantFlags(msg); err != nil {
				t.Error(err)
			} else if flags.RoomId != roomId {
				t.Errorf("Expected room id %s, got %+v", roomId, msg)
			} else if flags.SessionId != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if flags.Flags != FLAG_MUTED_SPEAKING {
				t.Errorf("Expected flags %d, got %+v", FLAG_MUTED_SPEAKING, msg)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if err := client2.checkMessageJoinedSession(msg, virtualSession.PublicId(), virtualUserId); err != nil {
				t.Error(err)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if msg, err := checkMessageParticipantsInCall(msg); err != nil {
				t.Error(err)
			} else if len(msg.Users) != 2 {
				t.Errorf("Expected two users, got %+v", msg)
			} else if v, ok := msg.Users[0]["internal"].(bool); !ok || !v {
				t.Errorf("Expected internal flag, got %+v", msg)
			} else if v, ok := msg.Users[0]["sessionId"].(string); !ok || v != hello2.Hello.SessionId {
				t.Errorf("Expected session id %s, got %+v", hello2.Hello.SessionId, msg)
			} else if v, ok := msg.Users[0]["inCall"].(float64); !ok || v != FlagInCall|FlagWithAudio {
				t.Errorf("Expected inCall flag %d, got %+v", FlagInCall|FlagWithAudio, msg)
			} else if v, ok := msg.Users[1]["virtual"].(bool); !ok || !v {
				t.Errorf("Expected virtual flag, got %+v", msg)
			} else if v, ok := msg.Users[1]["sessionId"].(string); !ok || v != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if v, ok := msg.Users[1]["inCall"].(float64); !ok || v != FlagInCall|FlagWithPhone {
				t.Errorf("Expected inCall flag %d, got %+v", FlagInCall|FlagWithPhone, msg)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if flags, err := checkMessageParticipantFlags(msg); err != nil {
				t.Error(err)
			} else if flags.RoomId != roomId {
				t.Errorf("Expected room id %s, got %+v", roomId, msg)
			} else if flags.SessionId != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if flags.Flags != FLAG_MUTED_SPEAKING {
				t.Errorf("Expected flags %d, got %+v", FLAG_MUTED_SPEAKING, msg)
			}

			updatedFlags := uint32(0)
			if err := client2.SendInternalUpdateSession(&UpdateSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				Flags: &updatedFlags,
			}); err != nil {
				t.Fatal(err)
			}

			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if flags, err := checkMessageParticipantFlags(msg); err != nil {
				t.Error(err)
			} else if flags.RoomId != roomId {
				t.Errorf("Expected room id %s, got %+v", roomId, msg)
			} else if flags.SessionId != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if flags.Flags != 0 {
				t.Errorf("Expected flags %d, got %+v", 0, msg)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if flags, err := checkMessageParticipantFlags(msg); err != nil {
				t.Error(err)
			} else if flags.RoomId != roomId {
				t.Errorf("Expected room id %s, got %+v", roomId, msg)
			} else if flags.SessionId != virtualSession.PublicId() {
				t.Errorf("Expected session id %s, got %+v", virtualSession.PublicId(), msg)
			} else if flags.Flags != 0 {
				t.Errorf("Expected flags %d, got %+v", 0, msg)
			}

			calledCtx, calledCancel = context.WithTimeout(ctx, time.Second)

			setSessionRequestHandler(t, func(request *BackendClientSessionRequest) {
				defer calledCancel()
				if request.Action != "remove" {
					t.Errorf("Expected action remove, got %+v", request)
				} else if request.RoomId != roomId {
					t.Errorf("Expected room id %s, got %+v", roomId, request)
				} else if request.SessionId == generatedSessionId {
					t.Errorf("Expected generated session id %s, got %+v", generatedSessionId, request)
				} else if request.UserId != virtualUserId {
					t.Errorf("Expected user id %s, got %+v", virtualUserId, request)
				}
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
			if err := checkReceiveClientMessageWithSenderAndRecipient(ctx, client2, "session", hello1.Hello, &payload, &sender, &recipient); err != nil {
				t.Error(err)
			} else if recipient.SessionId != virtualSessionId {
				t.Errorf("Expected session id %s, got %+v", virtualSessionId, recipient)
			} else if payload != data {
				t.Errorf("Expected payload %s, got %s", data, payload)
			}

			data = "control-to-virtual"
			client1.SendControl(virtualRecipient, data) // nolint

			if err := checkReceiveClientControlWithSenderAndRecipient(ctx, client2, "session", hello1.Hello, &payload, &sender, &recipient); err != nil {
				t.Error(err)
			} else if recipient.SessionId != virtualSessionId {
				t.Errorf("Expected session id %s, got %+v", virtualSessionId, recipient)
			} else if payload != data {
				t.Errorf("Expected payload %s, got %s", data, payload)
			}

			if err := client2.SendInternalRemoveSession(&RemoveSessionInternalClientMessage{
				CommonSessionInternalClientMessage: CommonSessionInternalClientMessage{
					SessionId: virtualSessionId,
					RoomId:    roomId,
				},

				UserId: virtualUserId,
			}); err != nil {
				t.Fatal(err)
			}
			<-calledCtx.Done()
			if err := calledCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}

			if msg, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if err := client1.checkMessageRoomLeaveSession(msg, virtualSession.PublicId()); err != nil {
				t.Error(err)
			}

			if msg, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if err := client2.checkMessageRoomLeaveSession(msg, virtualSession.PublicId()); err != nil {
				t.Error(err)
			}

		})
	}
}

func DoTestSwitchToOne(t *testing.T, details map[string]interface{}) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			roomSessionId1 := "roomsession1"
			roomId1 := "test-room"
			if room, err := client1.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId1); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId1 {
				t.Fatalf("Expected room %s, got %s", roomId1, room.Room.RoomId)
			}

			roomSessionId2 := "roomsession2"
			if room, err := client2.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId2); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId1 {
				t.Fatalf("Expected room %s, got %s", roomId1, room.Room.RoomId)
			}

			if err := client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
				t.Error(err)
			}
			if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
				t.Error(err)
			}

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			if details != nil {
				if sessions, err = json.Marshal(map[string]interface{}{
					roomSessionId1: details,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if sessions, err = json.Marshal([]string{
					roomSessionId1,
				}); err != nil {
					t.Fatal(err)
				}
			}

			// Notify first client to switch to different room.
			msg := &BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &BackendRoomSwitchToMessageRequest{
					RoomId:   roomId2,
					Sessions: &sessions,
				},
			}

			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			res, err := performBackendRequest(server2.URL+"/api/v1/room/"+roomId1, data)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != 200 {
				t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
			}

			var detailsData json.RawMessage
			if details != nil {
				if detailsData, err = json.Marshal(details); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client1.RunUntilSwitchTo(ctx, roomId2, detailsData); err != nil {
				t.Error(err)
			}

			// The other client will not receive a message.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel2()

			if message, err := client2.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
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
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
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
			if err := client1.SendHello(testDefaultUserId + "1"); err != nil {
				t.Fatal(err)
			}
			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			if err := client2.SendHello(testDefaultUserId + "2"); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1, err := client1.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}
			hello2, err := client2.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			roomSessionId1 := "roomsession1"
			roomId1 := "test-room"
			if room, err := client1.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId1); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId1 {
				t.Fatalf("Expected room %s, got %s", roomId1, room.Room.RoomId)
			}

			roomSessionId2 := "roomsession2"
			if room, err := client2.JoinRoomWithRoomSession(ctx, roomId1, roomSessionId2); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId1 {
				t.Fatalf("Expected room %s, got %s", roomId1, room.Room.RoomId)
			}

			if err := client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
				t.Error(err)
			}
			if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
				t.Error(err)
			}

			roomId2 := "test-room-2"
			var sessions json.RawMessage
			if details1 != nil || details2 != nil {
				if sessions, err = json.Marshal(map[string]interface{}{
					roomSessionId1: details1,
					roomSessionId2: details2,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if sessions, err = json.Marshal([]string{
					roomSessionId1,
					roomSessionId2,
				}); err != nil {
					t.Fatal(err)
				}
			}

			msg := &BackendServerRoomRequest{
				Type: "switchto",
				SwitchTo: &BackendRoomSwitchToMessageRequest{
					RoomId:   roomId2,
					Sessions: &sessions,
				},
			}

			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			res, err := performBackendRequest(server2.URL+"/api/v1/room/"+roomId1, data)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != 200 {
				t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
			}

			var detailsData1 json.RawMessage
			if details1 != nil {
				if detailsData1, err = json.Marshal(details1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client1.RunUntilSwitchTo(ctx, roomId2, detailsData1); err != nil {
				t.Error(err)
			}

			var detailsData2 json.RawMessage
			if details2 != nil {
				if detailsData2, err = json.Marshal(details2); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client2.RunUntilSwitchTo(ctx, roomId2, detailsData2); err != nil {
				t.Error(err)
			}
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

	if country := hub.OnLookupCountry(&Client{addr: "127.0.0.1"}); country != loopback {
		t.Errorf("expected country %s, got %s", loopback, country)
	}

	if country := hub.OnLookupCountry(&Client{addr: "8.8.8.8"}); country != unknownCountry {
		t.Errorf("expected country %s, got %s", unknownCountry, country)
	}

	if country := hub.OnLookupCountry(&Client{addr: "10.1.1.2"}); country != country1 {
		t.Errorf("expected country %s, got %s", country1, country)
	}

	if country := hub.OnLookupCountry(&Client{addr: "10.2.1.2"}); country != country2 {
		t.Errorf("expected country %s, got %s", country2, country)
	}

	if country := hub.OnLookupCountry(&Client{addr: "192.168.10.20"}); country != strings.ToUpper(country3) {
		t.Errorf("expected country %s, got %s", strings.ToUpper(country3), country)
	}
}

func TestDialoutStatus(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	internalClient := NewTestClient(t, server, hub)
	defer internalClient.CloseWithBye()
	if err := internalClient.SendHelloInternalWithFeatures([]string{"start-dialout"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := internalClient.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "12345"
	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	}

	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	callId := "call-123"

	stopped := make(chan struct{})
	go func(client *TestClient) {
		defer close(stopped)

		msg, err := client.RunUntilMessage(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		if msg.Type != "internal" || msg.Internal.Type != "dialout" {
			t.Errorf("expected internal dialout message, got %+v", msg)
			return
		}

		if msg.Internal.Dialout.RoomId != roomId {
			t.Errorf("expected room id %s, got %+v", roomId, msg)
		}

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
		if err := client.WriteJSON(response); err != nil {
			t.Error(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected error %d, got %s: %s", http.StatusOK, res.Status, string(body))
	}

	var response BackendServerRoomResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	if response.Type != "dialout" || response.Dialout == nil {
		t.Fatalf("expected type dialout, got %s", string(body))
	}
	if response.Dialout.Error != nil {
		t.Fatalf("expected dialout success, got %s", string(body))
	}
	if response.Dialout.CallId != callId {
		t.Errorf("expected call id %s, got %s", callId, string(body))
	}

	key := "callstatus_" + callId
	if msg, err := client.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "accepted",
		}, nil); err != nil {
			t.Error(err)
		}
	}

	if err := internalClient.SendInternalDialout(&DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "ringing",
		},
	}); err != nil {
		t.Fatal(err)
	}

	if msg, err := client.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "ringing",
		},
			map[string]interface{}{
				"callid": callId,
				"status": "accepted",
			}); err != nil {
			t.Error(err)
		}
	}

	old := removeCallStatusTTL
	defer func() {
		removeCallStatusTTL = old
	}()
	removeCallStatusTTL = 500 * time.Millisecond

	clearedCause := "cleared-call"
	if err := internalClient.SendInternalDialout(&DialoutInternalClientMessage{
		RoomId: roomId,
		Type:   "status",
		Status: &DialoutStatusInternalClientMessage{
			CallId: callId,
			Status: "cleared",
			Cause:  clearedCause,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if msg, err := client.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		},
			map[string]interface{}{
				"callid": callId,
				"status": "ringing",
			}); err != nil {
			t.Error(err)
		}
	}

	ctx2, cancel := context.WithTimeout(ctx, removeCallStatusTTL*2)
	defer cancel()

	if msg, err := client.RunUntilMessage(ctx2); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientRemove(msg, key, map[string]interface{}{
			"callid": callId,
			"status": "cleared",
			"cause":  clearedCause,
		}); err != nil {
			t.Error(err)
		}
	}
}
