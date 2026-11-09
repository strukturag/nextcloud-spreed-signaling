/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/async/events"
	eventstest "github.com/strukturag/nextcloud-spreed-signaling/v2/async/events/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/grpc"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/v2/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/talk"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/test"
)

func reserveRoomJoin(hub *Hub, room *Room) {
	hub.ru.Lock()
	defer hub.ru.Unlock()
	room.reserveJoin()
}

func TestRoom_LastSessionLeavesWhileJoining(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	client1.RunUntilJoined(ctx, hello1.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)

	// Session 2 is about to join the room while the last session leaves.
	reserveRoomJoin(hub, room)

	roomMsg = MustSucceed2(t, client1.JoinRoom, ctx, "")
	require.Empty(roomMsg.Room.RoomId)

	// The room may not be closed while a session is about to join.
	assert.False(room.IsClosed())
	assert.Same(room, hub.getRoom(roomId))

	session2, ok := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)
	join, err := room.startAddSession(session2, nil, true)
	require.NoError(err)
	room.finishAddSession(join)
	assert.True(room.HasSession(session2))
	assert.False(room.IsClosed())
	assert.Same(room, hub.getRoom(roomId))

	// The reservation was consumed, so the room gets closed once the last
	// session leaves.
	assert.False(room.RemoveSession(session2))
	assert.True(room.IsClosed())
	assert.Nil(hub.getRoom(roomId))
}

func TestRoom_CloseOnlyRemovesSameRoom(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	session1, ok := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.True(ok)
	session2, ok := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)

	// Use unique room id as the metrics are global.
	roomId := "test-room-close-same"
	backend := session1.Backend()
	sessionsGauge := func() int {
		return int(testutil.ToFloat64(statsRoomSessionsCurrent.WithLabelValues(backend.Id(), roomId, string(api.HelloClientTypeClient))))
	}

	// Simulate that rooms are removed from the hub without being closed.
	makeStale := func(room *Room) {
		hub.ru.Lock()
		defer hub.ru.Unlock()
		assert.True(hub.removeRoomLocked(room))
	}

	room1, err := hub.CreateRoom(roomId, nil, backend)
	require.NoError(err)
	require.NoError(room1.AddSession(session1, nil))
	makeStale(room1)

	room2, err := hub.CreateRoom(roomId, nil, backend)
	require.NoError(err)
	defer room2.Close()
	require.NoError(room2.AddSession(session2, nil))
	assert.Equal(2, sessionsGauge())

	// Closing the stale room may not remove the new room or its metrics.
	assert.Len(room1.Close(), 1)
	assert.True(room1.IsClosed())
	assert.Same(room2, hub.getRoom(roomId))
	assert.False(room2.IsClosed())
	assert.Equal(1, sessionsGauge())

	makeStale(room2)
	room3, err := hub.CreateRoom(roomId, nil, backend)
	require.NoError(err)
	defer room3.Close()
	require.NoError(room3.AddSession(session1, nil))
	assert.Equal(2, sessionsGauge())

	// The last session leaving the stale room may not remove the new room or
	// its metrics.
	assert.False(room2.RemoveSession(session2))
	assert.True(room2.IsClosed())
	assert.Same(room3, hub.getRoom(roomId))
	assert.False(room3.IsClosed())
	assert.Equal(1, sessionsGauge())

	assert.False(room3.RemoveSession(session1))
	assert.True(room3.IsClosed())
	assert.Nil(hub.getRoom(roomId))
	assert.Equal(0, sessionsGauge())
}

func TestRoom_DeleteWhileJoining(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	roomId := "test-room"
	room, err := hub.CreateRoom(roomId, nil, session.Backend())
	require.NoError(err)

	reserveRoomJoin(hub, room)
	assert.Empty(room.Close())
	assert.Nil(hub.getRoom(roomId))

	_, err = room.startAddSession(session, nil, true)
	assert.ErrorIs(err, ErrRoomClosed)
	assert.ErrorIs(room.AddSession(session, nil), ErrRoomClosed)
	assert.False(room.HasSession(session))
	assert.Nil(room.Close())
}

func TestRoom_FetchUsersAfterClose(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)
	client1.RunUntilJoined(ctx, hello1.Hello)

	room1 := hub1.getRoom(roomId)
	require.NotNil(room1)
	room1.addUser(hello1.Hello.SessionId, api.UserData{
		"sessionId": hello1.Hello.SessionId,
		"inCall":    FlagInCall,
	})

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)

	room2, err := hub2.CreateRoom(roomId, nil, session2.Backend())
	require.NoError(err)
	room2.Close()

	// Applying the initial users to a closed room may not modify it.
	_, users, failed := room2.fetchInitialState(ctx, hub2.rpcClients.GetClients())
	assert.Len(users, 1)
	assert.Empty(failed)
	room2.applyInitialState(nil, users, true)
	assert.Empty(room2.getUsers())

	room3, err := hub2.CreateRoom(roomId, nil, session2.Backend())
	require.NoError(err)
	defer room3.Close()

	waitForRoomSynced(ctx, t, room3)
	if users := room3.getUsers(); assert.Len(users, 1) {
		sid, found := users[0].SessionId()
		assert.True(found)
		assert.Equal(hello1.Hello.SessionId, sid)
	}
}

func TestRoom_SessionLeavesWhileJoining(t *testing.T) {
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
				hub1, _, _, server1 = CreateHubForTest(t)

				hub2 = hub1
				server2 = server1
			} else {
				hub1, hub2, server1, server2 = CreateClusteredHubsForTest(t)
			}

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)
			client1.RunUntilJoined(ctx, hello1.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)
			session1.SetPermissions([]api.Permission{api.PERMISSION_TRANSIENT_DATA})
			session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			require.True(ok)

			room := hub2.GetRoomForBackend(roomId, session2.Backend())
			if room == nil {
				var err error
				room, err = hub2.CreateRoom(roomId, nil, session2.Backend())
				require.NoError(err)
			}

			// Monitor the events published to the room.
			listener := eventstest.NewListener(t)
			require.NoError(hub1.events.RegisterRoomListener(roomId, session1.Backend(), listener))
			defer func() {
				assert.NoError(hub1.events.UnregisterRoomListener(roomId, session1.Backend(), listener))
			}()

			join, err := room.startAddSession(session2, nil, false)
			require.NoError(err)

			// The session leaves while it is still being added, the "leave" event
			// may not be published before the "join" event.
			room.RemoveSession(session2)
			listener.AssertNoMessage(200 * time.Millisecond)
			assertNoMoreMessages(t, client1)

			// Neither a "join" nor a "leave" event is published for the session that
			// already left, as no other session could know about it.
			room.finishAddSession(join)
			listener.AssertNoMessage(200 * time.Millisecond)
			assert.False(room.HasSession(session2))

			// The removed session may not receive transient data.
			require.NoError(client1.SetTransientData("foo", "bar", 0))
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "foo", "bar", nil)
			}
			assertNoMoreMessages(t, client1, client2)
		})
	}
}

func TestRoom_DeletedWhileJoining(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	roomId := "test-room"
	room, err := hub.CreateRoom(roomId, nil, session.Backend())
	require.NoError(err)

	// Monitor the events published to the room.
	listener := eventstest.NewListener(t)
	require.NoError(hub.events.RegisterRoomListener(roomId, session.Backend(), listener))
	defer func() {
		assert.NoError(hub.events.UnregisterRoomListener(roomId, session.Backend(), listener))
	}()

	// The room is deleted while the session is still being added.
	join, err := room.startAddSession(session, nil, false)
	require.NoError(err)
	assert.Len(room.Close(), 1)

	// No events are published to the closed room.
	room.finishAddSession(join)
	listener.AssertNoMessage(200 * time.Millisecond)

	// The session was not added as listener for transient data.
	assert.True(room.transientData.Set("foo", "bar"))
	assertNoMoreMessages(t, client)
}

func TestRoom_JoiningSessionNotSentToOthers(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	roomId := "test-room"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	client1.RunUntilJoined(ctx, hello1.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)
	session2, ok := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)

	// A session that is still being added is not sent to other sessions joining
	// the room, it publishes its "join" event once it has been added.
	join, err := room.startAddSession(session2, nil, false)
	require.NoError(err)
	room.notifySessionJoined(hello1.Hello.SessionId)
	assertNoMoreMessages(t, client1)

	// So no other session needs to be notified if it leaves before.
	room.RemoveSession(session2)
	room.finishAddSession(join)
	assert.False(room.HasSession(session2))
	assertNoMoreMessages(t, client1, client2)
}

func TestRoom_JoinDeletedRoom(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()
	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	// Simulate that the room is deleted while the session joins it.
	roomId := "test-room"
	room, err := hub.CreateRoom(roomId, nil, session.Backend())
	require.NoError(err)
	room.Close()
	internalRoomId := getRoomIdForBackend(roomId, session.Backend())
	hub.ru.Lock()
	hub.rooms[internalRoomId] = room
	hub.ru.Unlock()
	defer func() {
		hub.ru.Lock()
		defer hub.ru.Unlock()
		hub.removeRoomLocked(room)
	}()

	require.NoError(client.WriteJSON(&api.ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    roomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId)),
		},
	}))

	// The client is notified that the room was deleted and never gets a
	// successful response. The connection gets closed after the "disinvite", so
	// the (empty) room response might not be received.
	var disinvited bool
	for {
		msg, ok := client.RunUntilMessageOrClosed(ctx)
		if !ok || msg == nil {
			break
		}

		switch msg.Type {
		case "room":
			assert.Equal("ABCD", msg.Id)
			assert.Empty(msg.Room.RoomId, "should not have joined the room: %+v", msg)
		default:
			if event, ok := checkMessageRoomlistDisinvite(t, msg); ok {
				assert.False(disinvited, "received duplicate disinvite: %+v", msg)
				disinvited = true
				assert.Equal(roomId, event.RoomId)
				assert.Equal(api.DisinviteReasonDeleted, event.Reason)
			}
		}
	}
	assert.True(disinvited, "should have been disinvited")
	assert.False(room.HasSession(session))
}

func TestRoom_JoinResponseBeforeEvents(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	roomId := "test-room"
	MustSucceed2(t, client.JoinRoom, ctx, roomId)
	client.RunUntilJoined(ctx, hello.Hello)

	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)
	room := hub.getRoom(roomId)
	require.NotNil(room)

	// Messages are held back while the session is joining the room, so events
	// received through different channels are not sent before the response.
	session.HoldMessages(roomId)
	event := func(sessionId api.PublicSessionId) *api.ServerMessage {
		return &api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "participants",
				Type:   "update",
				Update: &api.RoomEventServerMessage{
					RoomId: roomId,
					Users: api.UserDataList{
						{"sessionId": sessionId, "inCall": FlagInCall},
					},
				},
			},
		}
	}
	assert.NoError(hub.events.PublishRoomMessage(roomId, session.Backend(), &events.AsyncMessage{
		Type:    "message",
		Message: event("session-1"),
	}))
	assert.NoError(room.publishLocal(event("session-2")))
	assertNoMoreMessages(t, client)

	session.ReleaseMessages(true, hub.newRoomResponse(&api.ClientMessage{Id: "ABCD"}, room))
	if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageRoomId(t, msg, roomId) {
		assert.Equal("ABCD", msg.Id)
	}
	// The events are received in the order they were held back.
	var received []any
	for range 2 {
		if msg, ok := client.RunUntilMessage(ctx); ok {
			if update, ok := checkMessageParticipantsInCall(t, msg); ok && assert.Len(update.Users, 1) {
				received = append(received, update.Users[0]["sessionId"])
			}
		}
	}
	assert.ElementsMatch([]any{"session-1", "session-2"}, received)
	assertNoMoreMessages(t, client)
}

func TestRoom_JoinFailedDropsRoomEvents(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	roomId := "test-room"
	MustSucceed2(t, client.JoinRoom, ctx, roomId)
	client.RunUntilJoined(ctx, hello.Hello)

	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	session.HoldMessages(roomId)
	// An event of the room that is being joined.
	assert.NoError(hub.events.PublishRoomMessage(roomId, session.Backend(), &events.AsyncMessage{
		Type: "message",
		Message: &api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "participants",
				Type:   "update",
				Update: &api.RoomEventServerMessage{
					RoomId: roomId,
					Users: api.UserDataList{
						{"sessionId": "the-other-session", "inCall": FlagInCall},
					},
				},
			},
		},
	}))
	// A message that is not related to the room.
	data := json.RawMessage("{\"foo\":\"bar\"}")
	assert.NoError(hub.events.PublishSessionMessage(hello.Hello.SessionId, session.Backend(), &events.AsyncMessage{
		Type: "message",
		Message: &api.ServerMessage{
			Type: "message",
			Message: &api.MessageServerMessage{
				Sender: &api.MessageServerMessageSender{
					Type:      "session",
					SessionId: "the-other-session",
				},
				Data: data,
			},
		},
	}))
	// The messages are held back until the join has finished.
	assert.Eventually(func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.heldMessages) == 2
	}, testTimeout, time.Millisecond)
	assertNoMoreMessages(t, client)

	// Joining the room failed, so held events of the room are not sent.
	session.ReleaseMessages(false, hub.newRoomResponse(&api.ClientMessage{Id: "ABCD"}, nil))
	if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageRoomId(t, msg, "") {
		assert.Equal("ABCD", msg.Id)
	}
	if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageType(t, msg, "message") {
		assert.Equal(string(data), string(msg.Message.Data))
	}
	assertNoMoreMessages(t, client)
}

func TestRoom_JoiningInternalSessionNotVisible(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloInternal())
	hello := MustSucceed1(t, client.RunUntilHello, ctx)
	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	room, err := hub.CreateRoom("test-room", nil, session.Backend())
	require.NoError(err)
	defer room.Close()
	waitForRoomSynced(ctx, t, room)

	isVisible := func(room *Room) (bool, bool) {
		t.Helper()
		var inUsers bool
		for _, user := range room.getParticipantsUpdateMessage(true).Event.Update.Users {
			if sid, _ := user.SessionId(); sid == hello.Hello.SessionId {
				inUsers = true
			}
		}
		internal, _, _ := room.GetInternalSessions()
		var inInternal bool
		for _, s := range internal {
			if s.SessionId == string(hello.Hello.SessionId) {
				inInternal = true
			}
		}
		return inUsers, inInternal
	}

	// The internal session is not visible to others while it is being added,
	// so it doesn't need to be removed if it leaves before it was added.
	join, err := room.startAddSession(session, nil, false)
	require.NoError(err)
	inUsers, inInternal := isVisible(room)
	assert.False(inUsers, "joining session should not be in the list of users")
	assert.False(inInternal, "joining session should not be in the internal sessions")
	room.RemoveSession(session)
	room.finishAddSession(join)

	// The internal session is visible once it has been added.
	room2, err := hub.CreateRoom("test-room-2", nil, session.Backend())
	require.NoError(err)
	defer room2.Close()
	waitForRoomSynced(ctx, t, room2)
	require.NoError(room2.AddSession(session, nil))
	inUsers, inInternal = isVisible(room2)
	assert.True(inUsers, "session should be in the list of users")
	assert.True(inInternal, "session should be in the internal sessions")
}

// initialStateHub provides the users of a room to other servers.
type initialStateHub struct {
	grpctest.MockHub

	backend *talk.Backend
	users   api.UserDataList
}

func (h *initialStateHub) GetBackend(u *url.URL) *talk.Backend {
	return h.backend
}

func (h *initialStateHub) GetRoomUsers(ctx context.Context, roomId string, backend *talk.Backend) (api.UserDataList, bool) {
	return h.users, true
}

// newInitialStateServer creates a GRPC server providing the given users that
// accepts connections but doesn't serve requests until it is started.
func newInitialStateServer(t *testing.T, backend *talk.Backend, users api.UserDataList) (*grpc.Server, string) {
	t.Helper()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	var server *grpc.Server
	var addr string
	for port := 50100; port < 50200; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config := goconf.NewConfigFile()
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = grpc.NewServer(ctx, config, "0.0.0")
		if test.IsErrorAddressAlreadyInUse(err) {
			continue
		}

		require.NoError(t, err)
		break
	}
	require.NotNil(t, server, "could not find free port")
	server.SetServerId("dont-match")
	server.SetHub(&initialStateHub{
		backend: backend,
		users:   users,
	})
	t.Cleanup(func() {
		server.Close()
	})
	return server, addr
}

func TestRoom_RetryInitialState(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, _ := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	backend := talk.NewCompatBackend(nil)
	room, err := hub.CreateRoom("test-room", nil, backend)
	require.NoError(err)
	defer room.Close()
	waitForRoomSynced(ctx, t, room)

	server, addr := newInitialStateServer(t, backend, api.UserDataList{
		{
			"sessionId": "session1",
			"inCall":    FlagInCall,
		},
		{
			"sessionId": "session2",
			"inCall":    FlagInCall,
		},
	})
	client, err := grpc.NewClient(logger, addr, nil, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(err)
	defer client.Close() // nolint

	// The hub has no other servers, simulate that the initial state is fetched.
	room.mu.Lock()
	room.syncState = newRoomSyncState()
	room.mu.Unlock()

	// The server doesn't serve requests yet, so fetching the state fails.
	fetchCtx, fetchCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer fetchCancel()
	failed := room.fetchAndApplyInitialState(fetchCtx, []*grpc.Client{client}, false)
	assert.Equal([]*grpc.Client{client}, failed)
	assert.Empty(room.getUsers())

	// Changes from events are still tracked while the state is retried.
	room.removeUsers("session2")

	go func() {
		assert.NoError(server.Run())
	}()

	room.initialStateRetryDelay = time.Millisecond
	room.retryInitialState(failed)

	if users := room.getUsers(); assert.Len(users, 1) {
		sid, _ := users[0].SessionId()
		assert.EqualValues("session1", sid)
	}
	room.mu.RLock()
	assert.Nil(room.syncState, "changes should no longer be tracked")
	room.mu.RUnlock()
}

func TestRoom_RetryInitialStateGiveUp(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, _ := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	backend := talk.NewCompatBackend(nil)
	room, err := hub.CreateRoom("test-room", nil, backend)
	require.NoError(err)
	defer room.Close()
	waitForRoomSynced(ctx, t, room)

	// Get an address where no server is listening.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(err)
	addr := listener.Addr().String()
	require.NoError(listener.Close())

	client, err := grpc.NewClient(logger, addr, nil, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(err)
	defer client.Close() // nolint

	room.mu.Lock()
	room.syncState = newRoomSyncState()
	room.mu.Unlock()

	room.initialStateRetryDelay = time.Millisecond
	room.retryInitialState([]*grpc.Client{client})

	// Changes are no longer tracked after giving up.
	room.mu.RLock()
	assert.Nil(room.syncState)
	room.mu.RUnlock()
}
