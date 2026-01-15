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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	eventstest "github.com/strukturag/nextcloud-spreed-signaling/async/events/test"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	natstest "github.com/strukturag/nextcloud-spreed-signaling/nats/test"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

var (
	turnApiKey        = "TheApiKey"
	turnSecret        = "TheTurnSecret"
	turnServersString = "turn:1.2.3.4:9991?transport=udp,turn:1.2.3.4:9991?transport=tcp"
	turnServers       = strings.Split(turnServersString, ",")
)

func CreateBackendServerForTest(t *testing.T) (*goconf.ConfigFile, *BackendServer, events.AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	return CreateBackendServerForTestFromConfig(t, nil)
}

func CreateBackendServerForTestWithTurn(t *testing.T) (*goconf.ConfigFile, *BackendServer, events.AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	config := goconf.NewConfigFile()
	config.AddOption("turn", "apikey", turnApiKey)
	config.AddOption("turn", "secret", turnSecret)
	config.AddOption("turn", "servers", turnServersString)
	return CreateBackendServerForTestFromConfig(t, config)
}

func CreateBackendServerForTestFromConfig(t *testing.T, config *goconf.ConfigFile) (*goconf.ConfigFile, *BackendServer, events.AsyncEvents, *Hub, *mux.Router, *httptest.Server) {
	require := require.New(t)
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
	require.NoError(err)
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
	events := eventstest.GetAsyncEventsForTest(t)
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	hub, err := NewHub(ctx, config, events, nil, nil, nil, r, "no-version")
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(r))

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

	nats, _ := natstest.StartLocalServer(t)
	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	if config1 == nil {
		config1 = goconf.NewConfigFile()
	}
	u1, err := url.Parse(server1.URL)
	require.NoError(err)
	config1.AddOption("backend", "allowed", u1.Host)
	if u1.Scheme == "http" {
		config1.AddOption("backend", "allowhttp", "true")
	}
	config1.AddOption("backend", "secret", string(testBackendSecret))
	config1.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config1.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config1.AddOption("clients", "internalsecret", string(testInternalSecret))
	config1.AddOption("geoip", "url", "none")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	events1, err := events.NewAsyncEvents(ctx, nats.ClientURL())
	require.NoError(err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(events1.Close(ctx))
	})
	client1, _ := grpctest.NewClientsForTest(t, addr2, nil)
	hub1, err := NewHub(ctx, config1, events1, grpcServer1, client1, nil, r1, "no-version")
	require.NoError(err)

	if config2 == nil {
		config2 = goconf.NewConfigFile()
	}
	u2, err := url.Parse(server2.URL)
	require.NoError(err)
	config2.AddOption("backend", "allowed", u2.Host)
	if u2.Scheme == "http" {
		config2.AddOption("backend", "allowhttp", "true")
	}
	config2.AddOption("backend", "secret", string(testBackendSecret))
	config2.AddOption("sessions", "hashkey", "12345678901234567890123456789012")
	config2.AddOption("sessions", "blockkey", "09876543210987654321098765432109")
	config2.AddOption("clients", "internalsecret", string(testInternalSecret))
	config2.AddOption("geoip", "url", "none")
	events2, err := events.NewAsyncEvents(ctx, nats.ClientURL())
	require.NoError(err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(events2.Close(ctx))
	})
	client2, _ := grpctest.NewClientsForTest(t, addr1, nil)
	hub2, err := NewHub(ctx, config2, events2, grpcServer2, client2, nil, r2, "no-version")
	require.NoError(err)

	b1, err := NewBackendServer(ctx, config1, hub1, "no-version")
	require.NoError(err)
	require.NoError(b1.Start(r1))
	b2, err := NewBackendServer(ctx, config2, hub2, "no-version")
	require.NoError(err)
	require.NoError(b2.Start(r2))

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
	rnd := internal.RandomString(32)
	check := talk.CalculateBackendChecksum(rnd, body, testBackendSecret)
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

func expectAsyncMessage(t *testing.T, ch events.AsyncChannel) (*events.AsyncMessage, bool) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case natsMsg := <-ch:
		var message events.AsyncMessage
		if !assert.NoError(nats.Decode(natsMsg, &message)) {
			return nil, false
		}

		return &message, true
	case <-ctx.Done():
		assert.NoError(ctx.Err())
		return nil, false
	}
}

func expectRoomlistEvent(t *testing.T, ch events.AsyncChannel, msgType string) (*api.EventServerMessage, bool) {
	assert := assert.New(t)
	message, ok := expectAsyncMessage(t, ch)
	if !ok ||
		!assert.Equal("message", message.Type, "invalid message type, got %+v", message) ||
		!assert.NotNil(message.Message, "message missing, got %+v", message) {
		return nil, false
	}

	msg := message.Message
	if !assert.Equal("event", msg.Type, "invalid message type, got %+v", msg) ||
		!assert.NotNil(msg.Event, "event missing, got %+v", msg) ||
		!assert.Equal("roomlist", msg.Event.Target, "invalid event target, got %+v", msg.Event) ||
		!assert.Equal(msgType, msg.Event.Type, "invalid event type, got %+v", msg.Event) {
		return nil, false
	}

	return msg.Event, true
}

func TestBackendServer_NoAuth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{'{', '}'}
	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	require.NoError(err)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	res, err := client.Do(request)
	require.NoError(err)

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusForbidden, res.StatusCode, "Expected error response, got %s: %s", res.Status, string(body))
}

func TestBackendServer_InvalidAuth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{'{', '}'}
	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	require.NoError(err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Spreed-Signaling-Random", "hello")
	request.Header.Set("Spreed-Signaling-Checksum", "world")
	client := &http.Client{}
	res, err := client.Do(request)
	require.NoError(err)

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusForbidden, res.StatusCode, "Expected error response, got %s: %s", res.Status, string(body))
}

func TestBackendServer_OldCompatAuth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	userid := "the-user-id"
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &talk.BackendServerRoomRequest{
		Type: "invite",
		Invite: &talk.BackendRoomInviteRequest{
			UserIds: []string{
				userid,
			},
			Properties: roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)

	request, err := http.NewRequest("POST", server.URL+"/api/v1/room/"+roomId, bytes.NewReader(data))
	require.NoError(err)
	request.Header.Set("Content-Type", "application/json")
	rnd := internal.RandomString(32)
	check := talk.CalculateBackendChecksum(rnd, data, testBackendSecret)
	request.Header.Set("Spreed-Signaling-Random", rnd)
	request.Header.Set("Spreed-Signaling-Checksum", check)
	client := &http.Client{}
	res, err := client.Do(request)
	require.NoError(err)

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected success, got %s: %s", res.Status, string(body))
}

func TestBackendServer_InvalidBody(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	roomId := "the-room-id"
	data := []byte{1, 2, 3, 4} // Invalid JSON
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusBadRequest, res.StatusCode, "Expected error response, got %s: %s", res.Status, string(body))
}

func TestBackendServer_UnsupportedRequest(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTest(t)

	msg := &talk.BackendServerRoomRequest{
		Type: "lala",
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	roomId := "the-room-id"
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusBadRequest, res.StatusCode, "Expected error response, got %s: %s", res.Status, string(body))
}

func TestBackendServer_RoomInvite(t *testing.T) {
	t.Parallel()
	for _, backend := range eventstest.EventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			RunTestBackendServer_RoomInvite(ctx, t)
		})
	}
}

type channelEventListener struct {
	ch events.AsyncChannel
}

func (l *channelEventListener) AsyncChannel() events.AsyncChannel {
	return l.ch
}

func RunTestBackendServer_RoomInvite(ctx context.Context, t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	_, _, asyncEvents, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	require.NoError(err)

	userid := "test-userid"
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	backend := hub.backend.GetBackend(u)

	eventsChan := make(events.AsyncChannel, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	require.NoError(asyncEvents.RegisterUserListener(userid, backend, listener))
	defer func() {
		assert.NoError(asyncEvents.UnregisterUserListener(userid, backend, listener))
	}()

	msg := &talk.BackendServerRoomRequest{
		Type: "invite",
		Invite: &talk.BackendRoomInviteRequest{
			UserIds: []string{
				userid,
			},
			Properties: roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	roomId := "the-room-id"
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s: %s", res.Status, string(body))

	if event, ok := expectRoomlistEvent(t, eventsChan, "invite"); ok && assert.NotNil(event.Invite) {
		assert.Equal(roomId, event.Invite.RoomId)
		assert.Equal(string(roomProperties), string(event.Invite.Properties))
	}
}

func TestBackendServer_RoomDisinvite(t *testing.T) {
	t.Parallel()
	for _, backend := range eventstest.EventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			RunTestBackendServer_RoomDisinvite(ctx, t)
		})
	}
}

func RunTestBackendServer_RoomDisinvite(ctx context.Context, t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	_, _, asyncEvents, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	require.NoError(err)

	backend := hub.backend.GetBackend(u)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	// Join room by id.
	roomId := "test-room"
	if room, ok := client.JoinRoom(ctx, roomId); assert.True(ok) {
		assert.Equal(roomId, room.Room.RoomId)
	}

	// Ignore "join" events.
	client.RunUntilJoined(ctx, hello.Hello)

	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")

	eventsChan := make(events.AsyncChannel, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	require.NoError(asyncEvents.RegisterUserListener(testDefaultUserId, backend, listener))
	defer func() {
		assert.NoError(asyncEvents.UnregisterUserListener(testDefaultUserId, backend, listener))
	}()

	msg := &talk.BackendServerRoomRequest{
		Type: "disinvite",
		Disinvite: &talk.BackendRoomDisinviteRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			SessionIds: []api.RoomSessionId{
				api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId)),
			},
			Properties: roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s: %s", res.Status, string(body))

	if event, ok := expectRoomlistEvent(t, eventsChan, "disinvite"); ok && assert.NotNil(event.Disinvite) {
		assert.Equal(roomId, event.Disinvite.RoomId)
		assert.Equal("disinvited", event.Disinvite.Reason)
		assert.Empty(string(event.Disinvite.Properties))
	}

	if message, ok := client.RunUntilRoomlistUpdate(ctx); ok {
		assert.Equal(roomId, message.RoomId)
	}
	if message, ok := client.RunUntilRoomlistDisinvite(ctx); ok {
		assert.Equal(roomId, message.RoomId)
		assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
	}
	if message, ok := client.RunUntilMessageOrClosed(ctx); ok && message != nil {
		// The client might receive a second disinvite message as both the userid and the session id were disinvited.
		if message, ok := checkMessageRoomlistDisinvite(t, message); ok {
			assert.Equal(roomId, message.RoomId)
			assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
		}
		client.RunUntilClosed(ctx)
	}
}

func TestBackendServer_RoomDisinviteDifferentRooms(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client2.CloseWithBye()

	// Join room by id.
	roomId1 := "test-room1"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId1)
	require.True(client1.RunUntilJoined(ctx, hello1.Hello))
	roomId2 := "test-room2"
	MustSucceed2(t, client2.JoinRoom, ctx, roomId2)
	require.True(client2.RunUntilJoined(ctx, hello2.Hello))

	msg := &talk.BackendServerRoomRequest{
		Type: "disinvite",
		Disinvite: &talk.BackendRoomDisinviteRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			SessionIds: []api.RoomSessionId{
				api.RoomSessionId(fmt.Sprintf("%s-%s", roomId1, hello1.Hello.SessionId)),
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId1, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	if message, ok := client1.RunUntilRoomlistUpdate(ctx); ok {
		assert.Equal(roomId1, message.RoomId)
	}
	if message, ok := client1.RunUntilRoomlistDisinvite(ctx); ok {
		assert.Equal(roomId1, message.RoomId)
		assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
	}
	if message, ok := client1.RunUntilMessageOrClosed(ctx); ok && message != nil {
		// The client might receive a second disinvite message as both the userid and the session id were disinvited.
		if message, ok := checkMessageRoomlistDisinvite(t, message); ok {
			assert.Equal(roomId1, message.RoomId)
			assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
		}
		client1.RunUntilClosed(ctx)
	}

	if message, ok := client2.RunUntilRoomlistDisinvite(ctx); ok {
		assert.Equal(roomId1, message.RoomId)
		assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
	}

	msg = &talk.BackendServerRoomRequest{
		Type: "update",
		Update: &talk.BackendRoomUpdateRequest{
			Properties: testRoomProperties,
		},
	}

	data, err = json.Marshal(msg)
	require.NoError(err)
	res, err = performBackendRequest(server.URL+"/api/v1/room/"+roomId2, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err = io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	if message, ok := client2.RunUntilRoomlistUpdate(ctx); ok {
		assert.Equal(roomId2, message.RoomId)
	}
}

func TestBackendServer_RoomDisinviteClustered(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, hub1, hub2, server1, server2 := CreateBackendServerWithClusteringForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	// Join room by id.
	roomId := "test-room1"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	msg := &talk.BackendServerRoomRequest{
		Type: "disinvite",
		Disinvite: &talk.BackendRoomDisinviteRequest{
			SessionIds: []api.RoomSessionId{
				api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, hello2.Hello.SessionId)),
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	if message, ok := client1.RunUntilRoomlistUpdate(ctx); ok {
		assert.Equal(roomId, message.RoomId)
	}

	if message, ok := client2.RunUntilRoomlistUpdate(ctx); ok {
		assert.Equal(roomId, message.RoomId)
	}
	if message, ok := client2.RunUntilRoomlistDisinvite(ctx); ok {
		assert.Equal(roomId, message.RoomId)
		assert.Equal(api.DisinviteReasonDisinvited, message.Reason)
	}
	client2.RunUntilClosed(ctx)

	client1.RunUntilLeft(ctx, hello2.Hello)
}

func TestBackendServer_RoomUpdate(t *testing.T) {
	t.Parallel()
	for _, backend := range eventstest.EventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			RunTestBackendServer_RoomUpdate(ctx, t)
		})
	}
}

func RunTestBackendServer_RoomUpdate(ctx context.Context, t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	_, _, asyncEvents, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	require.NoError(err)

	roomId := "the-room-id"
	emptyProperties := json.RawMessage("{}")
	backend := hub.backend.GetBackend(u)
	require.NotNil(backend, "Did not find backend")
	room, err := hub.CreateRoom(roomId, emptyProperties, backend)
	require.NoError(err, "Could not create room")
	defer room.Close()

	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")

	eventsChan := make(events.AsyncChannel, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	require.NoError(asyncEvents.RegisterRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(asyncEvents.UnregisterRoomListener(roomId, backend, listener))
	}()

	msg := &talk.BackendServerRoomRequest{
		Type: "update",
		Update: &talk.BackendRoomUpdateRequest{
			Properties: roomProperties,
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

	if event, ok := expectRoomlistEvent(t, eventsChan, "update"); ok && assert.NotNil(event.Update) {
		assert.Equal(roomId, event.Update.RoomId)
		assert.Equal(string(roomProperties), string(event.Update.Properties))
	}

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	room = hub.getRoom(roomId)
	require.NotNil(room, "Room %s does not exist", roomId)
	assert.Equal(string(roomProperties), string(room.Properties()))
}

func TestBackendServer_RoomDelete(t *testing.T) {
	t.Parallel()
	for _, backend := range eventstest.EventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			RunTestBackendServer_RoomDelete(ctx, t)
		})
	}
}

func RunTestBackendServer_RoomDelete(ctx context.Context, t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	_, _, asyncEvents, hub, _, server := CreateBackendServerForTest(t)

	u, err := url.Parse(server.URL)
	require.NoError(err)

	roomId := "the-room-id"
	emptyProperties := json.RawMessage("{}")
	backend := hub.backend.GetBackend(u)
	require.NotNil(backend, "Did not find backend")
	_, err = hub.CreateRoom(roomId, emptyProperties, backend)
	require.NoError(err)

	eventsChan := make(events.AsyncChannel, 1)
	listener := &channelEventListener{
		ch: eventsChan,
	}
	require.NoError(asyncEvents.RegisterBackendRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(asyncEvents.UnregisterBackendRoomListener(roomId, backend, listener))
	}()

	msg := &talk.BackendServerRoomRequest{
		Type:   "delete",
		Delete: &talk.BackendRoomDeleteRequest{},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	// A deleted room is signalled as a backend room event.
	if message, ok := expectAsyncMessage(t, eventsChan); ok {
		assert.Equal("room", message.Type)
		if assert.NotNil(message.Room) {
			assert.Equal("delete", message.Room.Type)
		}
	}

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	room := hub.getRoom(roomId)
	assert.Nil(room, "Room %s should have been deleted", roomId)
}

func TestBackendServer_ParticipantsUpdatePermissions(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			assert := assert.New(t)
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

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
			require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId)
			require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

			// Sessions have all permissions initially (fallback for old-style sessions).
			assertSessionHasPermission(t, session1, api.PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session1, api.PERMISSION_MAY_PUBLISH_SCREEN)
			assertSessionHasPermission(t, session2, api.PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session2, api.PERMISSION_MAY_PUBLISH_SCREEN)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Ignore "join" events.
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			msg := &talk.BackendServerRoomRequest{
				Type: "participants",
				Participants: &talk.BackendRoomParticipantsRequest{
					Changed: api.UserDataList{
						{
							"sessionId":   fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
							"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_MEDIA},
						},
						{
							"sessionId":   fmt.Sprintf("%s-%s", roomId, hello2.Hello.SessionId),
							"permissions": []api.Permission{api.PERMISSION_MAY_PUBLISH_SCREEN},
						},
					},
				},
			}

			data, err := json.Marshal(msg)
			require.NoError(err)
			// The request could be sent to any of the backend servers.
			res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
			require.NoError(err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			assert.NoError(err)
			assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

			// TODO: Use event to wait for asynchronous messages.
			time.Sleep(10 * time.Millisecond)

			assertSessionHasPermission(t, session1, api.PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasNotPermission(t, session1, api.PERMISSION_MAY_PUBLISH_SCREEN)
			assertSessionHasNotPermission(t, session2, api.PERMISSION_MAY_PUBLISH_MEDIA)
			assertSessionHasPermission(t, session2, api.PERMISSION_MAY_PUBLISH_SCREEN)
		})
	}
}

func TestBackendServer_ParticipantsUpdateEmptyPermissions(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	session := hub.GetSessionByPublicId(hello.Hello.SessionId)
	assert.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

	// Sessions have all permissions initially (fallback for old-style sessions).
	assertSessionHasPermission(t, session, api.PERMISSION_MAY_PUBLISH_MEDIA)
	assertSessionHasPermission(t, session, api.PERMISSION_MAY_PUBLISH_SCREEN)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Ignore "join" events.
	client.RunUntilJoined(ctx, hello.Hello)

	// Updating with empty permissions upgrades to non-old-style and removes
	// all previously available permissions.
	msg := &talk.BackendServerRoomRequest{
		Type: "participants",
		Participants: &talk.BackendRoomParticipantsRequest{
			Changed: api.UserDataList{
				{
					"sessionId":   fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId),
					"permissions": []api.Permission{},
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

	// TODO: Use event to wait for asynchronous messages.
	time.Sleep(10 * time.Millisecond)

	assertSessionHasNotPermission(t, session, api.PERMISSION_MAY_PUBLISH_MEDIA)
	assertSessionHasNotPermission(t, session, api.PERMISSION_MAY_PUBLISH_SCREEN)
}

func TestBackendServer_ParticipantsUpdateTimeout(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		msg := &talk.BackendServerRoomRequest{
			Type: "incall",
			InCall: &talk.BackendRoomInCallRequest{
				InCall: json.RawMessage("7"),
				Changed: api.UserDataList{
					{
						"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
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
		if !assert.NoError(err) {
			return
		}
		res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
		if !assert.NoError(err) {
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		assert.NoError(err)
		assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))
	}()

	// Ensure the first request is being processed.
	time.Sleep(100 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		msg := &talk.BackendServerRoomRequest{
			Type: "incall",
			InCall: &talk.BackendRoomInCallRequest{
				InCall: json.RawMessage("7"),
				Changed: api.UserDataList{
					{
						"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
						"inCall":    7,
					},
					{
						"sessionId": fmt.Sprintf("%s-%s", roomId, hello2.Hello.SessionId),
						"inCall":    3,
					},
				},
			},
		}

		data, err := json.Marshal(msg)
		if !assert.NoError(err) {
			return
		}
		res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
		if !assert.NoError(err) {
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		assert.NoError(err)
		assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))
	}()

	wg.Wait()
	if t.Failed() {
		return
	}

	if msg1_a, ok := client1.RunUntilMessage(ctx); ok {
		if in_call_1, ok := checkMessageParticipantsInCall(t, msg1_a); ok {
			if len(in_call_1.Users) != 2 {
				msg1_b, _ := client1.RunUntilMessage(ctx)
				if in_call_2, ok := checkMessageParticipantsInCall(t, msg1_b); ok {
					assert.Len(in_call_2.Users, 2)
				}
			}
		}
	}

	if msg2_a, ok := client2.RunUntilMessage(ctx); ok {
		if in_call_1, ok := checkMessageParticipantsInCall(t, msg2_a); ok {
			if len(in_call_1.Users) != 2 {
				msg2_b, _ := client2.RunUntilMessage(ctx)
				if in_call_2, ok := checkMessageParticipantsInCall(t, msg2_b); ok {
					assert.Len(in_call_2.Users, 2)
				}
			}
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	client1.RunUntilErrorIs(ctx2, context.DeadlineExceeded)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()

	client2.RunUntilErrorIs(ctx3, context.DeadlineExceeded)
}

func TestBackendServer_InCallAll(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			assert := assert.New(t)
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

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId)
			require.NotNil(session1, "Could not find session %s", hello1.Hello.SessionId)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId)
			require.NotNil(session2, "Could not find session %s", hello2.Hello.SessionId)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			room1 := hub1.getRoom(roomId)
			require.NotNil(room1, "Could not find room %s in hub1", roomId)
			room2 := hub2.getRoom(roomId)
			require.NotNil(room2, "Could not find room %s in hub2", roomId)

			assert.False(room1.IsSessionInCall(session1), "Session %s should not be in room %s", session1.PublicId(), room1.Id())
			assert.False(room2.IsSessionInCall(session2), "Session %s should not be in room %s", session2.PublicId(), room2.Id())

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				msg := &talk.BackendServerRoomRequest{
					Type: "incall",
					InCall: &talk.BackendRoomInCallRequest{
						InCall: json.RawMessage("7"),
						All:    true,
					},
				}

				data, err := json.Marshal(msg)
				if !assert.NoError(err) {
					return
				}
				res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
				if !assert.NoError(err) {
					return
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				assert.NoError(err)
				assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))
			}()

			wg.Wait()
			if t.Failed() {
				return
			}

			if msg1_a, ok := client1.RunUntilMessage(ctx); ok {
				if in_call_1, ok := checkMessageParticipantsInCall(t, msg1_a); ok {
					assert.True(in_call_1.All, "All flag not set in message %+v", in_call_1)
					assert.Equal("7", string(in_call_1.InCall))
				}
			}

			if msg2_a, ok := client2.RunUntilMessage(ctx); ok {
				if in_call_1, ok := checkMessageParticipantsInCall(t, msg2_a); ok {
					assert.True(in_call_1.All, "All flag not set in message %+v", in_call_1)
					assert.Equal("7", string(in_call_1.InCall))
				}
			}

			assert.True(room1.IsSessionInCall(session1), "Session %s should be in room %s", session1.PublicId(), room1.Id())
			assert.True(room2.IsSessionInCall(session2), "Session %s should be in room %s", session2.PublicId(), room2.Id())

			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			client1.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)

			ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel3()

			client2.RunUntilErrorIs(ctx3, ErrNoMessageReceived, context.DeadlineExceeded)

			wg.Add(1)
			go func() {
				defer wg.Done()
				msg := &talk.BackendServerRoomRequest{
					Type: "incall",
					InCall: &talk.BackendRoomInCallRequest{
						InCall: json.RawMessage("0"),
						All:    true,
					},
				}

				data, err := json.Marshal(msg)
				if !assert.NoError(err) {
					return
				}
				res, err := performBackendRequest(server1.URL+"/api/v1/room/"+roomId, data)
				if !assert.NoError(err) {
					return
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				assert.NoError(err)
				assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))
			}()

			wg.Wait()
			if t.Failed() {
				return
			}

			if msg1_a, ok := client1.RunUntilMessage(ctx); ok {
				if in_call_1, ok := checkMessageParticipantsInCall(t, msg1_a); ok {
					assert.True(in_call_1.All, "All flag not set in message %+v", in_call_1)
					assert.Equal("0", string(in_call_1.InCall))
				}
			}

			if msg2_a, ok := client2.RunUntilMessage(ctx); ok {
				if in_call_1, ok := checkMessageParticipantsInCall(t, msg2_a); ok {
					assert.True(in_call_1.All, "All flag not set in message %+v", in_call_1)
					assert.Equal("0", string(in_call_1.InCall))
				}
			}

			assert.False(room1.IsSessionInCall(session1), "Session %s should not be in room %s", session1.PublicId(), room1.Id())
			assert.False(room2.IsSessionInCall(session2), "Session %s should not be in room %s", session2.PublicId(), room2.Id())

			ctx4, cancel4 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel4()

			client1.RunUntilErrorIs(ctx4, ErrNoMessageReceived, context.DeadlineExceeded)

			ctx5, cancel5 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel5()

			client2.RunUntilErrorIs(ctx5, ErrNoMessageReceived, context.DeadlineExceeded)
		})
	}
}

func TestBackendServer_RoomMessage(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client.CloseWithBye()

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Ignore "join" events.
	client.RunUntilJoined(ctx, hello.Hello)

	messageData := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &talk.BackendServerRoomRequest{
		Type: "message",
		Message: &talk.BackendRoomMessageRequest{
			Data: messageData,
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

	if message, ok := client.RunUntilRoomMessage(ctx); ok {
		assert.Equal(roomId, message.RoomId)
		assert.Equal(string(messageData), string(message.Data))
	}
}

func TestBackendServer_TurnCredentials(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, _, _, server := CreateBackendServerForTestWithTurn(t)

	q := make(url.Values)
	q.Set("service", "turn")
	q.Set("api", turnApiKey)
	request, err := http.NewRequest("GET", server.URL+"/turn/credentials?"+q.Encode(), nil)
	require.NoError(err)
	client := &http.Client{}
	res, err := client.Do(request)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	var cred talk.TurnCredentials
	require.NoError(json.Unmarshal(body, &cred))

	m := hmac.New(sha1.New, []byte(turnSecret))
	m.Write([]byte(cred.Username)) // nolint
	password := base64.StdEncoding.EncodeToString(m.Sum(nil))
	assert.Equal(password, cred.Password)
	assert.InEpsilon((24 * time.Hour).Seconds(), cred.TTL, 0.0001)
	assert.Equal(turnServers, cred.URIs)
}

func TestBackendServer_StatsAllowedIps(t *testing.T) {
	t.Parallel()
	config := goconf.NewConfigFile()
	config.AddOption("app", "trustedproxies", "1.2.3.4")
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
			t.Parallel()
			assert := assert.New(t)
			r1 := &http.Request{
				RemoteAddr: addr,
			}
			assert.True(backend.allowStatsAccess(r1), "should allow %s", addr)

			if host, _, err := net.SplitHostPort(addr); err == nil {
				addr = host
			}

			r2 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-real-ip"): []string{addr},
				},
			}
			assert.True(backend.allowStatsAccess(r2), "should allow %s", addr)

			r3 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-forwarded-for"): []string{addr},
				},
			}
			assert.True(backend.allowStatsAccess(r3), "should allow %s", addr)

			r4 := &http.Request{
				RemoteAddr: "1.2.3.4:12345",
				Header: http.Header{
					textproto.CanonicalMIMEHeaderKey("x-forwarded-for"): []string{addr + ", 1.2.3.4:23456"},
				},
			}
			assert.True(backend.allowStatsAccess(r4), "should allow %s", addr)
		})
	}

	for _, addr := range notAllowed {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{
				RemoteAddr: addr,
			}
			assert.False(t, backend.allowStatsAccess(r), "should not allow %s", addr)
		})
	}
}

func Test_IsNumeric(t *testing.T) {
	t.Parallel()
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
		assert.True(t, isNumeric(s), "%s should be numeric", s)
	}
	for _, s := range nonNumeric {
		assert.False(t, isNumeric(s), "%s should not be numeric", s)
	}
}

func TestBackendServer_DialoutNoSipBridge(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloInternal())

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, client.RunUntilHello, ctx)

	roomId := "12345"
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
	require.Equal(http.StatusNotFound, res.StatusCode, "Expected error, got %s", string(body))

	var response talk.BackendServerRoomResponse
	if assert.NoError(json.Unmarshal(body, &response)) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) &&
			assert.NotNil(response.Dialout.Error) {
			assert.Equal("no_client_available", response.Dialout.Error.Code)
		}
	}
}

func TestBackendServer_DialoutAccepted(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, client.RunUntilHello, ctx)

	roomId := "12345"
	callId := "call-123"

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		msg, ok := client.RunUntilMessage(ctx)
		if !ok {
			return
		}

		if !assert.Equal("internal", msg.Type) ||
			!assert.NotNil(msg.Internal) ||
			!assert.Equal("dialout", msg.Internal.Type) ||
			!assert.NotNil(msg.Internal.Dialout) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)
		assert.Equal(server.URL+"/", msg.Internal.Dialout.Backend)

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
	}()

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
	if err := json.Unmarshal(body, &response); assert.NoError(err) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) {
			assert.Nil(response.Dialout.Error, "expected dialout success, got %s", string(body))
			assert.Equal(callId, response.Dialout.CallId)
		}
	}
}

func TestBackendServer_DialoutAcceptedCompat(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, client.RunUntilHello, ctx)

	roomId := "12345"
	callId := "call-123"

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		msg, ok := client.RunUntilMessage(ctx)
		if !ok {
			return
		}

		if !assert.Equal("internal", msg.Type) ||
			!assert.NotNil(msg.Internal) ||
			!assert.Equal("dialout", msg.Internal.Type) ||
			!assert.NotNil(msg.Internal.Dialout) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)
		assert.Equal(server.URL+"/", msg.Internal.Dialout.Backend)

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
	}()

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
	if err := json.Unmarshal(body, &response); assert.NoError(err) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) {
			assert.Nil(response.Dialout.Error, "expected dialout success, got %s", string(body))
			assert.Equal(callId, response.Dialout.CallId)
		}
	}
}

func TestBackendServer_DialoutRejected(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, client.RunUntilHello, ctx)

	roomId := "12345"
	errorCode := "error-code"
	errorMessage := "rejected call"

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		msg, ok := client.RunUntilMessage(ctx)
		if !ok {
			return
		}

		if !assert.Equal("internal", msg.Type) ||
			!assert.NotNil(msg.Internal) ||
			!assert.Equal("dialout", msg.Internal.Type) ||
			!assert.NotNil(msg.Internal.Dialout) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)
		assert.Equal(server.URL+"/", msg.Internal.Dialout.Backend)

		response := &api.ClientMessage{
			Id:   msg.Id,
			Type: "internal",
			Internal: &api.InternalClientMessage{
				Type: "dialout",
				Dialout: &api.DialoutInternalClientMessage{
					Type:  "error",
					Error: api.NewError(errorCode, errorMessage),
				},
			},
		}
		assert.NoError(client.WriteJSON(response))
	}()

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
	require.Equal(http.StatusBadGateway, res.StatusCode, "Expected error, got %s", string(body))

	var response talk.BackendServerRoomResponse
	if err := json.Unmarshal(body, &response); assert.NoError(err) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) &&
			assert.NotNil(response.Dialout.Error, "expected dialout error, got %s", string(body)) {
			assert.Equal(errorCode, response.Dialout.Error.Code)
			assert.Equal(errorMessage, response.Dialout.Error.Message)
		}
	}
}

func TestBackendServer_DialoutFirstFailed(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	_, _, _, hub, _, server := CreateBackendServerForTest(t)

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloInternalWithFeatures([]string{"start-dialout"}))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	MustSucceed1(t, client1.RunUntilHello, ctx)
	MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "12345"
	callId := "call-123"

	var returnedError atomic.Bool

	var wg sync.WaitGroup
	runClient := func(client *TestClient) {
		defer wg.Done()

		msg, ok := client.RunUntilMessage(ctx)
		if !ok {
			return
		}

		if !assert.Equal("internal", msg.Type) ||
			!assert.NotNil(msg.Internal) ||
			!assert.Equal("dialout", msg.Internal.Type) ||
			!assert.NotNil(msg.Internal.Dialout) {
			return
		}

		assert.Equal(roomId, msg.Internal.Dialout.RoomId)
		assert.Equal(server.URL+"/", msg.Internal.Dialout.Backend)

		var dialout *api.DialoutInternalClientMessage
		// The first session should return an error to make sure the second is retried afterwards.
		if returnedError.CompareAndSwap(false, true) {
			errorCode := "error-code"
			errorMessage := "rejected call"

			dialout = &api.DialoutInternalClientMessage{
				Type:  "error",
				Error: api.NewError(errorCode, errorMessage),
			}
		} else {
			dialout = &api.DialoutInternalClientMessage{
				Type:   "status",
				RoomId: msg.Internal.Dialout.RoomId,
				Status: &api.DialoutStatusInternalClientMessage{
					Status: "accepted",
					CallId: callId,
				},
			}
		}

		response := &api.ClientMessage{
			Id:   msg.Id,
			Type: "internal",
			Internal: &api.InternalClientMessage{
				Type:    "dialout",
				Dialout: dialout,
			},
		}
		assert.NoError(client.WriteJSON(response))
	}

	wg.Add(1)
	go runClient(client1)
	wg.Add(1)
	go runClient(client2)

	defer func() {
		wg.Wait()
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
	if err := json.Unmarshal(body, &response); assert.NoError(err) {
		assert.Equal("dialout", response.Type)
		if assert.NotNil(response.Dialout) {
			assert.Nil(response.Dialout.Error, "expected dialout success, got %s", string(body))
			assert.Equal(callId, response.Dialout.CallId)
		}
	}
}
