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
	// Log a warning if more messages are pending (and again whenever the
	// number of pending messages doubled).
	warnAsyncQueuePendingCount = 1024

	// Maximum number of pending messages, further messages are dropped. This
	// is only reached if the handler is stalled.
	maxAsyncQueuePendingCount = 65536
)

type asyncQueueEntry struct {
	msg *nats.Msg
	// Executed instead of the handler (if set).
	fn func()
}

// AsyncQueue buffers received messages and passes them to a handler in the
// order they were received. Messages don't get dropped if the handler is slow.
type AsyncQueue struct {
	logger log.Logger    // +checklocksignore: Only written to from constructor.
	name   string        // +checklocksignore: Only written to from constructor.
	notify chan struct{} // +checklocksignore: Only written to from constructor.

	// Only modified before the queue is used.
	maxPending int // +checklocksignore

	mu sync.Mutex
	// +checklocks:mu
	pending []asyncQueueEntry
	// +checklocks:mu
	nextWarnCount int
	// +checklocks:mu
	dropped int
}

func NewAsyncQueue(logger log.Logger, name string) *AsyncQueue {
	return &AsyncQueue{
		logger: logger,
		name:   name,
		notify: make(chan struct{}, 1),

		maxPending:    maxAsyncQueuePendingCount,
		nextWarnCount: warnAsyncQueuePendingCount,
	}
}

// Push adds a message to the queue, it doesn't block.
func (q *AsyncQueue) Push(msg *nats.Msg) {
	q.push(asyncQueueEntry{
		msg: msg,
	})
}

// Barrier returns a channel that is closed once all messages that were added
// before have been processed. The channel is not closed if the queue is
// stopped before.
func (q *AsyncQueue) Barrier() <-chan struct{} {
	barrier := make(chan struct{})
	q.Execute(func() {
		close(barrier)
	})
	return barrier
}

// Execute runs a function from the goroutine processing the messages once all
// messages that were added before have been processed. The function is not
// executed if the queue is stopped before.
func (q *AsyncQueue) Execute(fn func()) {
	q.push(asyncQueueEntry{
		fn: fn,
	})
}

func (q *AsyncQueue) push(entry asyncQueueEntry) {
	q.mu.Lock()
	// Functions are never dropped, they are only used internally.
	if entry.fn == nil && len(q.pending) >= q.maxPending {
		q.dropped++
		if q.dropped&(q.dropped-1) == 0 {
			// Only log for every power of two to not flood the log.
			q.logger.Printf("Queue of %s is full with %d pending messages, dropped %d messages", q.name, len(q.pending), q.dropped)
		}
		q.mu.Unlock()
		return
	}

	q.pending = append(q.pending, entry)
	if count := len(q.pending); count >= q.nextWarnCount {
		q.logger.Printf("Queue of %s has %d pending messages", q.name, count)
		q.nextWarnCount *= 2
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

		for entries := q.takePending(); len(entries) > 0; entries = q.takePending() {
			for _, entry := range entries {
				select {
				case <-done:
					return
				default:
				}

				if entry.fn != nil {
					entry.fn()
					continue
				}

				handler(entry.msg)
			}
		}
	}
}

func (q *AsyncQueue) takePending() []asyncQueueEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := q.pending
	q.pending = nil
	q.nextWarnCount = warnAsyncQueuePendingCount
	q.dropped = 0
	return result
}
