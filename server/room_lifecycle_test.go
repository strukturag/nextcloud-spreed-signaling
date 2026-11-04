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
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	eventstest "github.com/strukturag/nextcloud-spreed-signaling/v2/async/events/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
)

func waitForRoomEvent(ctx context.Context, t *testing.T, listener *eventstest.Listener) (*api.EventServerMessage, bool) {
	t.Helper()
	message, ok := listener.WaitForMessage(ctx)
	if !ok ||
		!assert.Equal(t, "message", message.Type, "invalid message %+v", message) ||
		!assert.NotNil(t, message.Message, "invalid message %+v", message) ||
		!checkMessageType(t, message.Message, "event") {
		return nil, false
	}
	return message.Message.Event, true
}

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
	require.NoError(room.addReservedSession(session2, nil))
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

	assert.ErrorIs(room.addReservedSession(session, nil), ErrRoomClosed)
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
	users := room2.fetchInitialUsers(ctx)
	assert.Len(users, 1)
	room2.applyInitialState(nil, users)
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

			room.finishAddSession(join)
			if event, ok := waitForRoomEvent(ctx, t, listener); ok && assert.Equal("join", event.Type, "expected join event, got %+v", event) {
				if assert.Len(event.Join, 1) {
					assert.Equal(hello2.Hello.SessionId, event.Join[0].SessionId)
				}
			}
			if event, ok := waitForRoomEvent(ctx, t, listener); ok && assert.Equal("leave", event.Type, "expected leave event, got %+v", event) {
				assert.Equal([]api.PublicSessionId{hello2.Hello.SessionId}, event.Leave)
			}
			listener.AssertNoMessage(200 * time.Millisecond)

			client1.RunUntilJoined(ctx, hello2.Hello)
			client1.RunUntilLeft(ctx, hello2.Hello)
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
