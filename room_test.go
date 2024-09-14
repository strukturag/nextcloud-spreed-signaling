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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoom_InCall(t *testing.T) {
	type Testcase struct {
		Value  interface{}
		InCall bool
		Valid  bool
	}
	tests := []Testcase{
		{nil, false, false},
		{"a", false, false},
		{true, true, true},
		{false, false, true},
		{0, false, true},
		{FlagDisconnected, false, true},
		{1, true, true},
		{FlagInCall, true, true},
		{2, false, true},
		{FlagWithAudio, false, true},
		{3, true, true},
		{FlagInCall | FlagWithAudio, true, true},
		{4, false, true},
		{FlagWithVideo, false, true},
		{5, true, true},
		{FlagInCall | FlagWithVideo, true, true},
		{1.1, true, true},
		{json.Number("1"), true, true},
		{json.Number("1.1"), false, false},
	}
	for _, test := range tests {
		inCall, ok := IsInCall(test.Value)
		if test.Valid {
			assert.True(t, ok, "%+v should be valid", test.Value)
		} else {
			assert.False(t, ok, "%+v should not be valid", test.Value)
		}
		assert.EqualValues(t, test.InCall, inCall, "conversion failed for %+v", test.Value)
	}
}

func TestRoom_Update(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	log := GetLoggerForTest(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(log, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Simulate backend request from Nextcloud to update the room.
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &BackendServerRoomRequest{
		Type: "update",
		Update: &BackendRoomUpdateRequest{
			UserIds: []string{
				testDefaultUserId,
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
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	// The client receives a roomlist update and a changed room event. The
	// ordering is not defined because messages are sent by asynchronous event
	// handlers.
	message1, err := client.RunUntilMessage(ctx)
	assert.NoError(err)
	message2, err := client.RunUntilMessage(ctx)
	assert.NoError(err)

	if msg, err := checkMessageRoomlistUpdate(message1); err != nil {
		assert.NoError(checkMessageRoomId(message1, roomId))
		if msg, err := checkMessageRoomlistUpdate(message2); assert.NoError(err) {
			assert.Equal(roomId, msg.RoomId)
			assert.Equal(string(roomProperties), string(msg.Properties))
		}
	} else {
		assert.Equal(roomId, msg.RoomId)
		assert.Equal(string(roomProperties), string(msg.Properties))
		assert.NoError(checkMessageRoomId(message2, roomId))
	}

	// Allow up to 100 milliseconds for asynchronous event processing.
	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

loop:
	for {
		select {
		case <-ctx2.Done():
			break loop
		default:
			// The internal room has been updated with the new properties.
			if room := hub.getRoom(roomId); room == nil {
				err = fmt.Errorf("Room %s not found in hub", roomId)
			} else if len(room.Properties()) == 0 || !bytes.Equal(room.Properties(), roomProperties) {
				err = fmt.Errorf("Expected room properties %s, got %+v", string(roomProperties), room.Properties())
			} else {
				err = nil
			}
		}
		if err == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.NoError(err)
}

func TestRoom_Delete(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	log := GetLoggerForTest(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(log, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

	// Simulate backend request from Nextcloud to update the room.
	msg := &BackendServerRoomRequest{
		Type: "delete",
		Delete: &BackendRoomDeleteRequest{
			UserIds: []string{
				testDefaultUserId,
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

	// The client is no longer invited to the room and leaves it. The ordering
	// of messages is not defined as they get published through events and handled
	// by asynchronous channels.
	message1, err := client.RunUntilMessage(ctx)
	assert.NoError(err)

	if err := checkMessageType(message1, "event"); err != nil {
		// Ordering should be "leave room", "disinvited".
		assert.NoError(checkMessageRoomId(message1, ""))
		if message2, err := client.RunUntilMessage(ctx); assert.NoError(err) {
			_, err := checkMessageRoomlistDisinvite(message2)
			assert.NoError(err)
		}
	} else {
		// Ordering should be "disinvited", "leave room".
		_, err := checkMessageRoomlistDisinvite(message1)
		assert.NoError(err)
		message2, err := client.RunUntilMessage(ctx)
		if err != nil {
			// The connection should get closed after the "disinvited".
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				assert.NoError(err)
			}
		} else {
			assert.NoError(checkMessageRoomId(message2, ""))
		}
	}

	// Allow up to 100 milliseconds for asynchronous event processing.
	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

loop:
	for {
		select {
		case <-ctx2.Done():
			break loop
		default:
			// The internal room has been updated with the new properties.
			hub.ru.Lock()
			_, found := hub.rooms[roomId]
			hub.ru.Unlock()

			if found {
				err = fmt.Errorf("Room %s still found in hub", roomId)
			} else {
				err = nil
			}
		}
		if err == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.NoError(err)
}

func TestRoom_RoomSessionData(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	log := GetLoggerForTest(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(log, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(authAnonymousUserId))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room-with-sessiondata"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event with the userid from the room session data.
	expected := "userid-from-sessiondata"
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		if assert.NoError(client.checkMessageJoinedSession(message, hello.Hello.SessionId, expected)) {
			assert.Equal(roomId+"-"+hello.Hello.SessionId, message.Event.Join[0].RoomSessionId)
		}
	}

	session := hub.GetSessionByPublicId(hello.Hello.SessionId)
	require.NotNil(session, "Could not find session %s", hello.Hello.SessionId)
	assert.Equal(expected, session.UserId())

	room := hub.getRoom(roomId)
	assert.NotNil(room, "Room not found")

	entries, wg := room.publishActiveSessions()
	assert.Equal(1, entries)
	wg.Wait()
}

func TestRoom_InCallAll(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	log := GetLoggerForTest(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(log, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(testDefaultUserId + "1"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	client2 := NewTestClient(t, server, hub)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(testDefaultUserId + "2"))

	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

	assert.NoError(client1.RunUntilJoined(ctx, hello2.Hello))

	// Simulate backend request from Nextcloud to update the "inCall" flag of all participants.
	msg1 := &BackendServerRoomRequest{
		Type: "incall",
		InCall: &BackendRoomInCallRequest{
			All:    true,
			InCall: json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
		},
	}

	data1, err := json.Marshal(msg1)
	require.NoError(err)
	res1, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data1)
	require.NoError(err)
	defer res1.Body.Close()
	body1, err := io.ReadAll(res1.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res1.StatusCode, "Expected successful request, got %s", string(body1))

	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageInCallAll(msg, roomId, FlagInCall))
	}

	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageInCallAll(msg, roomId, FlagInCall))
	}

	// Simulate backend request from Nextcloud to update the "inCall" flag of all participants.
	msg2 := &BackendServerRoomRequest{
		Type: "incall",
		InCall: &BackendRoomInCallRequest{
			All:    true,
			InCall: json.RawMessage(strconv.FormatInt(0, 10)),
		},
	}

	data2, err := json.Marshal(msg2)
	require.NoError(err)
	res2, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data2)
	require.NoError(err)
	defer res2.Body.Close()
	body2, err := io.ReadAll(res2.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res2.StatusCode, "Expected successful request, got %s", string(body2))

	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageInCallAll(msg, roomId, 0))
	}

	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.NoError(checkMessageInCallAll(msg, roomId, 0))
	}
}
