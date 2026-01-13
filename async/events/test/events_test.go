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
package test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type testListener struct {
	ch events.AsyncChannel
}

func (l *testListener) AsyncChannel() events.AsyncChannel {
	return l.ch
}

func TestAsyncEventsTest(t *testing.T) {
	t.Parallel()

	for _, backend := range EventBackendsForTest {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()

			require := require.New(t)
			assert := assert.New(t)

			ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
			defer cancel()

			eventsHandler := GetAsyncEventsForTest(t)

			listener := &testListener{
				ch: make(events.AsyncChannel, 1),
			}
			sessionId := api.PublicSessionId("foo")
			backend := talk.NewCompatBackend(nil)
			require.NoError(eventsHandler.RegisterSessionListener(sessionId, backend, listener))
			defer func() {
				assert.NoError(eventsHandler.UnregisterSessionListener(sessionId, backend, listener))
			}()

			msg := events.AsyncMessage{
				Type: "message",
				Message: &api.ServerMessage{
					Type:  "error",
					Error: api.NewError("test_error", "This is a test error."),
				},
			}
			if err := eventsHandler.PublishSessionMessage(sessionId, backend, &msg); assert.NoError(err) {
				select {
				case natsMsg := <-listener.ch:
					var received events.AsyncMessage
					if err := nats.Decode(natsMsg, &received); assert.NoError(err) {
						assert.True(msg.SendTime.Equal(received.SendTime), "send times don't match, expected %s, got %s", msg.SendTime, received.SendTime)
						received.SendTime = msg.SendTime
						assert.Equal(msg, received)
					}
				case <-ctx.Done():
					require.NoError(ctx.Err())
				}
			}

			WaitForAsyncEventsFlushed(ctx, t, eventsHandler)
		})
	}
}
