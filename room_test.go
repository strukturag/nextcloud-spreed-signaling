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
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
		if ok != test.Valid {
			t.Errorf("%+v should be valid %v, got %v", test.Value, test.Valid, ok)
		}
		if inCall != test.InCall {
			t.Errorf("%+v should convert to %v, got %v", test.Value, test.InCall, inCall)
		}
	}
}

func TestRoom_Update(t *testing.T) {
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, hub, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(router); err != nil {
		t.Fatal(err)
	}

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

	// Simulate backend request from Nextcloud to update the room.
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &BackendServerRoomRequest{
		Type: "update",
		Update: &BackendRoomUpdateRequest{
			UserIds: []string{
				testDefaultUserId,
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

	// The client receives a roomlist update and a changed room event. The
	// ordering is not defined because messages are sent by asynchronous NATS
	// handlers.
	message1, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}
	message2, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}

	if msg, err := checkMessageRoomlistUpdate(message1); err != nil {
		if err := checkMessageRoomId(message1, roomId); err != nil {
			t.Error(err)
		}
		if msg, err := checkMessageRoomlistUpdate(message2); err != nil {
			t.Error(err)
		} else if msg.RoomId != roomId {
			t.Errorf("Expected room id %s, got %+v", roomId, msg)
		} else if msg.Properties == nil || !bytes.Equal(*msg.Properties, roomProperties) {
			t.Errorf("Expected room properties %s, got %+v", string(roomProperties), msg)
		}
	} else {
		if msg.RoomId != roomId {
			t.Errorf("Expected room id %s, got %+v", roomId, msg)
		} else if msg.Properties == nil || !bytes.Equal(*msg.Properties, roomProperties) {
			t.Errorf("Expected room properties %s, got %+v", string(roomProperties), msg)
		}
		if err := checkMessageRoomId(message2, roomId); err != nil {
			t.Error(err)
		}
	}

	// Allow up to 100 milliseconds for NATS processing.
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
			} else if room.Properties() == nil || !bytes.Equal(*room.Properties(), roomProperties) {
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

	if err != nil {
		t.Error(err)
	}
}

func TestRoom_Delete(t *testing.T) {
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, hub, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(router); err != nil {
		t.Fatal(err)
	}

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

	// The client is no longer invited to the room and leaves it. The ordering
	// of messages is not defined as they get published through NATS and handled
	// by asynchronous channels.
	message1, err := client.RunUntilMessage(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := checkMessageType(message1, "event"); err != nil {
		// Ordering should be "leave room", "disinvited".
		if err := checkMessageRoomId(message1, ""); err != nil {
			t.Error(err)
		}
		message2, err := client.RunUntilMessage(ctx)
		if err != nil {
			t.Error(err)
		}
		if _, err := checkMessageRoomlistDisinvite(message2); err != nil {
			t.Error(err)
		}
	} else {
		// Ordering should be "disinvited", "leave room".
		if _, err := checkMessageRoomlistDisinvite(message1); err != nil {
			t.Error(err)
		}
		message2, err := client.RunUntilMessage(ctx)
		if err != nil {
			// The connection should get closed after the "disinvited".
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				t.Error(err)
			}
		} else if err := checkMessageRoomId(message2, ""); err != nil {
			t.Error(err)
		}
	}

	// Allow up to 100 milliseconds for NATS processing.
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

	if err != nil {
		t.Error(err)
	}
}

func TestRoom_RoomSessionData(t *testing.T) {
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, hub, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(router); err != nil {
		t.Fatal(err)
	}

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(authAnonymousUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room-with-sessiondata"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event with the userid from the room session data.
	expected := "userid-from-sessiondata"
	if message, err := client.RunUntilMessage(ctx); err != nil {
		t.Error(err)
	} else if err := client.checkMessageJoinedSession(message, hello.Hello.SessionId, expected); err != nil {
		t.Error(err)
	} else if message.Event.Join[0].RoomSessionId != roomId+"-"+hello.Hello.SessionId {
		t.Errorf("Expected join room session id %s, got %+v", roomId+"-"+hello.Hello.SessionId, message.Event.Join[0])
	}

	session := hub.GetSessionByPublicId(hello.Hello.SessionId)
	if session == nil {
		t.Fatalf("Could not find session %s", hello.Hello.SessionId)
	}

	if userid := session.UserId(); userid != expected {
		t.Errorf("Expected userid %s, got %s", expected, userid)
	}

	room := hub.getRoom(roomId)
	if room == nil {
		t.Fatalf("Room not found")
	}

	entries, wg := room.publishActiveSessions()
	if entries != 1 {
		t.Errorf("expected 1 entries, got %d", entries)
	}
	wg.Wait()
}

func TestRoom_InCallAll(t *testing.T) {
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBackendServer(config, hub, "no-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(router); err != nil {
		t.Fatal(err)
	}

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

	if err := client1.RunUntilJoined(ctx, hello1.Hello); err != nil {
		t.Error(err)
	}

	if room, err := client2.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	if err := client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello); err != nil {
		t.Error(err)
	}

	if err := client1.RunUntilJoined(ctx, hello2.Hello); err != nil {
		t.Error(err)
	}

	// Simulate backend request from Nextcloud to update the "inCall" flag of all participants.
	msg1 := &BackendServerRoomRequest{
		Type: "incall",
		InCall: &BackendRoomInCallRequest{
			All:    true,
			InCall: json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
		},
	}

	data1, err := json.Marshal(msg1)
	if err != nil {
		t.Fatal(err)
	}
	res1, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data1)
	if err != nil {
		t.Fatal(err)
	}
	defer res1.Body.Close()
	body1, err := io.ReadAll(res1.Body)
	if err != nil {
		t.Error(err)
	}
	if res1.StatusCode != 200 {
		t.Errorf("Expected successful request, got %s: %s", res1.Status, string(body1))
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else if err := checkMessageInCallAll(msg, roomId, FlagInCall); err != nil {
		t.Fatal(err)
	}

	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else if err := checkMessageInCallAll(msg, roomId, FlagInCall); err != nil {
		t.Fatal(err)
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
	if err != nil {
		t.Fatal(err)
	}
	res2, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	body2, err := io.ReadAll(res2.Body)
	if err != nil {
		t.Error(err)
	}
	if res2.StatusCode != 200 {
		t.Errorf("Expected successful request, got %s: %s", res2.Status, string(body2))
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else if err := checkMessageInCallAll(msg, roomId, 0); err != nil {
		t.Fatal(err)
	}

	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else if err := checkMessageInCallAll(msg, roomId, 0); err != nil {
		t.Fatal(err)
	}
}
