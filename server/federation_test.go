/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/client"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/sfu/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/talk"
)

func Test_FederationInvalidToken(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	_, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client := NewTestClient(t, server2, hub2)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	MustSucceed1(t, client.RunUntilHello, ctx)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    "test-room",
			SessionId: "room-session-id",
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				Token:        "invalid-token",
			},
		},
	}
	require.NoError(client.WriteJSON(msg))

	if message, ok := client.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("error", message.Type)
		require.Equal("invalid_token", message.Error.Code)
	}
}

func Test_Federation(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	features1 := []string{"one", "two", "three"}
	require.NoError(client1.SendHelloV2WithFeatures(testDefaultUserId+"1", features1))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	features2 := []string{"1", "2", "3"}
	require.NoError(client2.SendHelloV2WithFeatures(testDefaultUserId+"2", features2))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	room := hub1.getRoom(roomId)
	require.NotNil(room)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
		assert.True(evt.Federated)
		assert.Equal(features2, evt.Features)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	tmpRoom1 := hub2.getRoom(roomId)
	assert.Nil(tmpRoom1)
	tmpRoom2 := hub2.getRoom(federatedRoomId)
	assert.Nil(tmpRoom2)

	// The host hub has no federated sessions and thus doesn't send pings.
	count1, wg1 := hub1.publishFederatedSessions()
	wg1.Wait()
	assert.Equal(0, count1)

	count1, wg1 = room.publishActiveSessions()
	wg1.Wait()
	assert.Equal(2, count1)

	request1 := getPingRequests(t)
	clearPingRequests(t)
	if assert.Len(request1, 1) {
		if ping := request1[0].Ping; assert.NotNil(ping) {
			assert.Equal(roomId, ping.RoomId)
			assert.Equal("1.0", ping.Version)
			assert.Len(ping.Entries, 2)
			// The order of entries is not defined
			if ping.Entries[0].SessionId == api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)) {
				assert.Equal(hello2.Hello.UserId, ping.Entries[0].UserId)

				assert.EqualValues(fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId), ping.Entries[1].SessionId)
				assert.Equal(hello1.Hello.UserId, ping.Entries[1].UserId)
			} else {
				assert.EqualValues(fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId), ping.Entries[0].SessionId)
				assert.Equal(hello1.Hello.UserId, ping.Entries[0].UserId)

				assert.EqualValues(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId), ping.Entries[1].SessionId)
				assert.Equal(hello2.Hello.UserId, ping.Entries[1].UserId)
			}
		}
	}

	// The federated hub has a federated session for which it sends a ping.
	count2, wg2 := hub2.publishFederatedSessions()
	wg2.Wait()
	assert.Equal(1, count2)

	request2 := getPingRequests(t)
	clearPingRequests(t)
	if assert.Len(request2, 1) {
		if ping := request2[0].Ping; assert.NotNil(ping) {
			assert.Equal(federatedRoomId, ping.RoomId)
			assert.Equal("1.0", ping.Version)
			assert.Len(ping.Entries, 1)
			assert.EqualValues(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId), ping.Entries[0].SessionId)
			assert.Equal(hello2.Hello.UserId, ping.Entries[0].UserId)
		}
	}

	// Leaving and re-joining a room as "direct" session will trigger correct events.
	if room, ok := client1.JoinRoom(ctx, ""); ok {
		assert.Empty(room.Room.RoomId)
	}

	client2.RunUntilLeft(ctx, hello1.Hello)

	if room, ok := client1.JoinRoom(ctx, roomId); ok {
		assert.Equal(roomId, room.Room.RoomId)
	}

	client1.RunUntilJoined(ctx, hello1.Hello, &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	})
	client2.RunUntilJoined(ctx, hello1.Hello)

	// Leaving and re-joining a room as "federated" session will trigger correct events.
	if room, ok := client2.JoinRoom(ctx, ""); ok {
		assert.Empty(room.Room.RoomId)
	}

	client1.RunUntilLeft(ctx, &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	})

	// The federated session has left the room, so no more pings.
	count3, wg3 := hub2.publishFederatedSessions()
	wg3.Wait()
	assert.Equal(0, count3)

	require.NoError(client2.WriteJSON(msg))
	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// Client1 will receive the updated "remoteSessionId"
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		if assert.Len(message.Event.Join, 1, "invalid message received: %+v", message) {
			evt := message.Event.Join[0]
			remoteSessionId = evt.SessionId
			assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
			assert.Equal(testDefaultUserId+"2", evt.UserId)
			assert.True(evt.Federated)
			assert.Equal(features2, evt.Features)
		}
	}
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	// Test sending messages between sessions.
	data1 := "from-1-to-2"
	data2 := "from-2-to-1"
	if assert.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data1)) {
		var payload string
		if checkReceiveClientMessage(ctx, t, client2, "session", hello1.Hello, &payload) {
			assert.Equal(data1, payload)
		}
	}

	if assert.NoError(client1.SendControl(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data1)) {
		var payload string
		if checkReceiveClientControl(ctx, t, client2, "session", hello1.Hello, &payload) {
			assert.Equal(data1, payload)
		}
	}

	if assert.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, data2)) {
		var payload string
		if checkReceiveClientMessage(ctx, t, client1, "session", &api.HelloServerMessage{
			SessionId: remoteSessionId,
			UserId:    testDefaultUserId + "2",
		}, &payload) {
			assert.Equal(data2, payload)
		}
	}

	if assert.NoError(client2.SendControl(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, data2)) {
		var payload string
		if checkReceiveClientControl(ctx, t, client1, "session", &api.HelloServerMessage{
			SessionId: remoteSessionId,
			UserId:    testDefaultUserId + "2",
		}, &payload) {
			assert.Equal(data2, payload)
		}
	}

	// Special handling for the "forceMute" control event.
	forceMute := api.StringMap{
		"action": "forceMute",
		"peerId": remoteSessionId,
	}
	if assert.NoError(client1.SendControl(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, forceMute)) {
		var payload api.StringMap
		if checkReceiveClientControl(ctx, t, client2, "session", hello1.Hello, &payload) {
			// The sessionId in "peerId" will be replaced with the local one.
			forceMute["peerId"] = string(hello2.Hello.SessionId)
			assert.Equal(forceMute, payload)
		}
	}

	data3 := "from-2-to-2"
	// Clients can't send to their own (local) session id.
	if assert.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, data3)) {
		ctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel2()

		client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
	}

	// Clients can't send to their own (remote) session id.
	if assert.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data3)) {
		ctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel2()

		client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
	}

	// Simulate request from the backend that a federated user joined the call.
	users := []api.StringMap{
		{
			"sessionId": remoteSessionId,
			"inCall":    1,
			"actorId":   "remoteUser@" + strings.TrimPrefix(server2.URL, "http://"),
			"actorType": "federated_users",
		},
	}
	room.PublishUsersInCallChanged(users)
	var event *api.EventServerMessage
	// For the local user, it's a federated user on server 2 that joined.
	if checkReceiveClientEvent(ctx, t, client1, "update", &event) {
		assert.Equal(roomId, event.Update.RoomId)
		if assert.Len(event.Update.Users, 1) {
			assert.EqualValues(remoteSessionId, event.Update.Users[0]["sessionId"])
			assert.Equal("remoteUser@"+strings.TrimPrefix(server2.URL, "http://"), event.Update.Users[0]["actorId"])
			assert.Equal("federated_users", event.Update.Users[0]["actorType"])
		}
	}
	// For the federated user, it's a local user that joined.
	if checkReceiveClientEvent(ctx, t, client2, "update", &event) {
		assert.Equal(federatedRoomId, event.Update.RoomId)
		if assert.Len(event.Update.Users, 1) {
			assert.EqualValues(hello2.Hello.SessionId, event.Update.Users[0]["sessionId"])
			assert.Equal("remoteUser", event.Update.Users[0]["actorId"])
			assert.Equal("users", event.Update.Users[0]["actorType"])
		}
	}

	// Simulate request from the backend that a local user joined the call.
	users = []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
			"actorId":   "localUser",
			"actorType": "users",
		},
	}
	room.PublishUsersInCallChanged(users)
	// For the local user, it's a local user that joined.
	if checkReceiveClientEvent(ctx, t, client1, "update", &event) {
		assert.Equal(roomId, event.Update.RoomId)
		if assert.Len(event.Update.Users, 2) {
			users := event.Update.Users.Map()
			if user := users[hello1.Hello.SessionId]; assert.NotNil(user, "expected %s, got %+v", hello1.Hello.SessionId, event) {
				assert.EqualValues(hello1.Hello.SessionId, user["sessionId"])
				assert.Equal("localUser", user["actorId"])
				assert.Equal("users", user["actorType"])
			}

			if user := users[remoteSessionId]; assert.NotNil(user, "expected %s, got %+v", remoteSessionId, event) {
				assert.EqualValues(remoteSessionId, user["sessionId"])
				assert.Equal("remoteUser@"+strings.TrimPrefix(server2.URL, "http://"), user["actorId"])
				assert.Equal("federated_users", user["actorType"])
			}
		}
	}
	// For the federated user, it's a federated user on server 1 that joined.
	if checkReceiveClientEvent(ctx, t, client2, "update", &event) {
		assert.Equal(federatedRoomId, event.Update.RoomId)
		if assert.Len(event.Update.Users, 2) {
			users := event.Update.Users.Map()
			if user := users[hello1.Hello.SessionId]; assert.NotNil(user, "expected %s, got %+v", hello1.Hello.SessionId, event) {
				assert.EqualValues(hello1.Hello.SessionId, user["sessionId"])
				assert.Equal("localUser@"+strings.TrimPrefix(server1.URL, "http://"), user["actorId"])
				assert.Equal("federated_users", user["actorType"])
			}

			if user := users[hello2.Hello.SessionId]; assert.NotNil(user, "expected %s, got %+v", hello2.Hello.SessionId, event) {
				assert.EqualValues(hello2.Hello.SessionId, user["sessionId"])
				assert.Equal("remoteUser", user["actorId"])
				assert.Equal("users", user["actorType"])
			}
		}
	}

	// Joining another "direct" session will trigger correct events.

	client3 := NewTestClient(t, server1, hub1)
	defer client3.CloseWithBye()
	require.NoError(client3.SendHelloV2(testDefaultUserId + "3"))

	hello3 := MustSucceed1(t, client3.RunUntilHello, ctx)

	if room, ok := client3.JoinRoom(ctx, roomId); ok {
		require.Equal(roomId, room.Room.RoomId)
	}

	client1.RunUntilJoined(ctx, hello3.Hello)
	client2.RunUntilJoined(ctx, hello3.Hello)

	client3.RunUntilJoined(ctx, hello1.Hello, &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}, hello3.Hello)

	// Joining another "federated" session will trigger correct events.

	client4 := NewTestClient(t, server2, hub1)
	defer client4.CloseWithBye()
	require.NoError(client4.SendHelloV2WithFeatures(testDefaultUserId+"4", features2))

	hello4 := MustSucceed1(t, client4.RunUntilHello, ctx)

	userdata = api.StringMap{
		"displayname": "Federated user 2",
		"actorType":   "federated_users",
		"actorId":     "the-other-federated-user-id",
	}
	token, err = client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"4", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg = &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello4.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client4.WriteJSON(msg))

	if message, ok := client4.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client4.
	var remoteSessionId4 api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId4 = evt.SessionId
		assert.NotEqual(hello4.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"4", evt.UserId)
		assert.True(evt.Federated)
		assert.Equal(features2, evt.Features)
	}

	client2.RunUntilJoined(ctx, &api.HelloServerMessage{
		SessionId: remoteSessionId4,
		UserId:    hello4.Hello.UserId,
	})

	client3.RunUntilJoined(ctx, &api.HelloServerMessage{
		SessionId: remoteSessionId4,
		UserId:    hello4.Hello.UserId,
	})

	client4.RunUntilJoined(ctx, hello1.Hello, &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}, hello3.Hello, hello4.Hello)

	if room3, ok := client2.JoinRoom(ctx, ""); ok {
		assert.Empty(room3.Room.RoomId)
	}
}

func Test_FederationCloseOnSessionTeardown(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// Drain the "join" event seen by client1 so it doesn't interfere with cleanup.
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
	}

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)
	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)
	require.False(fed2.closer.IsClosed(), "federation client should still be connected")

	// Simulate the session itself being torn down (as "Hub.removeSession"
	// does by calling "session.LeaveRoom(true)", i.e. with a nil message).
	// The remote server will never see this session again, so the federation
	// client must be closed immediately instead of waiting for a "room" reply
	// from the remote that will now never be read by anyone.
	session2.LeaveRoomWithMessage(true, nil)

	assert.True(fed2.closer.IsClosed(), "federation client must be closed synchronously when the session is torn down")
}

func Test_FederationTimeouts(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, _, _, server1, server2 := CreateClusteredHubsForTestWithConfig(t, func(server *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(server)
		if err != nil {
			return nil, err
		}

		config.AddOption("federation", "pinginterval", "1")
		config.AddOption("federation", "readtimeout", "1")
		config.AddOption("federation", "writetimeout", "2")
		return config, nil
	})

	expected := client.Timeouts{
		PingInterval: time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: 2 * time.Second,
	}
	assert.Equal(expected, hub2.getFederationTimeouts())
	// Federation timeouts don't affect the listeners.
	assert.Equal(client.Timeouts{}.WithDefaults(), hub2.getTimeouts(false))
	assert.Equal(client.Timeouts{}.WithDefaults(), hub2.getTimeouts(true))

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
	}
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)
	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)
	assert.Equal(expected, fed2.timeouts)
	localAddr := fed2.LocalAddr()
	require.NotNil(localAddr)

	// The remote server responds to pings, so the connection is kept open
	// after the read timeout expired.
	time.Sleep(2 * (expected.PingInterval + expected.ReadTimeout))
	assert.False(fed2.closer.IsClosed())
	assert.False(fed2.reconnecting.Load())
	if addr := fed2.LocalAddr(); assert.NotNil(addr) {
		assert.Equal(localAddr.String(), addr.String(), "federation connection was reconnected")
	}

	pendingCtx, pendingCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer pendingCancel()
	if messages, err := client2.GetPendingMessages(pendingCtx); !assert.ErrorIs(err, context.DeadlineExceeded) {
		assert.Fail("expected no messages", "received %+v", messages)
	}

	// Changing the timeouts only affects new federation connections.
	config, err := getTestConfig(server2)
	require.NoError(err)
	config.AddOption("federation", "pinginterval", "30")
	hub2.loadTimeouts(config)
	assert.Equal(client.Timeouts{
		PingInterval: 30 * time.Second,
		ReadTimeout:  client.DefaultReadTimeout,
		WriteTimeout: client.DefaultWriteTimeout,
	}, hub2.getFederationTimeouts())
	assert.Equal(expected, fed2.timeouts)
}

func Test_FederationRoomDeleted(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
	}

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)
	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)

	// Simulate request from the backend that the room was deleted.
	room := hub1.getRoom(roomId)
	require.NotNil(room)
	room.processAsyncMessage(&events.AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type:   "delete",
			Delete: &talk.BackendRoomDeleteRequest{},
		},
	})

	// The federated session is notified about the deletion (with the local
	// room id) and that it is no longer in the room.
	message, ok := client2.RunUntilMessage(ctx)
	for ok && message.Type == "event" && message.Event.Target == "room" && message.Event.Type == "join" {
		// Ignore "join" events of the sessions in the room.
		message, ok = client2.RunUntilMessage(ctx)
	}
	if ok && assert.Equal("event", message.Type, "%+v", message) {
		assert.Equal("roomlist", message.Event.Target)
		assert.Equal("disinvite", message.Event.Type)
		if assert.NotNil(message.Event.Disinvite) {
			assert.Equal(federatedRoomId, message.Event.Disinvite.RoomId)
			assert.Equal(api.DisinviteReasonDeleted, message.Event.Disinvite.Reason)
		}
	}
	if message, ok := client2.RunUntilMessage(ctx); ok && assert.Equal("room", message.Type, "%+v", message) {
		assert.Empty(message.Room.RoomId)
	}

	// The federation client is closed and doesn't try to reconnect.
	assert.Eventually(fed2.closer.IsClosed, time.Second, time.Millisecond)
	assert.Nil(session2.GetFederationClient())
	assert.False(fed2.reconnecting.Load())

	// No further messages (e.g. "federation_interrupted") are sent.
	ctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel2()
	client2.RunUntilErrorIs(ctx2, context.DeadlineExceeded)
}

func Test_FederationClosedClientNotReused(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	// The client was closed but not detached from the session yet, e.g. while
	// processing a "disinvite" event. Joining a room on the same remote server
	// uses a new client.
	fed1 := s.joinFederated(t, "join-room-fed")
	fed1.Close()
	assert.ErrorIs(fed1.SendMessage(&api.ClientMessage{Type: "room"}), ErrNotConnected)
	fed2 := s.joinFederated(t, "join-room-fed-2")
	assert.NotSame(fed1, fed2)
}

func Test_FederationLeaveThroughClosedClient(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	// Leaving the room through a closed client gets a response.
	fed := s.joinFederated(t, "join-room-fed")
	fed.Close()
	leaveMsg := &api.ClientMessage{
		Id:   "leave-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId: "",
		},
	}
	require.NoError(s.client2.WriteJSON(leaveMsg))
	message, ok := s.client2.RunUntilMessage(ctx)
	for ok && message.Type == "event" {
		message, ok = s.client2.RunUntilMessage(ctx)
	}
	if ok && assert.Equal("room", message.Type, "%+v", message) {
		assert.Equal(leaveMsg.Id, message.Id)
		assert.Empty(message.Room.RoomId)
	}
	assert.Nil(s.session2.GetFederationClient())
}

func Test_FederationDisinviteWhileSwitchingRooms(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)
	otherJoin := s.otherRoomMessage(t, "join-other-room-fed")
	previous := simulatePendingJoin(fed, otherJoin)
	disinvite := func() {
		t.Helper()
		fed.processMessage(&api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "roomlist",
				Type:   "disinvite",
				Disinvite: &api.RoomDisinviteEventServerMessage{
					RoomEventServerMessage: api.RoomEventServerMessage{
						RoomId: s.roomId,
					},
					Reason: api.DisinviteReasonDisinvited,
				},
			},
		})
		if message, ok := s.client2.RunUntilMessage(ctx); ok && assert.Equal("event", message.Type, "%+v", message) {
			assert.Equal("disinvite", message.Event.Type)
			if assert.NotNil(message.Event.Disinvite) {
				assert.Equal(s.federatedRoomId, message.Event.Disinvite.RoomId)
			}
		}
	}

	// A "disinvite" for the previous room while switching to another room on
	// the same remote server doesn't close the client.
	disinvite()
	assert.False(fed.closer.IsClosed())
	assert.Same(fed, s.session2.GetFederationClient())
	assertNoMoreMessages(t, s.client2)

	// The join of the other room fails, so the remote session is still in the
	// previous room and a "disinvite" for it closes the client.
	fed.processMessage(&api.ServerMessage{
		Id:    otherJoin.Id,
		Type:  "error",
		Error: api.NewError("not_allowed", "Not allowed to join."),
	})
	if message, ok := s.client2.RunUntilMessage(ctx); ok && assert.Equal("error", message.Type, "%+v", message) {
		assert.Equal(otherJoin.Id, message.Id)
	}
	assert.Nil(fed.pendingJoin.Load())
	assert.Same(previous, fed.message.Load())

	disinvite()
	if message, ok := s.client2.RunUntilMessage(ctx); ok && assert.Equal("room", message.Type, "%+v", message) {
		assert.Empty(message.Room.RoomId)
	}
	assert.True(fed.closer.IsClosed())
	assert.Nil(s.session2.GetFederationClient())
}

func Test_FederationAlreadyJoinedWhileSwitchingRooms(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)
	otherJoin := s.otherRoomMessage(t, "join-other-room-fed")
	simulatePendingJoin(fed, otherJoin)

	// An "already_joined" error for the join of another room means the remote
	// session is in the requested room.
	fed.processMessage(&api.ServerMessage{
		Id:    otherJoin.Id,
		Type:  "error",
		Error: api.NewError("already_joined", "Already joined this room."),
	})
	if message, ok := s.client2.RunUntilMessage(ctx); ok && assert.Equal("error", message.Type, "%+v", message) {
		assert.Equal(otherJoin.Id, message.Id)
	}
	assert.Nil(fed.pendingJoin.Load())
	assert.Same(otherJoin, fed.message.Load())
	assert.Equal("other-room", fed.RemoteRoomId())
	assert.Equal("other-room@federated", fed.RoomId())
}

func Test_FederationJoinWhileProcessingHelloAfterReset(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)
	otherJoin := s.otherRoomMessage(t, "join-other-room-fed")
	fed.message.Store(otherJoin)

	// Sending a join request while processing the hello response and the
	// connection was reset in the meantime doesn't deadlock.
	fed.helloMu.Lock()
	interruptFederationConnection(t, fed)
	joined := make(chan error, 1)
	go func() {
		joined <- fed.joinRoom()
	}()
	select {
	case err := <-joined:
		assert.NoError(err)
	case <-time.After(time.Second):
		assert.Fail("joining blocked")
	}
	fed.helloMu.Unlock()
	fed.mu.Lock()
	if assert.Len(fed.pendingMessages, 1) {
		assert.Equal("room", fed.pendingMessages[0].Type)
	}
	fed.mu.Unlock()

	// The join request is sent after the session was resumed.
	fed.reconnect()
	message, ok := s.client2.RunUntilMessage(ctx)
	for ok && message.Type == "event" {
		message, ok = s.client2.RunUntilMessage(ctx)
	}
	if ok && assert.Equal("room", message.Type, "%+v", message) {
		// The id of the join request is cleared once it was answered.
		assert.Equal("join-other-room-fed", message.Id)
		assert.Equal(otherJoin.Room.RoomId, message.Room.RoomId)
	}
}

func Test_FederationReplaceClientWhileSending(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")

	// The session can receive messages from the client (e.g. if its connection
	// was interrupted) while the client is replaced.
	fed.mu.Lock()
	replaced := make(chan struct{})
	go func() {
		defer close(replaced)
		s.session2.SetFederationClient(nil)
	}()
	// Wait until the client is being closed.
	assert.Eventually(fed.closer.IsClosed, time.Second, time.Millisecond)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		s.session2.SendMessage(&api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "room",
				Type:   "federation_interrupted",
			},
		})
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		assert.Fail("sending message blocked")
	}
	fed.mu.Unlock()
	<-replaced
	<-sent
	assert.True(fed.closer.IsClosed())
	assert.Nil(s.session2.GetFederationClient())
}

func Test_FederationReplacedClientReconnected(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)

	// A client that reconnected with a new session after the session switched
	// to another client doesn't replace the current client.
	other := &FederationClient{}
	s.session2.federation.Store(other)
	defer s.session2.federation.Store(nil)
	fed.reconnecting.Store(true)
	fed.helloMu.Lock()
	fed.helloMsgId = "the-hello-id"
	fed.helloMu.Unlock()
	fed.mu.Lock()
	fed.resumeId = ""
	fed.mu.Unlock()
	fed.processHello(&api.ServerMessage{
		Id:   "the-hello-id",
		Type: "hello",
		Hello: &api.HelloServerMessage{
			Version:   api.HelloVersionV2,
			SessionId: "the-new-session-id",
			ResumeId:  "the-resume-id",
		},
	})
	assert.Same(other, s.session2.GetFederationClient())
	assert.True(fed.closer.IsClosed())
	assertNoMoreMessages(t, s.client2)
}

func Test_FederationLeaveWhileInterruptedNotResumed(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)
	remoteSession, ok := s.hub1.GetSessionByPublicId(fed.hello.Load().SessionId).(*ClientSession)
	require.True(ok)
	fed.mu.Lock()
	fed.reconnectDelay = 500 * time.Millisecond
	fed.mu.Unlock()
	// Simulate disconnected federated client with an expired session.
	if client := remoteSession.GetClient(); client != nil {
		remoteSession.ClearClient(client)
		client.Close()
	}
	remoteSession.Close()
	message, ok := s.client2.RunUntilMessage(ctx)
	for ok && (message.Type != "event" || message.Event.Type != "federation_interrupted") {
		message, ok = s.client2.RunUntilMessage(ctx)
	}
	require.True(ok)

	// Leaving the room while the connection is interrupted closes the client
	// and answers the request if the session could not be resumed.
	leaveMsg := &api.ClientMessage{
		Id:   "leave-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId: "",
		},
	}
	require.NoError(s.client2.WriteJSON(leaveMsg))
	if message, ok := s.client2.RunUntilMessage(ctx); ok && assert.Equal("room", message.Type, "%+v", message) {
		assert.Equal(leaveMsg.Id, message.Id)
		assert.Empty(message.Room.RoomId)
	}
	assert.True(fed.closer.IsClosed())
	assert.Nil(s.session2.GetFederationClient())
	s.hub2.mu.RLock()
	_, found := s.hub2.federatedSessions[s.session2]
	s.hub2.mu.RUnlock()
	assert.False(found)
	assertNoMoreMessages(t, s.client2)
}

func Test_FederationCloseOnSwitchToLocalRoom(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// Drain the "join" events seen by client1 and client2 so they don't
	// interfere with the room switch performed below.
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
	}
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)
	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)
	require.False(fed2.closer.IsClosed(), "federation client should still be connected")

	// Client2 now switches from the federated room to a plain, local room
	// while keeping its session connected (e.g. "Hub.processJoinRoom" calls
	// "session.LeaveRoom(true)" - a nil message - before joining the new
	// room). This must also close the still-attached federation client
	// immediately, since no reply forwarded through it would ever reach the
	// session's new (local) room.
	localRoomId := "local-room"
	room2 := MustSucceed2(t, client2.JoinRoom, ctx, localRoomId)
	require.Equal(localRoomId, room2.Room.RoomId)

	assert.True(fed2.closer.IsClosed(), "federation client must be closed synchronously when switching to a local room")
}

func Test_FederationJoinRoomTwice(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(hello2.Hello.UserId, evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	msg2 := &api.ClientMessage{
		Id:   "join-room-fed-2",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg2))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg2.Id, message.Id)
		if assert.Equal("error", message.Type) {
			assert.Equal("already_joined", message.Error.Code)
		}
		if assert.NotNil(message.Error.Details) {
			var roomMsg api.RoomErrorDetails
			if assert.NoError(json.Unmarshal(message.Error.Details, &roomMsg)) {
				if assert.NotNil(roomMsg.Room) {
					assert.Equal(federatedRoomId, roomMsg.Room.RoomId)
					assert.Equal(string(testRoomProperties), string(roomMsg.Room.Properties))
				}
			}
		}
	}
}

func Test_FederationChangeRoom(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	fed := session2.GetFederationClient()
	require.NotNil(fed)
	localAddr := fed.LocalAddr()

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(hello2.Hello.UserId, evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	roomId2 := roomId + "-2"
	federatedRoomId2 := roomId2 + "@federated"
	msg2 := &api.ClientMessage{
		Id:   "join-room-fed-2",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId2,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId2, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId2,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg2))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg2.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId2, message.Room.RoomId)
	}

	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)
	localAddr2 := fed2.LocalAddr()
	assert.Equal(localAddr, localAddr2)
	// The join was answered.
	assert.Nil(fed2.pendingJoin.Load())
	assert.Equal(roomId2, fed2.RemoteRoomId())

	// Switching rooms while the connection is interrupted sends the join after
	// the session was resumed.
	fed2.mu.Lock()
	fed2.reconnectDelay = 100 * time.Millisecond
	fed2.scheduleReconnectLocked()
	fed2.mu.Unlock()

	roomId3 := roomId + "-3"
	federatedRoomId3 := roomId3 + "@federated"
	msg3 := &api.ClientMessage{
		Id:   "join-room-fed-3",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId3,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId3, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId3,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg3))

	var resumed bool
	message, ok := client2.RunUntilMessage(ctx)
	for ok && message.Type == "event" {
		if message.Event.Target == "room" && message.Event.Type == "federation_resumed" {
			if assert.NotNil(message.Event.Resumed) {
				assert.True(*message.Event.Resumed)
			}
			resumed = true
		}
		message, ok = client2.RunUntilMessage(ctx)
	}
	if ok && assert.Equal("room", message.Type, "%+v", message) {
		assert.Equal(msg3.Id, message.Id)
		assert.Equal(federatedRoomId3, message.Room.RoomId)
	}
	assert.True(resumed)
	assert.Same(fed2, session2.GetFederationClient())
	assert.Nil(fed2.pendingJoin.Load())
	assert.Equal(roomId3, fed2.RemoteRoomId())
}

func Test_FederationMedia(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu1 := test.NewSFU(t)
	require.NoError(mcu1.Start(ctx))
	defer mcu1.Stop()

	hub1.SetMcu(mcu1)

	mcu2 := test.NewSFU(t)
	require.NoError(mcu2.Start(ctx))
	defer mcu2.Stop()

	hub2.SetMcu(mcu2)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "12345",
		RoomType: "screen",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client2.RunUntilAnswerFromSender(ctx, mock.MockSdpAnswerAudioAndVideo, &api.MessageServerMessageSender{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
		UserId:    hello2.Hello.UserId,
	})
}

func Test_FederationResume(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	session2 := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	fed2 := session2.GetFederationClient()
	require.NotNil(fed2)
	fed2.mu.Lock()
	err = fed2.conn.Close()

	data2 := "from-2-to-1"
	assert.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, data2))
	fed2.mu.Unlock()
	assert.NoError(err)

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal("event", message.Type)
		assert.Equal("room", message.Event.Target)
		assert.Equal("federation_interrupted", message.Event.Type)
	}

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal("event", message.Type)
		assert.Equal("room", message.Event.Target)
		assert.Equal("federation_resumed", message.Event.Type)
		assert.NotNil(message.Event.Resumed)
		assert.True(*message.Event.Resumed)
	}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel1()

	var payload string
	if checkReceiveClientMessage(ctx, t, client1, "session", &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    testDefaultUserId + "2",
	}, &payload) {
		assert.Equal(data2, payload)
	}

	client1.RunUntilErrorIs(ctx1, ErrNoMessageReceived, context.DeadlineExceeded)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	client2.RunUntilErrorIs(ctx2, ErrNoMessageReceived, context.DeadlineExceeded)
}

func Test_FederationResumeNewSession(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(hello2.Hello.UserId, evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	remoteSession2 := hub1.GetSessionByPublicId(remoteSessionId).(*ClientSession)
	// Simulate disconnected federated client with an expired session.
	if client := remoteSession2.GetClient(); client != nil {
		remoteSession2.ClearClient(client)
		client.Close()
	}
	remoteSession2.Close()

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal("event", message.Type)
		assert.Equal("room", message.Event.Target)
		assert.Equal("federation_interrupted", message.Event.Type)
	}

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal("event", message.Type)
		assert.Equal("room", message.Event.Target)
		assert.Equal("federation_resumed", message.Event.Type)
		assert.NotNil(message.Event.Resumed)
		assert.False(*message.Event.Resumed)
	}

	// Client1 will get a "leave" for the expired session and a "join" with the
	// new remote session id.
	client1.RunUntilLeft(ctx, &api.HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	})
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		assert.NotEqual(remoteSessionId, evt.SessionId)
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(hello2.Hello.UserId, evt.UserId)
		assert.True(evt.Federated)
	}

	// client2 will join the room again after the reconnect with the new
	// session and get "joined" events for all sessions in the room (including
	// its own).
	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Empty(message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
}

func Test_FederationTransientData(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
	hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, room1.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	msg := &api.ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
			Federation: &api.RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, ok := client2.RunUntilMessage(ctx); ok {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId api.PublicSessionId
	if message, ok := client1.RunUntilMessage(ctx); ok {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(hello2.Hello.UserId, evt.UserId)
		assert.True(evt.Federated)
	}

	// The client2 will see its own session id, not the one from the remote server.
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	// Regular transient data.
	require.NoError(client1.SetTransientData("foo", "bar", 0))
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, "foo", "bar", nil)
	}
	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, "foo", "bar", nil)
	}

	require.NoError(client2.SetTransientData("bar", "baz", 0))
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, "bar", "baz", nil)
	}
	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, "bar", "baz", nil)
	}

	// Transient session data
	sessionKey1 := "sd:" + string(hello1.Hello.SessionId)
	require.NoError(client1.SetTransientData(sessionKey1, "12345", 0))
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, sessionKey1, "12345", nil)
	}
	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, sessionKey1, "12345", nil)
	}

	sessionKey2 := "sd:" + string(hello2.Hello.SessionId)
	require.NoError(client2.SetTransientData(sessionKey2, "54321", 0))
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, "sd:"+string(remoteSessionId), "54321", nil)
	}
	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageTransientSet(t, msg, sessionKey2, "54321", nil)
	}
}

// federationTestSetup contains a session on the second hub that can join a
// room on the first hub through federation.
type federationTestSetup struct {
	ctx             context.Context
	hub1            *Hub
	hub2            *Hub
	client2         *TestClient
	hello1          *api.ServerMessage
	hello2          *api.ServerMessage
	session2        *ClientSession
	roomId          string
	federatedRoomId string
	message         func(id string) *api.ClientMessage
}

func newFederationTestSetup(ctx context.Context, t *testing.T) *federationTestSetup {
	t.Helper()
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
	t.Cleanup(func() {
		client1.CloseWithBye()
	})
	client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")
	t.Cleanup(func() {
		client2.CloseWithBye()
	})

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	client1.RunUntilJoined(ctx, hello1.Hello)

	now := time.Now()
	userdata := api.StringMap{
		"displayname": "Federated user",
		"actorType":   "federated_users",
		"actorId":     "the-federated-user-id",
	}
	token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
	require.NoError(err)

	session2, ok := hub2.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.True(ok)

	return &federationTestSetup{
		ctx:             ctx,
		hub1:            hub1,
		hub2:            hub2,
		client2:         client2,
		hello1:          hello1,
		hello2:          hello2,
		session2:        session2,
		roomId:          roomId,
		federatedRoomId: federatedRoomId,
		message: func(id string) *api.ClientMessage {
			return &api.ClientMessage{
				Id:   id,
				Type: "room",
				Room: &api.RoomClientMessage{
					RoomId:    federatedRoomId,
					SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
					Federation: &api.RoomFederationMessage{
						SignalingUrl: server1.URL,
						NextcloudUrl: server1.URL,
						RoomId:       roomId,
						Token:        token,
					},
				},
			}
		},
	}
}

// joinFederated joins the federated room and returns the federation client.
func (s *federationTestSetup) joinFederated(t *testing.T, id string) *FederationClient {
	t.Helper()
	require := require.New(t)
	msg := s.message(id)
	require.NoError(s.client2.WriteJSON(msg))

	message, ok := s.client2.RunUntilMessage(s.ctx)
	for ok && message.Type == "event" {
		message, ok = s.client2.RunUntilMessage(s.ctx)
	}
	require.True(ok)
	require.Equal("room", message.Type, "%+v", message)
	require.Equal(msg.Id, message.Id)
	require.Equal(s.federatedRoomId, message.Room.RoomId)

	fed := s.session2.GetFederationClient()
	require.NotNil(fed)
	return fed
}

// otherRoomMessage returns a request to join another room on the same remote
// server.
func (s *federationTestSetup) otherRoomMessage(t *testing.T, id string) *api.ClientMessage {
	t.Helper()
	msg := s.message(id)
	msg.Room.RoomId = "other-room@federated"
	msg.Room.SessionId = api.RoomSessionId(fmt.Sprintf("other-room@federated-%s", s.hello2.Hello.SessionId))
	msg.Room.Federation.RoomId = "other-room"
	require.NoError(t, msg.Room.Federation.CheckValid())
	return msg
}

// simulatePendingJoin simulates that a request to join another room was sent
// but not answered yet. Returns the previous join request.
func simulatePendingJoin(fed *FederationClient, message *api.ClientMessage) *api.ClientMessage {
	previous := fed.message.Load()
	fed.message.Store(message)
	fed.pendingJoin.Store(&federationPendingJoin{
		id:       message.Id,
		message:  message,
		previous: previous,
	})
	return previous
}

// interruptFederationConnection resets the connection of the federation client without
// reconnecting automatically.
func interruptFederationConnection(t *testing.T, fed *FederationClient) {
	t.Helper()
	fed.mu.Lock()
	fed.reconnectDelay = time.Hour
	fed.scheduleReconnectLocked()
	// The reading goroutine also schedules a reconnect once it notices the
	// closed connection, wait for it so it doesn't interfere.
	fed.reconnectDelay = time.Hour
	fed.mu.Unlock()
	assert.Eventually(t, func() bool {
		fed.mu.Lock()
		defer fed.mu.Unlock()
		return fed.reconnectDelay != time.Hour
	}, time.Second, time.Millisecond)
}

func Test_FederationNoErrorAfterClose(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)

	// Errors of clients that are closed don't belong to any current request of
	// the session.
	fed.Close()
	fed.closeWithError(errors.New("the error"))
	assertNoMoreMessages(t, s.client2)
}

func Test_FederationOutdatedLeaveResponse(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	s.client2.RunUntilJoined(ctx, s.hello1.Hello, s.hello2.Hello)

	// Leave the room while the connection is interrupted.
	interruptFederationConnection(t, fed)
	if msg, ok := s.client2.RunUntilMessage(ctx); ok {
		assert.Equal("federation_interrupted", msg.Event.Type, "%+v", msg)
	}
	require.NoError(s.client2.WriteJSON(&api.ClientMessage{
		Id:   "leave-room-fed",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId: "",
		},
	}))
	assert.Eventually(func() bool {
		return s.session2.GetFederationClient() == nil
	}, time.Second, time.Millisecond)

	// Join a local room before the request to leave was answered.
	localRoomId := "test-room-local"
	roomMsg := MustSucceed2(t, s.client2.JoinRoom, ctx, localRoomId)
	require.Equal(localRoomId, roomMsg.Room.RoomId)
	s.client2.RunUntilJoined(ctx, s.hello2.Hello)

	// The request to leave is sent after the session was resumed, but the
	// (outdated) response is not forwarded to the session.
	fed.reconnect()
	assert.Eventually(fed.closer.IsClosed, time.Second, time.Millisecond)
	assertNoMoreMessages(t, s.client2)
	if room := s.session2.GetRoom(); assert.NotNil(room) {
		assert.Equal(localRoomId, room.Id())
	}
}

func Test_FederationConnectAfterClose(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")

	// A connection that was established after the client was closed is not
	// used.
	fed.Close()
	require.ErrorIs(fed.connect(ctx), ErrNotConnected)
	fed.mu.Lock()
	defer fed.mu.Unlock()
	require.Nil(fed.conn)
}

func Test_FederationCloseWithBrokenConnection(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	s := newFederationTestSetup(ctx, t)

	fed := s.joinFederated(t, "join-room-fed")
	remoteSession, ok := s.hub1.GetSessionByPublicId(fed.hello.Load().SessionId).(*ClientSession)
	require.True(ok)

	// Sending the "bye" message fails while closing the client, this must not
	// schedule a reconnect (which resets the connection).
	fed.closer.Close()
	fed.mu.Lock()
	require.NotNil(fed.conn)
	require.NoError(fed.conn.UnderlyingConn().Close())
	fed.closeConnection(true)
	assert.Nil(fed.conn)
	assert.Nil(fed.reconnectFunc)
	assert.False(fed.reconnecting.Load())
	fed.mu.Unlock()

	// Finish closing the client. The remote session didn't receive the "bye".
	fed.Close()
	remoteSession.Close()
}
