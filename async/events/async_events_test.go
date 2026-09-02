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

type TestBackendRoomListener struct {
	events AsyncChannel
}

func (l *TestBackendRoomListener) AsyncChannel() AsyncChannel {
	return l.events
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

	listener := &TestBackendRoomListener{
		events: make(AsyncChannel, 1),
	}
	listener2 := &TestBackendRoomListener{
		events: make(AsyncChannel, 1),
	}
	slowListener := &TestBackendRoomListener{
		events: make(AsyncChannel),
	}

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
	if assert.NoError(events.PublishBackendRoomMessage(roomId, backend, msg)) {
		received := <-listener.events
		var receivedMsg AsyncMessage
		if assert.NoError(nats.Decode(received, &receivedMsg)) {
			assert.True(msg.SendTime.Equal(receivedMsg.SendTime), "send times don't match, expected %s, got %s", msg.SendTime, receivedMsg.SendTime)
			receivedMsg.SendTime = msg.SendTime
			assert.Equal(msg, &receivedMsg)
		}

		received2 := <-listener2.events
		var received2Msg AsyncMessage
		if assert.NoError(nats.Decode(received2, &received2Msg)) {
			assert.True(msg.SendTime.Equal(received2Msg.SendTime), "send times don't match, expected %s, got %s", msg.SendTime, received2Msg.SendTime)
			received2Msg.SendTime = msg.SendTime
			assert.Equal(msg, &received2Msg)
		}
		select {
		case msg := <-slowListener.events:
			assert.Fail("should not have received message", "got %+v", msg)
		default:
			// Expected, the slow listener was skipped.
		}
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
		received := <-listener.events
		var receivedMsg AsyncMessage
		if assert.NoError(nats.Decode(received, &receivedMsg)) {
			assert.True(roomMessage.SendTime.Equal(receivedMsg.SendTime), "send times don't match, expected %s, got %s", roomMessage.SendTime, receivedMsg.SendTime)
			receivedMsg.SendTime = roomMessage.SendTime
			assert.Equal(roomMessage, &receivedMsg)
		}

		received2 := <-listener2.events
		var received2Msg AsyncMessage
		if assert.NoError(nats.Decode(received2, &received2Msg)) {
			assert.True(roomMessage.SendTime.Equal(received2Msg.SendTime), "send times don't match, expected %s, got %s", roomMessage.SendTime, received2Msg.SendTime)
			received2Msg.SendTime = roomMessage.SendTime
			assert.Equal(roomMessage, &received2Msg)
		}
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
		received := <-listener.events
		var receivedMsg AsyncMessage
		if assert.NoError(nats.Decode(received, &receivedMsg)) {
			assert.True(userMessage.SendTime.Equal(receivedMsg.SendTime), "send times don't match, expected %s, got %s", userMessage.SendTime, receivedMsg.SendTime)
			receivedMsg.SendTime = userMessage.SendTime
			assert.Equal(userMessage, &receivedMsg)
		}
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
		received := <-listener.events
		var receivedMsg AsyncMessage
		if assert.NoError(nats.Decode(received, &receivedMsg)) {
			assert.True(sessionMessage.SendTime.Equal(receivedMsg.SendTime), "send times don't match, expected %s, got %s", sessionMessage.SendTime, receivedMsg.SendTime)
			receivedMsg.SendTime = sessionMessage.SendTime
			assert.Equal(sessionMessage, &receivedMsg)
		}
	}

	// Will get cleaned up on close.
	listener3 := &TestBackendRoomListener{
		events: make(AsyncChannel, 1),
	}
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
