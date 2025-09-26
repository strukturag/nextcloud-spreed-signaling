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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
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

type MockTransientListener struct {
	mu      sync.Mutex
	sending chan struct{}
	done    chan struct{}

	data *TransientData
}

func (l *MockTransientListener) SendMessage(message *ServerMessage) bool {
	close(l.sending)

	time.Sleep(10 * time.Millisecond)

	l.mu.Lock()
	defer l.mu.Unlock()
	defer close(l.done)

	time.Sleep(10 * time.Millisecond)

	return true
}

func (l *MockTransientListener) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.data.RemoveListener(l)
}

func Test_TransientDataDeadlock(t *testing.T) {
	data := NewTransientData()

	listener := &MockTransientListener{
		sending: make(chan struct{}),
		done:    make(chan struct{}),

		data: data,
	}
	data.AddListener(listener)

	go func() {
		<-listener.sending
		listener.Close()
	}()

	data.Set("foo", "bar")
	<-listener.done
}

func Test_TransientMessages(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")

	require.NoError(client1.SetTransientData("foo", "bar", 0))
	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageError(t, msg, "not_in_room")
	}

	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
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

	require.NoError(client1.SetTransientData("abc", data, 10*time.Millisecond))

	client3, hello3 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"3")
	roomMsg = MustSucceed2(t, client3.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	_, ignored, ok := client3.RunUntilJoinedAndReturn(ctx, hello1.Hello, hello2.Hello, hello3.Hello)
	require.True(ok)

	var msg *ServerMessage
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

	time.Sleep(10 * time.Millisecond)
	if msg, ok = client3.RunUntilMessage(ctx); ok {
		checkMessageTransientRemove(t, msg, "abc", data)
	}
}
