/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

func Test_TransientMessages(t *testing.T) {
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

			require.NoError(client1.SetTransientData("foo", "bar", 0))
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_in_room")
			}

			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// Give message processing some time.
			time.Sleep(10 * time.Millisecond)

			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			session1 := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
			session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
			require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

			// Client 1 may modify transient data.
			session1.SetPermissions([]api.Permission{api.PERMISSION_TRANSIENT_DATA})
			// Client 2 may not modify transient data.
			session2.SetPermissions([]api.Permission{})

			require.NoError(client2.SetTransientData("foo", "bar", 0))
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_allowed")
			}

			require.NoError(client1.SetTransientData("foo", "bar", 0))

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "foo", "bar", nil)
			}
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "foo", "bar", nil)
			}

			require.NoError(client2.RemoveTransientData("foo"))
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_allowed")
			}

			// Setting the same value is ignored by the server.
			require.NoError(client1.SetTransientData("foo", "bar", 0))
			ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel2()

			client1.RunUntilErrorIs(ctx2, context.DeadlineExceeded)

			data := map[string]any{
				"hello": "world",
			}
			require.NoError(client1.SetTransientData("foo", data, 0))

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "foo", data, "bar")
			}
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "foo", data, "bar")
			}

			require.NoError(client1.RemoveTransientData("foo"))

			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientRemove(t, msg, "foo", data)
			}
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientRemove(t, msg, "foo", data)
			}

			// Removing a non-existing key is ignored by the server.
			require.NoError(client1.RemoveTransientData("foo"))
			ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel3()

			client1.RunUntilErrorIs(ctx3, context.DeadlineExceeded)

			ttl := 200 * time.Millisecond
			require.NoError(client1.SetTransientData("abc", data, ttl))
			setAt := time.Now()
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, "abc", data, nil)
			}

			client1.CloseWithBye()
			require.NoError(client1.WaitForClientRemoved(ctx))
			client2.RunUntilLeft(ctx, hello1.Hello)

			client3, hello3 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"3")
			roomMsg = MustSucceed2(t, client3.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			client2.RunUntilJoined(ctx, hello3.Hello)

			_, ignored, ok := client3.RunUntilJoinedAndReturn(ctx, hello2.Hello, hello3.Hello)
			require.True(ok)

			var msg *api.ServerMessage
			if len(ignored) == 0 {
				msg = MustSucceed1(t, client3.RunUntilMessage, ctx)
			} else if len(ignored) == 1 {
				msg = ignored[0]
			} else {
				require.LessOrEqual(len(ignored), 1, "Received too many messages: %+v", ignored)
			}

			checkMessageTransientInitial(t, msg, api.StringMap{
				"abc": data,
			})

			client4, hello4 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"4")
			roomMsg = MustSucceed2(t, client4.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			client2.RunUntilJoined(ctx, hello4.Hello)
			client3.RunUntilJoined(ctx, hello4.Hello)

			_, ignored, ok = client4.RunUntilJoinedAndReturn(ctx, hello2.Hello, hello3.Hello, hello4.Hello)
			require.True(ok)

			if len(ignored) == 0 {
				msg = MustSucceed1(t, client4.RunUntilMessage, ctx)
			} else if len(ignored) == 1 {
				msg = ignored[0]
			} else {
				require.LessOrEqual(len(ignored), 1, "Received too many messages: %+v", ignored)
			}

			checkMessageTransientInitial(t, msg, api.StringMap{
				"abc": data,
			})

			delta := time.Until(setAt.Add(ttl))
			if assert.Greater(delta, time.Duration(0), "test runner too slow?") {
				time.Sleep(delta)
				if msg, ok = client2.RunUntilMessage(ctx); ok {
					checkMessageTransientRemove(t, msg, "abc", data)
				}
			}
		})
	}
}

func Test_TransientSessionData(t *testing.T) {
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

			roomId := "test-room"
			roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)
			roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

			sessionKey1 := "sd:" + string(hello1.Hello.SessionId)
			sessionKey2 := "sd:" + string(hello2.Hello.SessionId)
			require.NotEqual(sessionKey1, sessionKey2)

			require.NoError(client1.SetTransientData(sessionKey2, "foo", 0))
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_allowed")
			}
			require.NoError(client2.SetTransientData(sessionKey1, "bar", 0))
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_allowed")
			}

			require.NoError(client1.SetTransientData(sessionKey1, "foo", 0))
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey1, "foo", nil)
			}
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey1, "foo", nil)
			}

			require.NoError(client2.RemoveTransientData(sessionKey1))
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageError(t, msg, "not_allowed")
			}

			require.NoError(client2.SetTransientData(sessionKey2, "bar", 0))
			if msg, ok := client1.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey2, "bar", nil)
			}
			if msg, ok := client2.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey2, "bar", nil)
			}

			client1.CloseWithBye()
			assert.NoError(client1.WaitForClientRemoved(ctx))

			var messages []*api.ServerMessage
			for range 2 {
				if msg, ok := client2.RunUntilMessage(ctx); ok {
					messages = append(messages, msg)
				}
			}
			if assert.Len(messages, 2) {
				if messages[0].Type == "transient" {
					messages[0], messages[1] = messages[1], messages[0]
				}
				client2.checkMessageRoomLeaveSession(messages[0], hello1.Hello.SessionId)
				checkMessageTransientRemove(t, messages[1], sessionKey1, "foo")
			}

			client3, hello3 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"3")
			roomMsg = MustSucceed2(t, client3.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			_, ignored, ok := client3.RunUntilJoinedAndReturn(ctx, hello2.Hello, hello3.Hello)
			require.True(ok)

			var msg *api.ServerMessage
			if len(ignored) == 0 {
				msg = MustSucceed1(t, client3.RunUntilMessage, ctx)
			} else if len(ignored) == 1 {
				msg = ignored[0]
			} else {
				require.LessOrEqual(len(ignored), 1, "Received too many messages: %+v", ignored)
			}

			checkMessageTransientInitial(t, msg, api.StringMap{
				sessionKey2: "bar",
			})

			client2.CloseWithBye()
			assert.NoError(client2.WaitForClientRemoved(ctx))

			messages = nil
			for range 2 {
				if msg, ok := client3.RunUntilMessage(ctx); ok {
					messages = append(messages, msg)
				}
			}
			if assert.Len(messages, 2) {
				if messages[0].Type == "transient" {
					messages[0], messages[1] = messages[1], messages[0]
				}
				client3.checkMessageRoomLeaveSession(messages[0], hello2.Hello.SessionId)
				checkMessageTransientRemove(t, messages[1], sessionKey2, "bar")
			}

			// Internal clients may set transient data of any session.
			client4 := NewTestClient(t, server1, hub1)
			defer client4.CloseWithBye()

			require.NoError(client4.SendHelloInternal())
			hello4 := MustSucceed1(t, client4.RunUntilHello, ctx)
			roomMsg = MustSucceed2(t, client4.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			_, ignored, ok = client4.RunUntilJoinedAndReturn(ctx, hello3.Hello, hello4.Hello)
			require.True(ok)

			if len(ignored) == 0 {
				msg = MustSucceed1(t, client4.RunUntilMessage, ctx)
			} else if len(ignored) == 1 {
				msg = ignored[0]
			} else {
				require.LessOrEqual(len(ignored), 1, "Received too many messages: %+v", ignored)
			}

			if msgInCall, ok := checkMessageParticipantsInCall(t, msg); ok {
				if assert.Len(msgInCall.Users, 1) {
					assert.Equal(true, msgInCall.Users[0]["internal"])
					assert.EqualValues(hello4.Hello.SessionId, msgInCall.Users[0]["sessionId"])
					assert.EqualValues(FlagInCall|FlagWithAudio, msgInCall.Users[0]["inCall"])
				}
			}

			client3.RunUntilJoined(ctx, hello4.Hello)
			if msg, ok := client3.RunUntilMessage(ctx); ok {
				if msgInCall, ok := checkMessageParticipantsInCall(t, msg); ok {
					if assert.Len(msgInCall.Users, 1) {
						assert.Equal(true, msgInCall.Users[0]["internal"])
						assert.EqualValues(hello4.Hello.SessionId, msgInCall.Users[0]["sessionId"])
						assert.EqualValues(FlagInCall|FlagWithAudio, msgInCall.Users[0]["inCall"])
					}
				}
			}

			sessionKey3 := string("sd:" + hello3.Hello.SessionId)
			require.NoError(client4.SetTransientData(sessionKey3, "baz", 0))
			if msg, ok := client4.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey3, "baz", nil)
			}

			if msg, ok := client3.RunUntilMessage(ctx); ok {
				checkMessageTransientSet(t, msg, sessionKey3, "baz", nil)
			}
		})
	}
}
