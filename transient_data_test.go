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
	"testing"
	"time"
)

func Test_TransientData(t *testing.T) {
	data := NewTransientData()
	if data.Set("foo", nil) {
		t.Errorf("should not have set value")
	}
	if !data.Set("foo", "bar") {
		t.Errorf("should have set value")
	}
	if data.Set("foo", "bar") {
		t.Errorf("should not have set value")
	}
	if !data.Set("foo", "baz") {
		t.Errorf("should have set value")
	}
	if data.CompareAndSet("foo", "bar", "lala") {
		t.Errorf("should not have set value")
	}
	if !data.CompareAndSet("foo", "baz", "lala") {
		t.Errorf("should have set value")
	}
	if data.CompareAndSet("test", nil, nil) {
		t.Errorf("should not have set value")
	}
	if !data.CompareAndSet("test", nil, "123") {
		t.Errorf("should have set value")
	}
	if data.CompareAndRemove("test", "1234") {
		t.Errorf("should not have removed value")
	}
	if !data.CompareAndRemove("test", "123") {
		t.Errorf("should have removed value")
	}
	if data.Remove("lala") {
		t.Errorf("should not have removed value")
	}
	if !data.Remove("foo") {
		t.Errorf("should have removed value")
	}
}

func Test_TransientMessages(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

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

	if err := client1.SetTransientData("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "not_in_room"); err != nil {
			t.Fatal(err)
		}
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

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	if session1 == nil {
		t.Fatalf("Session %s does not exist", hello1.Hello.SessionId)
	}
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	if session2 == nil {
		t.Fatalf("Session %s does not exist", hello2.Hello.SessionId)
	}

	// Client 1 may modify transient data.
	session1.SetPermissions([]Permission{PERMISSION_TRANSIENT_DATA})
	// Client 2 may not modify transient data.
	session2.SetPermissions([]Permission{})

	if err := client2.SetTransientData("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "not_allowed"); err != nil {
			t.Fatal(err)
		}
	}

	if err := client1.SetTransientData("foo", "bar"); err != nil {
		t.Fatal(err)
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, "foo", "bar", nil); err != nil {
			t.Fatal(err)
		}
	}
	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, "foo", "bar", nil); err != nil {
			t.Fatal(err)
		}
	}

	if err := client2.RemoveTransientData("foo"); err != nil {
		t.Fatal(err)
	}
	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageError(msg, "not_allowed"); err != nil {
			t.Fatal(err)
		}
	}

	// Setting the same value is ignored by the server.
	if err := client1.SetTransientData("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if msg, err := client1.RunUntilMessage(ctx2); err != nil {
		if err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", msg)
	}

	data := map[string]interface{}{
		"hello": "world",
	}
	if err := client1.SetTransientData("foo", data); err != nil {
		t.Fatal(err)
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, "foo", data, "bar"); err != nil {
			t.Fatal(err)
		}
	}
	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientSet(msg, "foo", data, "bar"); err != nil {
			t.Fatal(err)
		}
	}

	if err := client1.RemoveTransientData("foo"); err != nil {
		t.Fatal(err)
	}

	if msg, err := client1.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientRemove(msg, "foo", data); err != nil {
			t.Fatal(err)
		}
	}
	if msg, err := client2.RunUntilMessage(ctx); err != nil {
		t.Fatal(err)
	} else {
		if err := checkMessageTransientRemove(msg, "foo", data); err != nil {
			t.Fatal(err)
		}
	}

	// Removing a non-existing key is ignored by the server.
	if err := client1.RemoveTransientData("foo"); err != nil {
		t.Fatal(err)
	}
	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()

	if msg, err := client1.RunUntilMessage(ctx3); err != nil {
		if err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	} else {
		t.Errorf("Expected no payload, got %+v", msg)
	}

	if err := client1.SetTransientData("abc", data); err != nil {
		t.Fatal(err)
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

	if room, err := client3.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	_, ignored, err := client3.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello, hello3.Hello)
	if err != nil {
		t.Fatal(err)
	}

	var msg *ServerMessage
	if len(ignored) == 0 {
		if msg, err = client3.RunUntilMessage(ctx); err != nil {
			t.Fatal(err)
		}
	} else if len(ignored) == 1 {
		msg = ignored[0]
	} else {
		t.Fatalf("Received too many messages: %+v", ignored)
	}

	if err := checkMessageTransientInitial(msg, map[string]interface{}{
		"abc": data,
	}); err != nil {
		t.Fatal(err)
	}
}
