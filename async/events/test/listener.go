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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/async/events"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/nats"
)

// Listener is an events.AsyncEventListener that queues received messages
// until they are fetched by the test.
type Listener struct {
	t        *testing.T
	queue    *events.AsyncQueue
	messages chan *nats.Msg
}

func NewListener(t *testing.T) *Listener {
	t.Helper()
	listener := &Listener{
		t:        t,
		queue:    events.NewAsyncQueue(logtest.NewLoggerForTest(t), "test listener"),
		messages: make(chan *nats.Msg),
	}

	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
	})
	go listener.queue.Run(done, func(msg *nats.Msg) {
		select {
		case listener.messages <- msg:
		case <-done:
		}
	})
	return listener
}

func (l *Listener) AsyncEventReceived(msg *nats.Msg) {
	l.queue.Push(msg)
}

// WaitForMessage returns the next received message.
func (l *Listener) WaitForMessage(ctx context.Context) (*events.AsyncMessage, bool) {
	l.t.Helper()
	select {
	case msg := <-l.messages:
		var message events.AsyncMessage
		if !assert.NoError(l.t, nats.Decode(msg, &message)) {
			return nil, false
		}

		return &message, true
	case <-ctx.Done():
		assert.NoError(l.t, ctx.Err(), "no message received")
		return nil, false
	}
}

// AssertNoMessage checks that no message is received within the timeout.
func (l *Listener) AssertNoMessage(timeout time.Duration) bool {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(l.t.Context(), timeout)
	defer cancel()

	select {
	case msg := <-l.messages:
		return assert.Fail(l.t, "received unexpected message", "message: %s", string(msg.Data))
	case <-ctx.Done():
		return true
	}
}
