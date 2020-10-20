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
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

const (
	testDefaultUserId   = "test-userid"
	authAnonymousUserId = "anonymous-userid"

	testTimeout = 10 * time.Second
)

func getTestConfig(server *httptest.Server) (*goconf.ConfigFile, error) {
	config := goconf.NewConfigFile()
	u, err := url.Parse(server.URL)
	if err != nil {
		return nil, err
	}
	config.AddOption("backend", "allowed", u.Host)
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

func CreateHubForTestWithConfig(t *testing.T, getConfigFunc func(*httptest.Server) (*goconf.ConfigFile, error)) (*Hub, NatsClient, *mux.Router, *httptest.Server, func()) {
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	nats, err := NewLoopbackNatsClient()
	if err != nil {
		t.Fatal(err)
	}
	config, err := getConfigFunc(server)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHub(config, nats, r, "no-version")
	if err != nil {
		t.Fatal(err)
	}

	go h.Run()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		WaitForHub(ctx, t, h)
		(nats).(*LoopbackNatsClient).waitForSubscriptionsEmpty(ctx, t)
		server.Close()
	}

	return h, nats, r, server, shutdown
}

func CreateHubForTest(t *testing.T) (*Hub, NatsClient, *mux.Router, *httptest.Server, func()) {
	return CreateHubForTestWithConfig(t, getTestConfig)
}

func CreateHubWithMultipleBackendsForTest(t *testing.T) (*Hub, NatsClient, *mux.Router, *httptest.Server, func()) {
	h, nats, r, server, shutdown := CreateHubForTestWithConfig(t, getTestConfigWithMultipleBackends)
	registerBackendHandlerUrl(t, r, "/one")
	registerBackendHandlerUrl(t, r, "/two")
	return h, nats, r, server, shutdown
}

func WaitForHub(ctx context.Context, t *testing.T, h *Hub) {
	h.Stop()
	for {
		h.mu.Lock()
		clients := len(h.clients)
		sessions := len(h.sessions)
		h.mu.Unlock()
		h.ru.Lock()
		rooms := len(h.rooms)
		h.ru.Unlock()
		if clients == 0 && rooms == 0 && sessions == 0 {
			break
		}

		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.ru.Lock()
			t.Errorf("Error waiting for clients %+v / rooms %+v / sessions %+v to terminate: %s", h.clients, h.rooms, h.sessions, ctx.Err())
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
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal("Error reading body: ", err)
		}

		rnd := r.Header.Get(HeaderBackendSignalingRandom)
		checksum := r.Header.Get(HeaderBackendSignalingChecksum)
		if rnd == "" || checksum == "" {
			t.Fatal("No checksum headers found")
		}

		if verify := CalculateBackendChecksum(rnd, body, testBackendSecret); verify != checksum {
			t.Fatal("Backend checksum verification failed")
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
		w.Write(data)
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
	return response
}

func processRoomRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "room" || request.Room == nil {
		t.Fatalf("Expected an room backend request, got %+v", request)
	}

	if request.Room.RoomId == "test-room-takeover-room-session" {
		// Additional checks for testcase "TestClientTakeoverRoomSession"
		if request.Room.Action == "leave" && request.Room.UserId == "test-userid1" {
			t.Errorf("Should not receive \"leave\" event for first user, received %+v", request.Room)
		}
	}

	// Allow joining any room.
	response := &BackendClientResponse{
		Type: "room",
		Room: &BackendClientRoomResponse{
			Version: BackendVersion,
			RoomId:  request.Room.RoomId,
		},
	}
	return response
}

func processSessionRequest(t *testing.T, w http.ResponseWriter, r *http.Request, request *BackendClientRequest) *BackendClientResponse {
	if request.Type != "session" || request.Session == nil {
		t.Fatalf("Expected an session backend request, got %+v", request)
	}

	// TODO(jojo): Evaluate request.

	response := &BackendClientResponse{
		Type: "session",
		Session: &BackendClientSessionResponse{
			Version: BackendVersion,
			RoomId:  request.Session.RoomId,
		},
	}
	return response
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
		default:
			t.Fatalf("Unsupported request received: %+v", request)
			return nil
		}
	})

	router.HandleFunc(url, handleFunc)
	router.HandleFunc("/ocs/v2.php/apps/spreed/api/v1/signaling/backend", handleFunc)
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

func TestExpectClientHello(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

func TestClientHello(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

func TestClientHelloWithSpaces(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.RemoveOption("backend", "allowed")
		config.AddOption("backend", "allowall", "true")
		return config, nil
	})
	defer shutdown()

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

func TestSessionIdsUnordered(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	atomic.StoreUint64(&hub.sid, 0)
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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	client1.SendMessage(recipient2, data)

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
	}
}

func TestClientHelloClient(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

func TestClientHelloInternal(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	recipient1 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}
	recipient2 := MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient2, data1)
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2)

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
}

func TestClientMessageToUserId(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	client1.SendMessage(recipient2, data1)
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2)

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

func TestClientMessageToUserIdMultipleSessions(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	client1.SendMessage(recipient, data1)

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
	// defined as messages are processed and sent by asynchronous NATS handlers.
	msg1_1, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	msg1_2, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	msg2_1, err := client2.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	msg2_2, err := client2.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := client1.checkMessageJoined(msg1_1, hello1.Hello); err != nil {
		// Ordering is "joined" from client 2, then from client 1
		if err := client1.checkMessageJoined(msg1_1, hello2.Hello); err != nil {
			t.Error(err)
		}
		if err := client1.checkMessageJoined(msg1_2, hello1.Hello); err != nil {
			t.Error(err)
		}
	} else {
		// Ordering is "joined" from client 1, then from client 2
		if err := client1.checkMessageJoined(msg1_2, hello2.Hello); err != nil {
			t.Error(err)
		}
	}
	if err := client2.checkMessageJoined(msg2_1, hello1.Hello); err != nil {
		// Ordering is "joined" from client 2, then from client 1
		if err := client2.checkMessageJoined(msg2_1, hello2.Hello); err != nil {
			t.Error(err)
		}
		if err := client2.checkMessageJoined(msg2_2, hello1.Hello); err != nil {
			t.Error(err)
		}
	} else {
		// Ordering is "joined" from client 1, then from client 2
		if err := client2.checkMessageJoined(msg2_2, hello2.Hello); err != nil {
			t.Error(err)
		}
	}
}

func TestClientMessageToRoom(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
	}

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	recipient := MessageClientMessageRecipient{
		Type: "room",
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1)
	data2 := "from-2-to-1"
	client2.SendMessage(recipient, data2)

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
}

func TestJoinRoom(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
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

func TestExpectAnonymousJoinRoom(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

func TestJoinRoomChange(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
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
	if err := client2.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}
	if err := client2.RunUntilJoined(ctx, hello2.Hello); err != nil {
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

func TestGetRealUserIP(t *testing.T) {
	REMOTE_ATTR := "192.168.1.2"
	var request *http.Request

	request = &http.Request{
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
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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
	client1.SendMessage(recipient2, data1)
	client1.SendMessage(recipient2, data1)

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
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestRoomParticipantsListUpdateWhileDisconnected(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
	}

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that somebody joined the call.
	users := []map[string]interface{}{
		map[string]interface{}{
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

	// Give NATS message some time to be processed.
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
	client1.SendMessage(recipient2, data1)

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
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestClientTakeoverRoomSession(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

	client1 := NewTestClient(t, server, hub)
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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
	} else {
		t.Fatalf("Room %s does not exist", roomId)
	}

	if session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId); session1 == nil {
		t.Fatalf("There should be a session %s", hello1.Hello.SessionId)
	}

	client3 := NewTestClient(t, server, hub)
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

	client2 := NewTestClient(t, server, hub)
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
	if session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId); session1 != nil {
		t.Errorf("The session %s should have been removed", hello1.Hello.SessionId)
	}

	// The new client will receive "joined" events for the existing client3 and
	// himself.
	if err := client2.RunUntilJoined(ctx, hello3.Hello); err != nil {
		t.Error(err)
	}

	if err := client2.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}

	// No message about the closing is sent to the new connection.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	if message, err := client2.RunUntilMessage(ctx2); err != nil && err != NoMessageReceivedError && err != context.DeadlineExceeded {
		t.Error(err)
	} else if message != nil {
		t.Errorf("Expected no message, got %+v", message)
	}

	// The permanently connected client will receive a "left" event from the
	// overridden session and a "joined" for the new session.
	if err := client3.RunUntilLeft(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}
	if err := client3.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}

	time.Sleep(time.Second)
}

func TestClientSendOfferPermissions(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubForTest(t)
	defer shutdown()

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

	if hubRoom := hub.getRoom(roomId); hubRoom != nil {
		defer hubRoom.Close()
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

	// The test MCU doesn't support clients yet, so an error will be returned
	// to the client trying to send the offer.
	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "client_not_found"); err != nil {
			t.Fatal(err)
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if msg, err := client2.RunUntilMessage(ctx2); err != nil {
		if err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", msg)
	}
}

func TestNoSendBetweenSessionsOnDifferentBackends(t *testing.T) {
	// Clients can't send messages to sessions connected from other backends.
	hub, _, _, server, shutdown := CreateHubWithMultipleBackendsForTest(t)
	defer shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	if err := client1.SendHelloParams(server.URL+"/one", "client", params1); err != nil {
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
	if err := client2.SendHelloParams(server.URL+"/two", "client", params2); err != nil {
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
	client1.SendMessage(recipient2, data1)
	data2 := "from-2-to-1"
	client2.SendMessage(recipient1, data2)

	var payload string
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err != nil {
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err != nil {
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}

func TestNoSameRoomOnDifferentBackends(t *testing.T) {
	hub, _, _, server, shutdown := CreateHubWithMultipleBackendsForTest(t)
	defer shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	params1 := TestBackendClientAuthParams{
		UserId: "user1",
	}
	if err := client1.SendHelloParams(server.URL+"/one", "client", params1); err != nil {
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
	if err := client2.SendHelloParams(server.URL+"/two", "client", params2); err != nil {
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
	roomCount := 0
	for _, room := range hub.rooms {
		defer room.Close()
		roomCount++
	}
	hub.ru.RUnlock()

	if roomCount != 2 {
		t.Errorf("Expected 2 rooms, got %d", roomCount)
	}

	recipient := MessageClientMessageRecipient{
		Type: "room",
	}

	data1 := "from-1-to-2"
	client1.SendMessage(recipient, data1)
	data2 := "from-2-to-1"
	client2.SendMessage(recipient, data2)

	var payload string
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := checkReceiveClientMessage(ctx2, client1, "session", hello2.Hello, &payload); err != nil {
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	if err := checkReceiveClientMessage(ctx3, client2, "session", hello1.Hello, &payload); err != nil {
		if err != NoMessageReceivedError {
			t.Error(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", payload)
	}
}
