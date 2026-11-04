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
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/sfu"
	sfutest "github.com/strukturag/nextcloud-spreed-signaling/v2/sfu/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/talk"
)

func createHubsForClusteredTest(t *testing.T) (*Hub, *Hub, *httptest.Server, *httptest.Server) {
	t.Helper()
	if isLocalTest(t) {
		hub, _, _, server := CreateHubForTest(t)
		return hub, hub, server, server
	}

	return CreateClusteredHubsForTest(t)
}

func publishBackendRoomRequest(t *testing.T, hub *Hub, roomId string, backend *talk.Backend, request *talk.BackendServerRoomRequest) {
	t.Helper()
	require.NoError(t, hub.events.PublishBackendRoomMessage(roomId, backend, &events.AsyncMessage{
		Type: "room",
		Room: request,
	}))
}

// assertRoomUsers checks that the room on all hubs has the expected users.
func assertRoomUsers(t *testing.T, roomId string, hubs []*Hub, expected ...api.PublicSessionId) {
	t.Helper()
	for _, hub := range hubs {
		room := hub.getRoom(roomId)
		if assert.NotNil(t, room) {
			users := room.getUsers().Map()
			assert.Len(t, users, len(expected), "users: %+v", users)
			for _, sid := range expected {
				assert.Contains(t, users, sid)
			}
		}
	}
}

func TestRoom_ProcessOlderChangeRequests(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			assert := assert.New(t)
			hub1, hub2, server1, server2 := createHubsForClusteredTest(t)

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)
			backend := session1.Backend()

			// Requests that only contain changes must be processed even if they
			// were received earlier than the previous request (e.g. by different
			// servers or because of slower processing).
			for idx, sid := range []api.PublicSessionId{hello1.Hello.SessionId, hello2.Hello.SessionId} {
				publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
					Type:         "incall",
					ReceivedTime: int64(2000 - idx*1000),
					InCall: &talk.BackendRoomInCallRequest{
						InCall: json.RawMessage(strconv.Itoa(FlagInCall)),
						Changed: api.UserDataList{
							{
								"sessionId": sid,
								"inCall":    FlagInCall,
							},
						},
					},
				})
			}
			for _, client := range []*TestClient{client1, client2} {
				for range 2 {
					if msg, ok := client.RunUntilMessage(ctx); ok {
						checkMessageParticipantsInCall(t, msg)
					}
				}
			}
			for _, hub := range []*Hub{hub1, hub2} {
				room := hub.getRoom(roomId)
				require.NotNil(room)
				users := room.getUsers().Map()
				assert.Len(users, 2)
				assert.Contains(users, hello1.Hello.SessionId)
				assert.Contains(users, hello2.Hello.SessionId)
			}

			for idx, data := range []string{"{\"foo\":1}", "{\"foo\":2}"} {
				publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
					Type:         "message",
					ReceivedTime: int64(4000 - idx*1000),
					Message: &talk.BackendRoomMessageRequest{
						Data: json.RawMessage(data),
					},
				})
			}
			for _, client := range []*TestClient{client1, client2} {
				for _, data := range []string{"{\"foo\":1}", "{\"foo\":2}"} {
					if msg, ok := client.RunUntilRoomMessage(ctx); ok {
						assert.Equal(roomId, msg.RoomId)
						assert.JSONEq(data, string(msg.Data))
					}
				}
			}

			// Updates contain the complete properties, so older updates are ignored.
			for idx, properties := range []string{"{\"name\":\"new\"}", "{\"name\":\"old\"}"} {
				publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
					Type:         "update",
					ReceivedTime: int64(6000 - idx*1000),
					Update: &talk.BackendRoomUpdateRequest{
						Properties: json.RawMessage(properties),
					},
				})
			}
			for _, client := range []*TestClient{client1, client2} {
				if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageType(t, msg, "room") {
					assert.Equal(roomId, msg.Room.RoomId)
					assert.JSONEq("{\"name\":\"new\"}", string(msg.Room.Properties))
				}
			}
			assertNoMoreMessages(t, client1, client2)
			for _, hub := range []*Hub{hub1, hub2} {
				room := hub.getRoom(roomId)
				require.NotNil(room)
				assert.JSONEq("{\"name\":\"new\"}", string(room.Properties()))
			}
		})
	}
}

func TestRoom_NoDroppedEvents(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			assert := assert.New(t)
			hub1, hub2, server1, server2 := createHubsForClusteredTest(t)

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)

			// Send a burst of events.
			count := 1000
			for idx := range count {
				publishBackendRoomRequest(t, hub1, roomId, session1.Backend(), &talk.BackendServerRoomRequest{
					Type:         "message",
					ReceivedTime: int64(count - idx),
					Message: &talk.BackendRoomMessageRequest{
						Data: json.RawMessage(strconv.Itoa(idx)),
					},
				})
			}

			for _, client := range []*TestClient{client1, client2} {
				for idx := range count {
					msg, ok := client.RunUntilRoomMessage(ctx)
					if !ok {
						break
					}

					assert.Equal(strconv.Itoa(idx), string(msg.Data))
				}
			}
		})
	}
}

func TestRoom_ClosedRoomIgnoresEvents(t *testing.T) {
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
	room1, err := hub.CreateRoom(roomId, nil, session.Backend())
	require.NoError(err)
	room1.Close()

	MustSucceed2(t, client.JoinRoom, ctx, roomId)
	client.RunUntilJoined(ctx, hello.Hello)
	room2 := hub.getRoom(roomId)
	require.NotNil(room2)
	require.NotSame(room1, room2)

	// A message that was still pending for the closed room may not be
	// processed by the new room.
	room1.processAsyncMessage(&events.AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "incall",
			InCall: &talk.BackendRoomInCallRequest{
				InCall: json.RawMessage(strconv.Itoa(FlagInCall)),
				Changed: api.UserDataList{
					{
						"sessionId": hello.Hello.SessionId,
						"inCall":    FlagInCall,
					},
				},
			},
		},
	})
	assertNoMoreMessages(t, client)
	assert.Empty(room1.getUsers())
	assert.Empty(room2.getUsers())
	assert.False(room2.IsSessionInCall(session))
}

func TestRoom_OlderUsersListsAreIgnored(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			hub1, hub2, server1, server2 := createHubsForClusteredTest(t)

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)
			backend := session1.Backend()

			sid1 := hello1.Hello.SessionId
			sid2 := hello2.Hello.SessionId
			user := func(sid api.PublicSessionId, inCall int) api.UserData {
				return api.UserData{
					"sessionId": sid,
					"inCall":    inCall,
				}
			}
			assertUsers := func(expected ...api.PublicSessionId) {
				t.Helper()
				assertRoomUsers(t, roomId, []*Hub{hub1, hub2}, expected...)
			}
			assertParticipantsUpdate := func() {
				t.Helper()
				for _, client := range []*TestClient{client1, client2} {
					if msg, ok := client.RunUntilMessage(ctx); ok {
						checkMessageParticipantsInCall(t, msg)
					}
				}
			}
			inCall := json.RawMessage(strconv.Itoa(FlagInCall))

			// Older versions of Talk send the complete list of users.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "incall",
				ReceivedTime: 2000,
				InCall: &talk.BackendRoomInCallRequest{
					InCall:  inCall,
					Changed: api.UserDataList{user(sid1, FlagInCall)},
					Users:   api.UserDataList{user(sid1, FlagInCall)}, // nolint
				},
			})
			assertParticipantsUpdate()
			assertUsers(sid1)

			// Requests with a complete list of users that were received earlier
			// are ignored, independent of the request type.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "incall",
				ReceivedTime: 1000,
				InCall: &talk.BackendRoomInCallRequest{
					InCall:  inCall,
					Changed: api.UserDataList{user(sid2, FlagInCall)},
					Users:   api.UserDataList{user(sid1, FlagInCall), user(sid2, FlagInCall)}, // nolint
				},
			})
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "participants",
				ReceivedTime: 1500,
				Participants: &talk.BackendRoomParticipantsRequest{
					Changed: api.UserDataList{user(sid2, FlagInCall)},
					Users:   api.UserDataList{user(sid1, FlagInCall), user(sid2, FlagInCall)}, // nolint
				},
			})
			assertNoMoreMessages(t, client1, client2)
			assertUsers(sid1)

			// Requests with only changes are always processed.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "incall",
				ReceivedTime: 500,
				InCall: &talk.BackendRoomInCallRequest{
					InCall:  inCall,
					Changed: api.UserDataList{user(sid2, FlagInCall)},
				},
			})
			assertParticipantsUpdate()
			assertUsers(sid1, sid2)

			// The complete list replaces the existing users, also if it is empty.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "participants",
				ReceivedTime: 3000,
				Participants: &talk.BackendRoomParticipantsRequest{
					Changed: api.UserDataList{user(sid2, FlagInCall)},
					Users:   api.UserDataList{user(sid2, FlagInCall)}, // nolint
				},
			})
			assertParticipantsUpdate()
			assertUsers(sid2)

			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type:         "incall",
				ReceivedTime: 4000,
				InCall: &talk.BackendRoomInCallRequest{
					InCall:  json.RawMessage(strconv.Itoa(FlagDisconnected)),
					Changed: api.UserDataList{user(sid2, FlagDisconnected)},
					Users:   api.UserDataList{}, // nolint
				},
			})
			assertParticipantsUpdate()
			assertUsers()
			assertNoMoreMessages(t, client1, client2)
		})
	}
}

func TestRoom_ParticipantsUpdateRemovesUsers(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			hub1, hub2, server1, server2 := createHubsForClusteredTest(t)

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)
			backend := session1.Backend()

			sid1 := hello1.Hello.SessionId
			sid2 := hello2.Hello.SessionId
			hubs := []*Hub{hub1, hub2}
			publishParticipants := func(changed ...api.UserData) {
				t.Helper()
				publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
					Type: "participants",
					Participants: &talk.BackendRoomParticipantsRequest{
						Changed: changed,
					},
				})
				for _, client := range []*TestClient{client1, client2} {
					if msg, ok := client.RunUntilMessage(ctx); ok {
						checkMessageParticipantsInCall(t, msg)
					}
				}
			}

			publishParticipants(
				api.UserData{"sessionId": sid1, "inCall": FlagInCall | FlagWithAudio},
				api.UserData{"sessionId": sid2, "inCall": FlagInCall},
			)
			assertRoomUsers(t, roomId, hubs, sid1, sid2)

			// Users that are no longer in the call are removed.
			publishParticipants(
				api.UserData{"sessionId": sid1, "inCall": FlagDisconnected},
			)
			assertRoomUsers(t, roomId, hubs, sid2)

			// Changes without in-call information only update the other properties
			// of users that are stored.
			publishParticipants(
				api.UserData{"sessionId": sid1, "participantType": 3},
				api.UserData{"sessionId": sid2, "participantType": 3},
			)
			assertRoomUsers(t, roomId, hubs, sid2)
			for _, hub := range hubs {
				room := hub.getRoom(roomId)
				require.NotNil(room)
				if user := room.getUsers().Map()[sid2]; assert.NotNil(t, user) {
					assert.EqualValues(t, 3, user["participantType"], "user: %+v", user)
					assert.True(t, isUserInCall(user), "user: %+v", user)
				}
			}

			publishParticipants(
				api.UserData{"sessionId": sid2, "inCall": FlagDisconnected},
			)
			assertRoomUsers(t, roomId, hubs)
			assertNoMoreMessages(t, client1, client2)
		})
	}
}

func TestRoom_InCallChangesAfterRoomSwitch(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	mcu := sfutest.NewSFU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()
	hub.SetMcu(mcu)

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	defer client1.CloseWithBye()
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	defer client2.CloseWithBye()

	roomId1 := "test-room-1"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId1)
	client1.RunUntilJoined(ctx, hello1.Hello)
	MustSucceed2(t, client2.JoinRoom, ctx, roomId1)
	client1.RunUntilJoined(ctx, hello2.Hello)
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	room1 := hub.getRoom(roomId1)
	require.NotNil(room1)
	session1, ok := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.True(ok)
	sid1 := hello1.Hello.SessionId

	// Session 1 switches to a different room (e.g. a breakout room) and joins
	// the call there.
	roomId2 := "test-room-2"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId2)
	client1.RunUntilJoined(ctx, hello1.Hello)
	client2.RunUntilLeft(ctx, hello1.Hello)
	room2 := hub.getRoom(roomId2)
	require.NotNil(room2)
	require.Same(room2, session1.GetRoom())

	publishInCall := func(room *Room, inCall int) {
		t.Helper()
		publishBackendRoomRequest(t, hub, room.Id(), room.Backend(), &talk.BackendServerRoomRequest{
			Type: "incall",
			InCall: &talk.BackendRoomInCallRequest{
				InCall: json.RawMessage(strconv.Itoa(inCall)),
				Changed: api.UserDataList{
					{
						"sessionId": sid1,
						"inCall":    inCall,
					},
				},
			},
		})
		client := client2
		if room == room2 {
			client = client1
		}
		if msg, ok := client.RunUntilMessage(ctx); ok {
			checkMessageParticipantsInCall(t, msg)
		}
	}

	publishInCall(room2, FlagInCall|FlagWithAudio|FlagWithVideo)
	assert.True(room2.IsSessionInCall(session1))

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: sid1,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)
	require.NotNil(session1.GetPublisher(sfu.StreamTypeVideo))

	// Delayed in-call changes for the previous room may not affect the session.
	publishInCall(room1, FlagInCall)
	assert.False(room1.IsSessionInCall(session1))
	publishInCall(room1, FlagDisconnected)
	assert.True(room2.IsSessionInCall(session1))
	assert.NotNil(session1.GetPublisher(sfu.StreamTypeVideo), "publisher should not have been closed")

	// Leaving the call in the previous room doesn't release the media while
	// the session is still in the call.
	session1.LeaveCall(room1)
	session1.LeaveCall(room2)
	assert.NotNil(session1.GetPublisher(sfu.StreamTypeVideo), "publisher should not have been closed")

	// Leaving the call in the current room releases the media.
	publishInCall(room2, FlagDisconnected)
	assert.False(room2.IsSessionInCall(session1))
	assert.Nil(session1.GetPublisher(sfu.StreamTypeVideo), "publisher should have been closed")

	assertNoMoreMessages(t, client1, client2)
}

func TestRoom_NotifyInternalRoomDeletedLockOrder(t *testing.T) {
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
	require.NoError(room.AddSession(session, nil))

	// Simulate a session that is leaving the room: it holds its own lock and
	// then needs the room lock.
	session.mu.Lock()
	notified := make(chan struct{})
	go func() {
		defer close(notified)
		room.notifyInternalRoomDeleted()
	}()

	// Give the notification a chance to block on the session lock.
	time.Sleep(100 * time.Millisecond)

	locked := make(chan struct{})
	go func() {
		defer close(locked)
		room.mu.Lock()
		room.mu.Unlock() // nolint
	}()

	select {
	case <-locked:
	case <-ctx.Done():
		assert.Fail("could not acquire room lock while session is notified, deadlock")
	}
	session.mu.Unlock()

	select {
	case <-notified:
	case <-ctx.Done():
		assert.Fail("session was not notified")
	}

	if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageType(t, msg, "event") {
		assert.Equal("room", msg.Event.Target)
		assert.Equal("delete", msg.Event.Type)
	}
}

func TestRoom_OutdatedMembershipEvents(t *testing.T) {
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

	MustSucceed2(t, client.JoinRoom, ctx, "test-room")
	client.RunUntilJoined(ctx, hello.Hello)

	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	// Events about a session can be received out of order through different
	// channels, the send times are set by the server of that session.
	other := api.PublicSessionId("the-other-session")
	start := time.Now()
	at := func(ms int) time.Time {
		return start.Add(time.Duration(ms) * time.Millisecond)
	}
	process := func(sendTime time.Time, event *api.EventServerMessage) {
		t.Helper()
		session.processAsyncMessage(&events.AsyncMessage{
			Type:     "message",
			SendTime: sendTime,
			Message: &api.ServerMessage{
				Type:  "event",
				Event: event,
			},
		})
	}
	join := func(sendTime time.Time) {
		t.Helper()
		process(sendTime, &api.EventServerMessage{
			Target: "room",
			Type:   "join",
			Join: []api.EventServerMessageSessionEntry{
				{SessionId: other},
			},
		})
	}
	leave := func(sendTime time.Time) {
		t.Helper()
		process(sendTime, &api.EventServerMessage{
			Target: "room",
			Type:   "leave",
			Leave:  []api.PublicSessionId{other},
		})
	}
	expectJoin := func() {
		t.Helper()
		if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageType(t, msg, "event") &&
			assert.Equal("join", msg.Event.Type, "invalid event %+v", msg.Event) &&
			assert.Len(msg.Event.Join, 1) {
			assert.Equal(other, msg.Event.Join[0].SessionId)
		}
	}
	expectLeave := func() {
		t.Helper()
		if msg, ok := client.RunUntilMessage(ctx); ok && checkMessageType(t, msg, "event") &&
			assert.Equal("leave", msg.Event.Type, "invalid event %+v", msg.Event) {
			assert.Equal([]api.PublicSessionId{other}, msg.Event.Leave)
		}
	}

	// The "leave" is received before the outdated "join".
	leave(at(20))
	join(at(10))
	assertNoMoreMessages(t, client)

	// The session joins again.
	join(at(30))
	expectJoin()

	// The session left and joined again, but the "leave" is received after
	// the "join".
	join(at(50))
	leave(at(40))
	assertNoMoreMessages(t, client)

	leave(at(60))
	expectLeave()
	join(at(55))
	assertNoMoreMessages(t, client)

	// Messages sent directly (e.g. from federated servers) have no send time and
	// are not checked.
	session.SendMessage(&api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "room",
			Type:   "join",
			Join: []api.EventServerMessageSessionEntry{
				{SessionId: other},
			},
		},
	})
	expectJoin()
	assertNoMoreMessages(t, client)
}

func TestRoom_RemoveSessionWhileNotifyingJoined(t *testing.T) {
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

	room := hub.getRoom(roomId)
	require.NotNil(room)
	session, ok := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
	require.True(ok)

	// While the sessions are published in reply to a "sessionjoined" event,
	// they may not be removed, otherwise the "leave" event could be published
	// before the "join" event.
	room.membershipMu.Lock()
	removed := make(chan struct{})
	go func() {
		defer close(removed)
		room.RemoveSession(session)
	}()

	time.Sleep(100 * time.Millisecond)
	assert.True(room.HasSession(session))
	room.membershipMu.Unlock()

	select {
	case <-removed:
	case <-ctx.Done():
		assert.Fail("session was not removed")
	}
	assert.False(room.HasSession(session))
}

func TestRoom_StoredUsersAreCopies(t *testing.T) {
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
	room, err := hub.CreateRoom("test-room", nil, session.Backend())
	require.NoError(err)
	defer room.Close()

	// Read the users concurrently, e.g. as done for requests from other servers.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				room.getUsers()
			}
		}
	})

	sid := api.PublicSessionId("the-session")
	var changed api.UserDataList
	for range 100 {
		changed = api.UserDataList{
			{
				"sessionId":   sid,
				"inCall":      FlagInCall,
				"permissions": []any{"publish-audio"},
			},
		}
		room.PublishUsersInCallChanged(changed)
	}
	close(stop)
	wg.Wait()

	if users := room.getUsers(); assert.Len(users, 1) {
		assert.NotContains(users[0], "permissions")
	}

	// Modifying the data passed to the room doesn't modify the stored data.
	changed[0]["foo"] = "bar"
	if users := room.getUsers(); assert.Len(users, 1) {
		assert.NotContains(users[0], "foo")
	}

	// Modifying the stored data doesn't modify the data passed to the room
	// (which could be referenced by messages being sent).
	room.PublishUsersInCallChangedAll(FlagInCall | FlagWithAudio)
	assert.Equal(FlagInCall, changed[0]["inCall"])
	if users := room.getUsers(); assert.Len(users, 1) {
		assert.Equal(FlagInCall|FlagWithAudio, users[0]["inCall"])
	}
}

func TestRoom_InCallAllLeaveClearsUsers(t *testing.T) {
	t.Parallel()
	for _, subtest := range clusteredTests {
		t.Run(subtest, func(t *testing.T) {
			t.Parallel()
			logger := logtest.NewLoggerForTest(t)
			ctx := log.NewLoggerContext(t.Context(), logger)
			require := require.New(t)
			hub1, hub2, server1, server2 := createHubsForClusteredTest(t)

			ctx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
			defer client1.CloseWithBye()
			client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
			defer client2.CloseWithBye()

			roomId := "test-room"
			MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello1.Hello)
			MustSucceed2(t, client2.JoinRoom, ctx, roomId)
			client1.RunUntilJoined(ctx, hello2.Hello)
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			session1, ok := hub1.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
			require.True(ok)
			backend := session1.Backend()
			hubs := []*Hub{hub1, hub2}
			clients := []*TestClient{client1, client2}

			// A user that is connected to a different server is in the call, none
			// of the local sessions is in the call.
			other := api.PublicSessionId("the-other-session")
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type: "incall",
				InCall: &talk.BackendRoomInCallRequest{
					InCall: json.RawMessage(strconv.Itoa(FlagInCall)),
					Changed: api.UserDataList{
						{
							"sessionId": other,
							"inCall":    FlagInCall,
						},
					},
				},
			})
			for _, client := range clients {
				if msg, ok := client.RunUntilMessage(ctx); ok {
					checkMessageParticipantsInCall(t, msg)
				}
			}
			assertRoomUsers(t, roomId, hubs, other)

			// The call is ended for everybody.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type: "incall",
				InCall: &talk.BackendRoomInCallRequest{
					InCall: json.RawMessage(strconv.Itoa(FlagDisconnected)),
					All:    true,
				},
			})
			for _, client := range clients {
				if msg, ok := client.RunUntilMessage(ctx); ok {
					checkMessageInCallAll(t, msg, roomId, FlagDisconnected)
				}
			}
			assertRoomUsers(t, roomId, hubs)

			// Nobody is in the call, so nobody gets notified.
			publishBackendRoomRequest(t, hub1, roomId, backend, &talk.BackendServerRoomRequest{
				Type: "incall",
				InCall: &talk.BackendRoomInCallRequest{
					InCall: json.RawMessage(strconv.Itoa(FlagDisconnected)),
					All:    true,
				},
			})
			assertNoMoreMessages(t, clients...)
		})
	}
}
