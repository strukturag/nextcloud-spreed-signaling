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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	turnApiKey        = "TheApiKey"
	turnSecret        = "TheTurnSecret"
	turnServersString = "turn:1.2.3.4:9991?transport=udp,turn:1.2.3.4:9991?transport=tcp"
	turnServers       = strings.Split(turnServersString, ",")
)

func CreateBackendServerForTest(t *testing.T) (*goconf.ConfigFile, *BackendServer, AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	return CreateBackendServerForTestFromConfig(t, nil)
}

func CreateBackendServerForTestWithTurn(t *testing.T) (*goconf.ConfigFile, *BackendServer, AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	config := goconf.NewConfigFile()
	config.AddOption("turn", "apikey", turnApiKey)
	config.AddOption("turn", "secret", turnSecret)
	config.AddOption("turn", "servers", turnServersString)
	return CreateBackendServerForTestFromConfig(t, config)
}

func CreateBackendServerForTestFromConfig(t *testing.T, config *goconf.ConfigFile) (*goconf.ConfigFile, *BackendServer, AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})
	if config == nil {
		config = goconf.NewConfigFile()
	}
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(t.Name(), "Compat") {
		config.AddOption("backend", "allowed", u.Host)
		config.AddOption("backend", "secret", string(testBackendSecret))
	} else {
		backendId := "backend1"
		config.AddOption("backend", "backends", backendId)
		config.AddOption(backendId, "url", server.URL)
		config.AddOption(backendId, "secret", string(testBackendSecret))
	}
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	config.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config.AddOption("clients", "internalsecret", string(testInternalSecret))
	config.AddOption("geoip", "url", "none")
	events := getAsyncEventsForTest(t)
	hub, err := NewHub(config, events, nil, nil, nil, r, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, hub, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(r); err != nil {
		t.Fatal(err)
	}

	go hub.Run()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		WaitForHub(ctx, t, hub)
	})

	return config, b, events, hub, r, server
}

func CreateBackendServerWithClusteringForTest(t *testing.T) (*BackendServer, *BackendServer, *Hub, *Hub, *httptest.Server, *httptest.Server) {
	return CreateBackendServerWithClusteringForTestFromConfig(t, nil, nil)
}

func CreateBackendServerWithClusteringForTestFromConfig(t *testing.T, config1 *goconf.ConfigFile, config2 *goconf.ConfigFile) (*BackendServer, *BackendServer, *Hub, *Hub, *httptest.Server, *httptest.Server) {
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

	if config1 == nil {
		config1 = goconf.NewConfigFile()
	}
	u1, err := url.Parse(server1.URL)
	if err != nil {
		t.Fatal(err)
	}
	config1.AddOption("backend", "allowed", u1.Host)
	if u1.Scheme == "http" {
		config1.AddOption("backend", "allowhttp", "true")
	}
	config1.AddOption("backend", "secret", string(testBackendSecret))
	config1.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config1.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config1.AddOption("clients", "internalsecret", string(testInternalSecret))
	config1.AddOption("geoip", "url", "none")

	events1, err := NewAsyncEvents(nats)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		events1.Close()
	})
	client1, _ := NewGrpcClientsForTest(t, addr2)
	hub1, err := NewHub(config1, events1, grpcServer1, client1, nil, r1, "no-version")
	if err != nil {
		t.Fatal(err)
	}

	if config2 == nil {
		config2 = goconf.NewConfigFile()
	}
	u2, err := url.Parse(server2.URL)
	if err != nil {
		t.Fatal(err)
	}
	config2.AddOption("backend", "allowed", u2.Host)
	if u2.Scheme == "http" {
		config2.AddOption("backend", "allowhttp", "true")
	}
	config2.AddOption("backend", "secret", string(testBackendSecret))
	config2.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config2.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config2.AddOption("clients", "internalsecret", string(testInternalSecret))
	config2.AddOption("geoip", "url", "none")
	events2, err := NewAsyncEvents(nats)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		events2.Close()
	})
	client2, _ := NewGrpcClientsForTest(t, addr1)
	hub2, err := NewHub(config2, events2, grpcServer2, client2, nil, r2, "no-version")
	if err != nil {
		t.Fatal(err)
	}

	b1, err := NewBackendServer(config1, hub1, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b1.Start(r1); err != nil {
		t.Fatal(err)
	}
	b2, err := NewBackendServer(config2, hub2, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.Start(r2); err != nil {
		t.Fatal(err)
	}

	go hub1.Run()
	go hub2.Run()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		WaitForHub(ctx, t, hub1)
		WaitForHub(ctx, t, hub2)
	})

	return b1, b2, hub1, hub2, server1, server2
}

func performBackendRequest(requestUrl string, body []byte) (*http.Response, error) {
	request, err := http.NewRequest("POST", requestUrl, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	rnd := newRandomString(32)
	check := CalculateBackendChecksum(rnd, body, testBackendSecret)
	request.Header.Set("Spreed-Signaling-Random", rnd)
	request.Header.Set("Spreed-Signaling-Checksum", check)
	u, err := url.Parse(requestUrl)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Spreed-Signaling-Backend", u.Scheme+"://"+u.Host)
	client := &http.Client{}
	return client.Do(request)
}

func expectRoomlistEvent(ch chan *AsyncMessage, msgType string) (*EventServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case message := <-ch:
		if message.Type != "message" || message.Message == nil {
			return nil, fmt.Errorf("Expected message type message, got %+v", message)
		}

		msg := message.Message
		if msg.Type != "event" || msg.Event == nil {
			return nil, fmt.Errorf("Expected message type event, got %+v", msg)
		}
		if msg.Event.Target != "roomlist" || msg.Event.Type != msgType {
			return nil, fmt.Errorf("Expected roomlist %s event, got %+v", msgType, msg.Event)
		}
		return msg.Event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestBackendServer_NoAuth(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{'{', '}'}
	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	res, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected error response, got %s: %s", res.Status, string(body))
	}
}

func TestBackendServer_InvalidAuth(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{'{', '}'}
	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Spreed-Signaling-Random", "hello")
	request.Header.Set("Spreed-Signaling-Checksum", "world")
	client := &http.Client{}
	res, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected error response, got %s: %s", res.Status, string(body))
	}
}

func TestBackendServer_OldCompatAuth(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	userid := "the-user-id"
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &BackendServerRoomRequest{
		Type: "invite",
		Invite: &BackendRoomInviteRequest{
			UserIds: []string{
				userid,
			},
			AllUserIds: []string{
				userid,
			},
			Properties: &roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	rnd := newRandomString(32)
	check := CalculateBackendChecksum(rnd, data, testBackendSecret)
	request.Header.Set("Spreed-Signaling-Random", rnd)
	request.Header.Set("Spreed-Signaling-Checksum", check)
	client := &http.Client{}
	res, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected success, got %s: %s", res.Status, string(body))
	}
}

func TestBackendServer_InvalidBody(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{1, 2, 3, 4} // Invalid JSON
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected error response, got %s: %s", res.Status, string(body))
	}
}

func TestBackendServer_UnsupportedRequest(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	msg := &BackendServerRoomRequest{
		Type: "lala",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	roomId := "the-room-id"
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected error response, got %s: %s", res.Status, string(body))
	}
}

func TestBackendServer_RoomInvite(t *testing.T) {
	for _, backend := range eventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			RunTestBackendServer_RoomInvite(t)
		})
	}
}

type channelEventListener struct {
	ch chan *AsyncMessage
}

func (l *channelEventListener) ProcessAsyncUserMessage(message *AsyncMessage) {
	l.ch <- message
}

func RunTestBackendServer_RoomInvite(t *testing.T) {
	_, _, events, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	userid := "test-userid"
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	backend := hub.backend.GetBackend(u)

	eventsChan := make(chan *AsyncMessage, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	if err := events.RegisterUserListener(userid, backend, listener); err != nil {
		t.Fatal(err)
	}
	defer events.UnregisterUserListener(userid, backend, listener)

	msg := &BackendServerRoomRequest{
		Type: "invite",
		Invite: &BackendRoomInviteRequest{
			UserIds: []string{
				userid,
			},
			AllUserIds: []string{
				userid,
			},
			Properties: &roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	roomId := "the-room-id"
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

	event, err := expectRoomlistEvent(eventsChan, "invite")
	if err != nil {
		t.Error(err)
	} else if event.Invite == nil {
		t.Errorf("Expected invite, got %+v", event)
	} else if event.Invite.RoomId != roomId {
		t.Errorf("Expected room %s, got %+v", roomId, event)
	} else if event.Invite.Properties == nil || !bytes.Equal(*event.Invite.Properties, roomProperties) {
		t.Errorf("Room properties don't match: expected %s, got %s", string(roomProperties), string(*event.Invite.Properties))
	}
}

func TestBackendServer_RoomDisinvite(t *testing.T) {
	for _, backend := range eventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			RunTestBackendServer_RoomDisinvite(t)
		})
	}
}

func RunTestBackendServer_RoomDisinvite(t *testing.T) {
	_, _, events, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	backend := hub.backend.GetBackend(u)

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

	// Ignore "join" events.
	if err := client.DrainMessages(ctx); err != nil {
		t.Error(err)
	}

	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")

	eventsChan := make(chan *AsyncMessage, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	if err := events.RegisterUserListener(testDefaultUserId, backend, listener); err != nil {
		t.Fatal(err)
	}
	defer events.UnregisterUserListener(testDefaultUserId, backend, listener)

	msg := &BackendServerRoomRequest{
		Type: "disinvite",
		Disinvite: &BackendRoomDisinviteRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			SessionIds: []string{
				roomId + "-" + hello.Hello.SessionId,
			},
			AllUserIds: []string{},
			Properties: &roomProperties,
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

	event, err := expectRoomlistEvent(eventsChan, "disinvite")
	if err != nil {
		t.Error(err)
	} else if event.Disinvite == nil {
		t.Errorf("Expected disinvite, got %+v", event)
	} else if event.Disinvite.RoomId != roomId {
		t.Errorf("Expected room %s, got %+v", roomId, event)
	} else if event.Disinvite.Properties != nil {
		t.Errorf("Room properties should be omitted, got %s", string(*event.Disinvite.Properties))
	} else if event.Disinvite.Reason != "disinvited" {
		t.Errorf("Reason should be disinvited, got %s", event.Disinvite.Reason)
	}

	if message, err := client.RunUntilRoomlistDisinvite(ctx); err != nil {
		t.Error(err)
	} else if message.RoomId != roomId {
		t.Errorf("Expected message for room %s, got %s", roomId, message.RoomId)
	}

	if message, err := client.RunUntilMessage(ctx); err != nil && !websocket.IsCloseError(err, websocket.CloseNoStatusReceived) {
		t.Errorf("Received unexpected error %s", err)
	} else if err == nil {
		t.Errorf("Server should have closed the connection, received %+v", *message)
	}
}

func TestBackendServer_RoomDisinviteDifferentRooms(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	if err := client1.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	if err := client2.SendHello(testDefaultUserId); err != nil {
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

	// Join room by id.
	roomId1 := "test-room1"
	if _, err := client1.JoinRoom(ctx, roomId1); err != nil {
		t.Fatal(err)
	}
	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}
	roomId2 := "test-room2"
	if _, err := client2.JoinRoom(ctx, roomId2); err != nil {
		t.Fatal(err)
	}
	if err := client2.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}

	msg := &BackendServerRoomRequest{
		Type: "disinvite",
		Disinvite: &BackendRoomDisinviteRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			SessionIds: []string{
				roomId1 + "-" + hello1.Hello.SessionId,
			},
			AllUserIds: []string{},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId1, data)
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

	if message, err := client1.RunUntilRoomlistDisinvite(ctx); err != nil {
		t.Error(err)
	} else if message.RoomId != roomId1 {
		t.Errorf("Expected message for room %s, got %s", roomId1, message.RoomId)
	}

	if message, err := client1.RunUntilMessage(ctx); err != nil && !websocket.IsCloseError(err, websocket.CloseNoStatusReceived) {
		t.Errorf("Received unexpected error %s", err)
	} else if err == nil {
		t.Errorf("Server should have closed the connection, received %+v", *message)
	}

	if message, err := client2.RunUntilRoomlistDisinvite(ctx); err != nil {
		t.Error(err)
	} else if message.RoomId != roomId1 {
		t.Errorf("Expected message for room %s, got %s", roomId1, message.RoomId)
	}

	msg = &BackendServerRoomRequest{
		Type: "update",
		Update: &BackendRoomUpdateRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			Properties: (*json.RawMessage)(&testRoomProperties),
		},
	}

	data, err = json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	res, err = performBackendRequest(server.URL+"/api/v1/room/"+roomId2, data)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err = io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
	}

	if message, err := client2.RunUntilRoomlistUpdate(ctx); err != nil {
		t.Error(err)
	} else if message.RoomId != roomId2 {
		t.Errorf("Expected message for room %s, got %s", roomId2, message.RoomId)
	}
}

func TestBackendServer_RoomUpdate(t *testing.T) {
	for _, backend := range eventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			RunTestBackendServer_RoomUpdate(t)
		})
	}
}

func RunTestBackendServer_RoomUpdate(t *testing.T) {
	_, _, events, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "the-room-id"
	emptyProperties := json.RawMessage("{}")
	backend := hub.backend.GetBackend(u)
	if backend == nil {
		t.Fatalf("Did not find backend")
	}
	room, err := hub.createRoom(roomId, &emptyProperties, backend)
	if err != nil {
		t.Fatalf("Could not create room: %s", err)
	}
	defer room.Close()

	userid := "test-userid"
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")

	eventsChan := make(chan *AsyncMessage, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	if err := events.RegisterUserListener(userid, backend, listener); err != nil {
		t.Fatal(err)
	}
	defer events.UnregisterUserListener(userid, backend, listener)

	msg := &BackendServerRoomRequest{
		Type: "update",
		Update: &BackendRoomUpdateRequest{
			UserIds: []string{
				userid,
			},
			Properties: &roomProperties,
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

	event, err := expectRoomlistEvent(eventsChan, "update")
	if err != nil {
		t.Error(err)
	} else if event.Update == nil {
		t.Errorf("Expected update, got %+v", event)
	} else if event.Update.RoomId != roomId {
		t.Errorf("Expected room %s, got %+v", roomId, event)
	} else if event.Update.Properties == nil || !bytes.Equal(*event.Update.Properties, roomProperties) {
		t.Errorf("Room properties don't match: expected %s, got %s", string(roomProperties), string(*event.Update.Properties))
	}

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	room = hub.getRoom(roomId)
	if room == nil {
		t.Fatalf("Room %s does not exist", roomId)
	}
	if string(*room.Properties()) != string(roomProperties) {
		t.Errorf("Expected properties %s for room %s, got %s", string(roomProperties), room.Id(), string(*room.Properties()))
	}
}

func TestBackendServer_RoomDelete(t *testing.T) {
	for _, backend := range eventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			RunTestBackendServer_RoomDelete(t)
		})
	}
}

func RunTestBackendServer_RoomDelete(t *testing.T) {
	_, _, events, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "the-room-id"
	emptyProperties := json.RawMessage("{}")
	backend := hub.backend.GetBackend(u)
	if backend == nil {
		t.Fatalf("Did not find backend")
	}
	if _, err := hub.createRoom(roomId, &emptyProperties, backend); err != nil {
		t.Fatalf("Could not create room: %s", err)
	}

	userid := "test-userid"
	eventsChan := make(chan *AsyncMessage, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	if err := events.RegisterUserListener(userid, backend, listener); err != nil {
		t.Fatal(err)
	}
	defer events.UnregisterUserListener(userid, backend, listener)

	msg := &BackendServerRoomRequest{
		Type: "delete",
		Delete: &BackendRoomDeleteRequest{
			UserIds: []string{
				userid,
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

	// A deleted room is signalled as a "disinvite" event.
	event, err := expectRoomlistEvent(eventsChan, "disinvite")
	if err != nil {
		t.Error(err)
	} else if event.Disinvite == nil {
		t.Errorf("Expected disinvite, got %+v", event)
	} else if event.Disinvite.RoomId != roomId {
		t.Errorf("Expected room %s, got %+v", roomId, event)
	} else if event.Disinvite.Properties != nil {
		t.Errorf("Room properties should be omitted, got %s", string(*event.Disinvite.Properties))
	} else if event.Disinvite.Reason != "deleted" {
		t.Errorf("Reason should be deleted, got %s", event.Disinvite.Reason)
	}

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	room := hub.getRoom(roomId)
	if room != nil {
		t.Errorf("Room %s should have been deleted", roomId)
	}
}

func TestBackendServer_ParticipantsUpdatePermissions(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				_, _, _, hub1, _, server1 = CreateBackendServerForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				_, _, hub1, hub2, server1, server2 = CreateBackendServerWithClusteringForTest(t)
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

			session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
			if session1 == nil {
				t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
			}
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId)
			if session2 == nil {
				t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
			}

			// Sessions have all permissions initially (fallback for old-style sessions).
			assertSessionHasPermission(t, session1, PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session1, PERMISSION_MAY_PUBLISH_SCREEN)
			assertSessionHasPermission(t, session2, PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session2, PERMISSION_MAY_PUBLISH_SCREEN)

			// Join room by id.
			roomId := "test-room"
			if room, err := client1.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}
			if room, err := client2.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// Ignore "join" events.
			if err := client1.DrainMessages(ctx); err != nil {
				t.Error(err)
			}
			if err := client2.DrainMessages(ctx); err != nil {
				t.Error(err)
			}

			msg := &BackendServerRoomRequest{
				Type: "participants",
				Participants: &BackendRoomParticipantsRequest{
					Changed: []map[string]interface{}{
						{
							"sessionId":   roomId + "-" + hello1.Hello.SessionId,
							"permissions": []Permission{PERMISSION_MAY_PUBLISH_MEDIA},
						},
						{
							"sessionId":   roomId + "-" + hello2.Hello.SessionId,
							"permissions": []Permission{PERMISSION_MAY_PUBLISH_SCREEN},
						},
					},
					Users: []map[string]interface{}{
						{
							"sessionId":   roomId + "-" + hello1.Hello.SessionId,
							"permissions": []Permission{PERMISSION_MAY_PUBLISH_MEDIA},
						},
						{
							"sessionId":   roomId + "-" + hello2.Hello.SessionId,
							"permissions": []Permission{PERMISSION_MAY_PUBLISH_SCREEN},
						},
					},
				},
			}

			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			// The request could be sent to any of the backend servers.
			res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
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

			// TODO: Use event to wait for asynchronous messages.
			time.Sleep(10 * time.Millisecond)

			assertSessionHasPermission(t, session1, PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasNotPermission(t, session1, PERMISSION_MAY_PUBLISH_SCREEN)
			assertSessionHasNotPermission(t, session2, PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session2, PERMISSION_MAY_PUBLISH_SCREEN)
		})
	}
}

func TestBackendServer_ParticipantsUpdateEmptyPermissions(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

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

	session := hub.GetSessionByPublicId(hello.Hello.SessionId)
	if session == nil {
		t.Fatalf("Session %s does not exist", hello.Hello.SessionId)
	}

	// Sessions have all permissions initially (fallback for old-style sessions).
	assertSessionHasPermission(t, session, PERMISSION_MAY_PUBLISH_MEDIA)
	assertSessionHasPermission(t, session, PERMISSION_MAY_PUBLISH_SCREEN)

	// Join room by id.
	roomId := "test-room"
	room, err := client.JoinRoom(ctx, roomId)
	if err != nil {
		t.Fatal(err)
	}
	if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// Ignore "join" events.
	if err := client.DrainMessages(ctx); err != nil {
		t.Error(err)
	}

	// Updating with empty permissions upgrades to non-old-style and removes
	// all previously available permissions.
	msg := &BackendServerRoomRequest{
		Type: "participants",
		Participants: &BackendRoomParticipantsRequest{
			Changed: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello.Hello.SessionId,
					"permissions": []Permission{},
				},
			},
			Users: []map[string]interface{}{
				{
					"sessionId":   roomId + "-" + hello.Hello.SessionId,
					"permissions": []Permission{},
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

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	assertSessionHasNotPermission(t, session, PERMISSION_MAY_PUBLISH_MEDIA)
	assertSessionHasNotPermission(t, session, PERMISSION_MAY_PUBLISH_SCREEN)
}

func TestBackendServer_ParticipantsUpdateTimeout(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

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

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		msg := &BackendServerRoomRequest{
			Type: "incall",
			InCall: &BackendRoomInCallRequest{
				InCall: json.RawMessage("7"),
				Changed: []map[string]interface{}{
					{
						"sessionId": roomId + "-" + hello1.Hello.SessionId,
						"inCall":    7,
					},
					{
						"sessionId": "unknown-room-session-id",
						"inCall":    3,
					},
				},
				Users: []map[string]interface{}{
					{
						"sessionId": roomId + "-" + hello1.Hello.SessionId,
						"inCall":    7,
					},
					{
						"sessionId": "unknown-room-session-id",
						"inCall":    3,
					},
				},
			},
		}

		data, err := json.Marshal(msg)
		if err != nil {
			t.Error(err)
			return
		}
		res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Error(err)
		}
		if res.StatusCode != 200 {
			t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
		}
	}()

	// Ensure the first request is being processed.
	time.Sleep(100 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		msg := &BackendServerRoomRequest{
			Type: "incall",
			InCall: &BackendRoomInCallRequest{
				InCall: json.RawMessage("7"),
				Changed: []map[string]interface{}{
					{
						"sessionId": roomId + "-" + hello1.Hello.SessionId,
						"inCall":    7,
					},
					{
						"sessionId": roomId + "-" + hello2.Hello.SessionId,
						"inCall":    3,
					},
				},
				Users: []map[string]interface{}{
					{
						"sessionId": roomId + "-" + hello1.Hello.SessionId,
						"inCall":    7,
					},
					{
						"sessionId": roomId + "-" + hello2.Hello.SessionId,
						"inCall":    3,
					},
				},
			},
		}

		data, err := json.Marshal(msg)
		if err != nil {
			t.Error(err)
			return
		}
		res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Error(err)
		}
		if res.StatusCode != 200 {
			t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
		}
	}()

	wg.Wait()
	if t.Failed() {
		return
	}

	msg1_a, err := client1.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	if in_call_1, err := checkMessageParticipantsInCall(msg1_a); err != nil {
		t.Error(err)
	} else if len(in_call_1.Users) != 2 {
		msg1_b, err := client1.RunUntilMessage(ctx)
		if err != nil {
			t.Error(err)
		}
		if in_call_2, err := checkMessageParticipantsInCall(msg1_b); err != nil {
			t.Error(err)
		} else if len(in_call_2.Users) != 2 {
			t.Errorf("Wrong number of users received: %d, expected 2", len(in_call_2.Users))
		}
	}

	msg2_a, err := client2.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	if in_call_1, err := checkMessageParticipantsInCall(msg2_a); err != nil {
		t.Error(err)
	} else if len(in_call_1.Users) != 2 {
		msg2_b, err := client2.RunUntilMessage(ctx)
		if err != nil {
			t.Error(err)
		}
		if in_call_2, err := checkMessageParticipantsInCall(msg2_b); err != nil {
			t.Error(err)
		} else if len(in_call_2.Users) != 2 {
			t.Errorf("Wrong number of users received: %d, expected 2", len(in_call_2.Users))
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second+100*time.Millisecond)
	defer cancel2()

	if msg1_c, _ := client1.RunUntilMessage(ctx2); msg1_c != nil {
		if in_call_2, err := checkMessageParticipantsInCall(msg1_c); err != nil {
			t.Error(err)
		} else if len(in_call_2.Users) != 2 {
			t.Errorf("Wrong number of users received: %d, expected 2", len(in_call_2.Users))
		}
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), time.Second+100*time.Millisecond)
	defer cancel3()
	if msg2_c, _ := client2.RunUntilMessage(ctx3); msg2_c != nil {
		if in_call_2, err := checkMessageParticipantsInCall(msg2_c); err != nil {
			t.Error(err)
		} else if len(in_call_2.Users) != 2 {
			t.Errorf("Wrong number of users received: %d, expected 2", len(in_call_2.Users))
		}
	}
}

func TestBackendServer_InCallAll(t *testing.T) {
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			var hub1 *Hub
			var hub2 *Hub
			var server1 *httptest.Server
			var server2 *httptest.Server

			if isLocalTest(t) {
				_, _, _, hub1, _, server1 = CreateBackendServerForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				_, _, hub1, hub2, server1, server2 = CreateBackendServerWithClusteringForTest(t)
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

			session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
			if session1 == nil {
				t.Fatalf("Could not find session %s", hello1.Hello.SessionId)
			}
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId)
			if session2 == nil {
				t.Fatalf("Could not find session %s", hello2.Hello.SessionId)
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

			room1 := hub1.getRoom(roomId)
			if room1 == nil {
				t.Fatalf("Could not find room %s in hub1", roomId)
			}
			room2 := hub2.getRoom(roomId)
			if room2 == nil {
				t.Fatalf("Could not find room %s in hub2", roomId)
			}

			if room1.IsSessionInCall(session1) {
				t.Errorf("Session %s should not be in room %s", session1.PublicId(), room1.Id())
			}
			if room2.IsSessionInCall(session2) {
				t.Errorf("Session %s should not be in room %s", session2.PublicId(), room2.Id())
			}

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				msg := &BackendServerRoomRequest{
					Type: "incall",
					InCall: &BackendRoomInCallRequest{
						InCall: json.RawMessage("7"),
						All:    true,
					},
				}

				data, err := json.Marshal(msg)
				if err != nil {
					t.Error(err)
					return
				}
				res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
				if err != nil {
					t.Error(err)
					return
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Error(err)
				}
				if res.StatusCode != 200 {
					t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
				}
			}()

			wg.Wait()
			if t.Failed() {
				return
			}

			if msg1_a, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if in_call_1, err := checkMessageParticipantsInCall(msg1_a); err != nil {
				t.Error(err)
			} else if !in_call_1.All {
				t.Errorf("All flag not set in message %+v", in_call_1)
			} else if !bytes.Equal(*in_call_1.InCall, []byte("7")) {
				t.Errorf("Expected inCall flag 7, got %s", string(*in_call_1.InCall))
			}

			if msg2_a, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if in_call_1, err := checkMessageParticipantsInCall(msg2_a); err != nil {
				t.Error(err)
			} else if !in_call_1.All {
				t.Errorf("All flag not set in message %+v", in_call_1)
			} else if !bytes.Equal(*in_call_1.InCall, []byte("7")) {
				t.Errorf("Expected inCall flag 7, got %s", string(*in_call_1.InCall))
			}

			if !room1.IsSessionInCall(session1) {
				t.Errorf("Session %s should be in room %s", session1.PublicId(), room1.Id())
			}
			if !room2.IsSessionInCall(session2) {
				t.Errorf("Session %s should be in room %s", session2.PublicId(), room2.Id())
			}

			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			if message, err := client1.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
			}

			ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel3()

			if message, err := client2.RunUntilMessage(ctx3); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				msg := &BackendServerRoomRequest{
					Type: "incall",
					InCall: &BackendRoomInCallRequest{
						InCall: json.RawMessage("0"),
						All:    true,
					},
				}

				data, err := json.Marshal(msg)
				if err != nil {
					t.Error(err)
					return
				}
				res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
				if err != nil {
					t.Error(err)
					return
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Error(err)
				}
				if res.StatusCode != 200 {
					t.Errorf("Expected successful request, got %s: %s", res.Status, string(body))
				}
			}()

			wg.Wait()
			if t.Failed() {
				return
			}

			if msg1_a, err := client1.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if in_call_1, err := checkMessageParticipantsInCall(msg1_a); err != nil {
				t.Error(err)
			} else if !in_call_1.All {
				t.Errorf("All flag not set in message %+v", in_call_1)
			} else if !bytes.Equal(*in_call_1.InCall, []byte("0")) {
				t.Errorf("Expected inCall flag 0, got %s", string(*in_call_1.InCall))
			}

			if msg2_a, err := client2.RunUntilMessage(ctx); err != nil {
				t.Error(err)
			} else if in_call_1, err := checkMessageParticipantsInCall(msg2_a); err != nil {
				t.Error(err)
			} else if !in_call_1.All {
				t.Errorf("All flag not set in message %+v", in_call_1)
			} else if !bytes.Equal(*in_call_1.InCall, []byte("0")) {
				t.Errorf("Expected inCall flag 0, got %s", string(*in_call_1.InCall))
			}

			if room1.IsSessionInCall(session1) {
				t.Errorf("Session %s should not be in room %s", session1.PublicId(), room1.Id())
			}
			if room2.IsSessionInCall(session2) {
				t.Errorf("Session %s should not be in room %s", session2.PublicId(), room2.Id())
			}

			ctx4, cancel4 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel4()

			if message, err := client1.RunUntilMessage(ctx4); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
			}

			ctx5, cancel5 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel5()

			if message, err := client2.RunUntilMessage(ctx5); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
				t.Error(err)
			} else if message != nil {
				t.Errorf("Expected no message, got %+v", message)
			}
		})
	}
}

func TestBackendServer_RoomMessage(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHello(testDefaultUserId + "1"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
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

	// Ignore "join" events.
	if err := client.DrainMessages(ctx); err != nil {
		t.Error(err)
	}

	messageData := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &BackendServerRoomRequest{
		Type: "message",
		Message: &BackendRoomMessageRequest{
			Data: &messageData,
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

	message, err := client.RunUntilRoomMessage(ctx)
	if err != nil {
		t.Error(err)
	} else if message.RoomId != roomId {
		t.Errorf("Expected message for room %s, got %s", roomId, message.RoomId)
	} else if !bytes.Equal(messageData, *message.Data) {
		t.Errorf("Expected message data %s, got %s", string(messageData), string(*message.Data))
	}
}

func TestBackendServer_TurnCredentials(t *testing.T) {
	_, _, _, _, _, server := CreateBackendServerForTestWithTurn(t)

	q := make(url.Values)
	q.Set("service", "turn")
	q.Set("api", turnApiKey)
	request, err := http.NewRequest("GET", server.URL+"/turn/credentials?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{}
	res, err := client.Do(request)
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

	var cred TurnCredentials
	if err := json.Unmarshal(body, &cred); err != nil {
		t.Fatal(err)
	}

	m := hmac.New(sha1.New, []byte(turnSecret))
	m.Write([]byte(cred.Username)) // nolint
	password := base64.StdEncoding.EncodeToString(m.Sum(nil))
	if cred.Password != password {
		t.Errorf("Expected password %s, got %s", password, cred.Password)
	}
	if cred.TTL != int64((24 * time.Hour).Seconds()) {
		t.Errorf("Expected a TTL of %d, got %d", int64((24 * time.Hour).Seconds()), cred.TTL)
	}
	if !reflect.DeepEqual(cred.URIs, turnServers) {
		t.Errorf("Expected the list of servers as %s, got %s", turnServers, cred.URIs)
	}
}

func TestBackendServer_StatsAllowedIps(t *testing.T) {
	config := goconf.NewConfigFile()
	config.AddOption("stats", "allowed_ips", "127.0.0.1, 192.168.0.1, 192.168.1.1/24")
	_, backend, _, _, _, _ := CreateBackendServerForTestFromConfig(t, config)

	allowed := []string{
		"127.0.0.1",
		"127.0.0.1:1234",
		"192.168.0.1:1234",
		"192.168.1.1:1234",
		"192.168.1.100:1234",
	}
	notAllowed := []string{
		"192.168.0.2:1234",
		"10.1.2.3:1234",
	}

	for _, addr := range allowed {
		t.Run(addr, func(t *testing.T) {
			r1 := &http.Request{
				RemoteAddr: addr,
			}
			if !backend.allowStatsAccess(r1) {
				t.Errorf("should allow %s", addr)
			}

			r2 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-real-ip"): []string{addr},
				},
			}
			if !backend.allowStatsAccess(r2) {
				t.Errorf("should allow %s", addr)
			}

			r3 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-forwarded-for"): []string{addr},
				},
			}
			if !backend.allowStatsAccess(r3) {
				t.Errorf("should allow %s", addr)
			}

			r4 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-forwarded-for"): []string{addr + ", 1.2.3.4:23456"},
				},
			}
			if !backend.allowStatsAccess(r4) {
				t.Errorf("should allow %s", addr)
			}
		})
	}

	for _, addr := range notAllowed {
		t.Run(addr, func(t *testing.T) {
			r := &http.Request{
				RemoteAddr: addr,
			}
			if backend.allowStatsAccess(r) {
				t.Errorf("should not allow %s", addr)
			}
		})
	}
}

func Test_IsNumeric(t *testing.T) {
	numeric := []string{
		"0",
		"1",
		"12345",
	}
	nonNumeric := []string{
		"",
		" ",
		" 0",
		"0 ",
		" 0 ",
		"-1",
		"1.2",
		"1a",
		"a1",
	}
	for _, s := range numeric {
		if !isNumeric(s) {
			t.Errorf("%s should be numeric", s)
		}
	}
	for _, s := range nonNumeric {
		if isNumeric(s) {
			t.Errorf("%s should not be numeric", s)
		}
	}
}

func TestBackendServer_DialoutNoSipBridge(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHelloInternal(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "12345"
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
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected error %d, got %s: %s", http.StatusNotFound, res.Status, string(body))
	}

	var response BackendServerRoomResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	if response.Type != "dialout" || response.Dialout == nil {
		t.Fatalf("expected type dialout, got %s", string(body))
	}
	if response.Dialout.Error == nil {
		t.Fatalf("expected dialout error, got %s", string(body))
	}
	if expected := "no_client_available"; response.Dialout.Error.Code != expected {
		t.Errorf("expected error code %s, got %s", expected, string(body))
	}
}

func TestBackendServer_DialoutAccepted(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHelloInternalWithFeatures([]string{"start-dialout"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "12345"
	callId := "call-123"

	stopped := make(chan struct{})
	go func() {
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
		if url := server.URL + "/"; msg.Internal.Dialout.Backend != url {
			t.Errorf("expected backend %s, got %+v", url, msg)
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
	}()

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
}

func TestBackendServer_DialoutAcceptedCompat(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHelloInternalWithFeatures([]string{"start-dialout"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "12345"
	callId := "call-123"

	stopped := make(chan struct{})
	go func() {
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
		if url := server.URL + "/"; msg.Internal.Dialout.Backend != url {
			t.Errorf("expected backend %s, got %+v", url, msg)
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
	}()

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
}

func TestBackendServer_DialoutRejected(t *testing.T) {
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	if err := client.SendHelloInternalWithFeatures([]string{"start-dialout"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	roomId := "12345"
	errorCode := "error-code"
	errorMessage := "rejected call"

	stopped := make(chan struct{})
	go func() {
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
		if url := server.URL + "/"; msg.Internal.Dialout.Backend != url {
			t.Errorf("expected backend %s, got %+v", url, msg)
		}

		response := &ClientMessage{
			Id:   msg.Id,
			Type: "internal",
			Internal: &InternalClientMessage{
				Type: "dialout",
				Dialout: &DialoutInternalClientMessage{
					Type:  "error",
					Error: NewError(errorCode, errorMessage),
				},
			},
		}
		if err := client.WriteJSON(response); err != nil {
			t.Error(err)
		}
	}()

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
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("Expected error %d, got %s: %s", http.StatusBadGateway, res.Status, string(body))
	}

	var response BackendServerRoomResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	if response.Type != "dialout" || response.Dialout == nil {
		t.Fatalf("expected type dialout, got %s", string(body))
	}
	if response.Dialout.Error == nil {
		t.Fatalf("expected dialout error, got %s", string(body))
	}
	if response.Dialout.Error.Code != errorCode {
		t.Errorf("expected error code %s, got %s", errorCode, string(body))
	}
	if response.Dialout.Error.Message != errorMessage {
		t.Errorf("expected error message %s, got %s", errorMessage, string(body))
	}
}
