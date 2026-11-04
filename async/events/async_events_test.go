/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/nats"
	natstest "github.com/strukturag/nextcloud-spreed-signaling/v2/nats/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/talk"
)

type testListener struct {
	t        *testing.T
	queue    *AsyncQueue
	messages chan *nats.Msg
	// If set, processing of messages is blocked until the channel is closed.
	blocked chan struct{}
}

func newTestListener(t *testing.T) *testListener {
	return startTestListener(t, nil)
}

// newBlockedTestListener creates a listener that doesn't process received
// messages until "release" is called.
func newBlockedTestListener(t *testing.T) *testListener {
	return startTestListener(t, make(chan struct{}))
}

func startTestListener(t *testing.T, blocked chan struct{}) *testListener {
	listener := &testListener{
		t:        t,
		queue:    NewAsyncQueue(logtest.NewLoggerForTest(t), "test listener"),
		messages: make(chan *nats.Msg),
		blocked:  blocked,
	}

	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
	})
	go listener.queue.Run(done, func(msg *nats.Msg) {
		if blocked != nil {
			select {
			case <-blocked:
			case <-done:
				return
			}
		}

		select {
		case listener.messages <- msg:
		case <-done:
		}
	})
	return listener
}

func (l *testListener) release() {
	close(l.blocked)
}

func (l *testListener) assertNoMessage(timeout time.Duration) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(l.t.Context(), timeout)
	defer cancel()

	select {
	case msg := <-l.messages:
		assert.Fail(l.t, "received unexpected message", "message: %s", string(msg.Data))
	case <-ctx.Done():
	}
}

func (l *testListener) AsyncEventReceived(msg *nats.Msg) {
	l.queue.Push(msg)
}

func (l *testListener) assertMessage(expected *AsyncMessage) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(l.t.Context(), time.Second)
	defer cancel()

	select {
	case msg := <-l.messages:
		var received AsyncMessage
		if assert.NoError(l.t, nats.Decode(msg, &received)) {
			assert.True(l.t, expected.SendTime.Equal(received.SendTime), "send times don't match, expected %s, got %s", expected.SendTime, received.SendTime)
			received.SendTime = expected.SendTime
			assert.Equal(l.t, expected, &received)
		}
	case <-ctx.Done():
		assert.NoError(l.t, ctx.Err(), "no message received")
	}
}

func testAsyncEvents(t *testing.T, events AsyncEvents) {
	require := require.New(t)
	assert := assert.New(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if ne, ok := events.(*asyncEventsNats); ok {
			ne.mu.Lock()
			assert.NotEmpty(ne.backendRoomSubscriptions)
			assert.NotEmpty(ne.roomSubscriptions)
			assert.NotEmpty(ne.userSubscriptions)
			assert.NotEmpty(ne.sessionSubscriptions)
			ne.mu.Unlock()
		}

		if assert.NoError(events.Close(ctx)) {
			if ne, ok := events.(*asyncEventsNats); ok {
				ne.mu.Lock()
				assert.Empty(ne.backendRoomSubscriptions)
				assert.Empty(ne.roomSubscriptions)
				assert.Empty(ne.userSubscriptions)
				assert.Empty(ne.sessionSubscriptions)
				ne.mu.Unlock()
			}
		}
	})

	listener := newTestListener(t)
	listener2 := newTestListener(t)
	slowListener := newBlockedTestListener(t)

	roomId := "1234"
	backend := talk.NewCompatBackend(nil)
	require.NoError(events.RegisterBackendRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterBackendRoomListener(roomId, backend, listener))
	}()
	assert.ErrorIs(events.RegisterBackendRoomListener(roomId, backend, listener), ErrAlreadyRegistered)
	require.NoError(events.RegisterBackendRoomListener(roomId, backend, listener2))
	defer func() {
		assert.NoError(events.UnregisterBackendRoomListener(roomId, backend, listener2))
	}()
	require.NoError(events.RegisterBackendRoomListener(roomId, backend, slowListener))
	defer func() {
		assert.NoError(events.UnregisterBackendRoomListener(roomId, backend, slowListener))
	}()

	msg := &AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "test",
		},
	}
	msg2 := &AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "test2",
		},
	}
	if assert.NoError(events.PublishBackendRoomMessage(roomId, backend, msg)) &&
		assert.NoError(events.PublishBackendRoomMessage(roomId, backend, msg2)) {
		// The slow listener is still blocked processing the first message, but
		// didn't prevent the other listeners from receiving both messages (the
		// second message can only be delivered once delivery of the first one
		// has completed for all listeners).
		listener.assertMessage(msg)
		listener.assertMessage(msg2)
		listener2.assertMessage(msg)
		listener2.assertMessage(msg2)
		slowListener.assertNoMessage(100 * time.Millisecond)

		// The slow listener gets the messages once processing continues, so
		// they were queued and not dropped.
		slowListener.release()
		slowListener.assertMessage(msg)
		slowListener.assertMessage(msg2)
	}

	require.NoError(events.RegisterRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterRoomListener(roomId, backend, listener))
	}()
	assert.ErrorIs(events.RegisterRoomListener(roomId, backend, listener), ErrAlreadyRegistered)
	require.NoError(events.RegisterRoomListener(roomId, backend, listener2))
	defer func() {
		assert.NoError(events.UnregisterRoomListener(roomId, backend, listener2))
	}()

	roomMessage := &AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "other-test",
		},
	}
	if assert.NoError(events.PublishRoomMessage(roomId, backend, roomMessage)) {
		listener.assertMessage(roomMessage)

		listener2.assertMessage(roomMessage)
	}

	userId := "the-user"
	require.NoError(events.RegisterUserListener(userId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterUserListener(userId, backend, listener))
	}()
	assert.ErrorIs(events.RegisterUserListener(userId, backend, listener), ErrAlreadyRegistered)

	userMessage := &AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "user-test",
		},
	}
	if assert.NoError(events.PublishUserMessage(userId, backend, userMessage)) {
		listener.assertMessage(userMessage)
	}

	sessionId := api.PublicSessionId("the-session")
	require.NoError(events.RegisterSessionListener(sessionId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterSessionListener(sessionId, backend, listener))
	}()
	assert.ErrorIs(events.RegisterSessionListener(sessionId, backend, listener), ErrAlreadyRegistered)

	sessionMessage := &AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "session-test",
		},
	}
	if assert.NoError(events.PublishSessionMessage(sessionId, backend, sessionMessage)) {
		listener.assertMessage(sessionMessage)
	}

	// Will get cleaned up on close.
	listener3 := newTestListener(t)
	assert.NoError(events.RegisterBackendRoomListener(roomId+"-other", backend, listener3))
	assert.NoError(events.RegisterRoomListener(roomId+"-other", backend, listener3))
	assert.NoError(events.RegisterUserListener(userId+"-other", backend, listener3))
	assert.NoError(events.RegisterSessionListener(sessionId+"-other", backend, listener3))
}

func TestAsyncEvents_Loopback(t *testing.T) {
	t.Parallel()

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	events, err := NewAsyncEvents(ctx, nats.LoopbackUrl)
	require.NoError(t, err)
	testAsyncEvents(t, events)
}

func TestAsyncEvents_NATS(t *testing.T) {
	t.Parallel()

	server, _ := natstest.StartLocalServer(t)
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	events, err := NewAsyncEvents(ctx, server.ClientURL())
	require.NoError(t, err)
	testAsyncEvents(t, events)
}
