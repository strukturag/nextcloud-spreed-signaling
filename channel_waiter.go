/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
	"sync"
)

type ChannelWaiters struct {
	mu      sync.RWMutex
	id      uint64
	waiters map[uint64]chan struct{}
}

func (w *ChannelWaiters) Wakeup() {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, ch := range w.waiters {
		select {
		case ch <- struct{}{}:
		default:
			// Receiver is still processing previous wakeup.
		}
	}
}

func (w *ChannelWaiters) Add(ch chan struct{}) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.waiters == nil {
		w.waiters = make(map[uint64]chan struct{})
	}
	id := w.id
	w.id++
	w.waiters[id] = ch
	return id
}

func (w *ChannelWaiters) Remove(id uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.waiters, id)
}
