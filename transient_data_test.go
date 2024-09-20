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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (t *TransientData) SetTTLChannel(ch chan<- struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ttlCh = ch
}

func Test_TransientData(t *testing.T) {
	assert := assert.New(t)
	data := NewTransientData()
	assert.False(data.Set("foo", nil))
	assert.True(data.Set("foo", "bar"))
	assert.False(data.Set("foo", "bar"))
	assert.True(data.Set("foo", "baz"))
	assert.False(data.CompareAndSet("foo", "bar", "lala"))
	assert.True(data.CompareAndSet("foo", "baz", "lala"))
	assert.False(data.CompareAndSet("test", nil, nil))
	assert.True(data.CompareAndSet("test", nil, "123"))
	assert.False(data.CompareAndSet("test", nil, "456"))
	assert.False(data.CompareAndRemove("test", "1234"))
	assert.True(data.CompareAndRemove("test", "123"))
	assert.False(data.Remove("lala"))
	assert.True(data.Remove("foo"))

	ttlCh := make(chan struct{})
	data.SetTTLChannel(ttlCh)
	assert.True(data.SetTTL("test", "1234", time.Millisecond))
	assert.Equal("1234", data.GetData()["test"])
	// Data is removed after the TTL
	<-ttlCh
	assert.Nil(data.GetData()["test"])

	assert.True(data.SetTTL("test", "1234", time.Millisecond))
	assert.Equal("1234", data.GetData()["test"])
	assert.True(data.SetTTL("test", "2345", 3*time.Millisecond))
	assert.Equal("2345", data.GetData()["test"])
	// Data is removed after the TTL only if the value still matches
	time.Sleep(2 * time.Millisecond)
	assert.Equal("2345", data.GetData()["test"])
	// Data is removed after the (second) TTL
	<-ttlCh
	assert.Nil(data.GetData()["test"])

	// Setting existing key will update the TTL
	assert.True(data.SetTTL("test", "1234", time.Millisecond))
	assert.False(data.SetTTL("test", "1234", 3*time.Millisecond))
	// Data still exists after the first TTL
	time.Sleep(2 * time.Millisecond)
	assert.Equal("1234", data.GetData()["test"])
	// Data is removed after the (updated) TTL
	<-ttlCh
	assert.Nil(data.GetData()["test"])
}

func Test_TransientMessages(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewTestClient(t, server, hub)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHello(testDefaultUserId + "1"))
	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	require.NoError(client1.SetTransientData("foo", "bar", 0))
	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageError(msg, "not_in_room"))
	}

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

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg, err = client2.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	session1 := hub.GetSessionByPublicId(hello1.Hello.SessionId).(*ClientSession)
	require.NotNil(session1, "Session %s does not exist", hello1.Hello.SessionId)
	session2 := hub.GetSessionByPublicId(hello2.Hello.SessionId).(*ClientSession)
	require.NotNil(session2, "Session %s does not exist", hello2.Hello.SessionId)

	// Client 1 may modify transient data.
	session1.SetPermissions([]Permission{PERMISSION_TRANSIENT_DATA})
	// Client 2 may not modify transient data.
	session2.SetPermissions([]Permission{})

	require.NoError(client2.SetTransientData("foo", "bar", 0))
	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageError(msg, "not_allowed"))
	}

	require.NoError(client1.SetTransientData("foo", "bar", 0))

	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientSet(msg, "foo", "bar", nil))
	}
	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientSet(msg, "foo", "bar", nil))
	}

	require.NoError(client2.RemoveTransientData("foo"))
	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageError(msg, "not_allowed"))
	}

	// Setting the same value is ignored by the server.
	require.NoError(client1.SetTransientData("foo", "bar", 0))
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	if msg, err := client1.RunUntilMessage(ctx2); err == nil {
		assert.Fail("Expected no payload, got %+v", msg)
	} else {
		require.ErrorIs(err, context.DeadlineExceeded)
	}

	data := map[string]interface{}{
		"hello": "world",
	}
	require.NoError(client1.SetTransientData("foo", data, 0))

	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientSet(msg, "foo", data, "bar"))
	}
	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientSet(msg, "foo", data, "bar"))
	}

	require.NoError(client1.RemoveTransientData("foo"))

	if msg, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientRemove(msg, "foo", data))
	}
	if msg, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientRemove(msg, "foo", data))
	}

	// Removing a non-existing key is ignored by the server.
	require.NoError(client1.RemoveTransientData("foo"))
	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()

	if msg, err := client1.RunUntilMessage(ctx3); err == nil {
		assert.Fail("Expected no payload, got %+v", msg)
	} else {
		require.ErrorIs(err, context.DeadlineExceeded)
	}

	require.NoError(client1.SetTransientData("abc", data, 10*time.Millisecond))

	client3 := NewTestClient(t, server, hub)
	defer client3.CloseWithBye()
	require.NoError(client3.SendHello(testDefaultUserId + "3"))
	hello3, err := client3.RunUntilHello(ctx)
	require.NoError(err)

	roomMsg, err = client3.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	_, ignored, err := client3.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello, hello3.Hello)
	require.NoError(err)

	var msg *ServerMessage
	if len(ignored) == 0 {
		msg, err = client3.RunUntilMessage(ctx)
		require.NoError(err)
	} else if len(ignored) == 1 {
		msg = ignored[0]
	} else {
		require.Fail("Received too many messages: %+v", ignored)
	}

	require.NoError(checkMessageTransientInitial(msg, map[string]interface{}{
		"abc": data,
	}))

	time.Sleep(10 * time.Millisecond)
	if msg, err = client3.RunUntilMessage(ctx); assert.NoError(err) {
		require.NoError(checkMessageTransientRemove(msg, "abc", data))
	}
}
