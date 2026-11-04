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
package events

import (
	"sync"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/nats"
)

const (
	// Log a warning if more messages are pending.
	warnAsyncQueuePendingCount = 1024
)

// AsyncQueue buffers received messages and passes them to a handler in the
// order they were received. Messages don't get dropped if the handler is slow.
type AsyncQueue struct {
	logger log.Logger    // +checklocksignore: Only written to from constructor.
	name   string        // +checklocksignore: Only written to from constructor.
	notify chan struct{} // +checklocksignore: Only written to from constructor.

	mu sync.Mutex
	// +checklocks:mu
	pending []*nats.Msg
	// +checklocks:mu
	warned bool
}

func NewAsyncQueue(logger log.Logger, name string) *AsyncQueue {
	return &AsyncQueue{
		logger: logger,
		name:   name,
		notify: make(chan struct{}, 1),
	}
}

// Push adds a message to the queue, it doesn't block.
func (q *AsyncQueue) Push(msg *nats.Msg) {
	q.mu.Lock()
	q.pending = append(q.pending, msg)
	if count := len(q.pending); count >= warnAsyncQueuePendingCount && !q.warned {
		q.warned = true
		q.logger.Printf("Queue of %s has %d pending messages", q.name, count)
	}
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// Run passes queued messages to the handler until "done" is closed.
func (q *AsyncQueue) Run(done <-chan struct{}, handler func(msg *nats.Msg)) {
	for {
		select {
		case <-done:
			return
		case <-q.notify:
		}

		for msgs := q.takePending(); len(msgs) > 0; msgs = q.takePending() {
			for _, msg := range msgs {
				select {
				case <-done:
					return
				default:
				}

				handler(msg)
			}
		}
	}
}

func (q *AsyncQueue) takePending() []*nats.Msg {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := q.pending
	q.pending = nil
	if len(result) < warnAsyncQueuePendingCount {
		q.warned = false
	}
	return result
}
