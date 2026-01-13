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

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	natstest "github.com/strukturag/nextcloud-spreed-signaling/nats/test"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
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

		assert.NoError(events.Close(ctx))
	})

	listener := &TestBackendRoomListener{
		events: make(AsyncChannel, 1),
	}

	roomId := "1234"
	backend := talk.NewCompatBackend(nil)
	require.NoError(events.RegisterBackendRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterBackendRoomListener(roomId, backend, listener))
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
	}

	require.NoError(events.RegisterRoomListener(roomId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterRoomListener(roomId, backend, listener))
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
	}

	userId := "the-user"
	require.NoError(events.RegisterUserListener(userId, backend, listener))
	defer func() {
		assert.NoError(events.UnregisterUserListener(userId, backend, listener))
	}()

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
