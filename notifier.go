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
)

type Waiter struct {
	key string
	ch  chan bool
}

func (w *Waiter) Wait(ctx context.Context) error {
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Notifier struct {
	sync.Mutex

	waiters map[string]*Waiter
}

func (n *Notifier) NewWaiter(key string) *Waiter {
	n.Lock()
	defer n.Unlock()

	_, found := n.waiters[key]
	if found {
		panic("already waiting")
	}

	waiter := &Waiter{
		key: key,
		ch:  make(chan bool, 1),
	}
	if n.waiters == nil {
		n.waiters = make(map[string]*Waiter)
	}
	n.waiters[key] = waiter
	return waiter
}

func (n *Notifier) Reset() {
	n.Lock()
	defer n.Unlock()

	for _, w := range n.waiters {
		close(w.ch)
	}
	n.waiters = nil
}

func (n *Notifier) Release(w *Waiter) {
	n.Lock()
	defer n.Unlock()

	if _, found := n.waiters[w.key]; found {
		delete(n.waiters, w.key)
		close(w.ch)
	}
}

func (n *Notifier) Notify(key string) {
	n.Lock()
	defer n.Unlock()

	if w, found := n.waiters[key]; found {
		select {
		case w.ch <- true:
		default:
			// Ignore, already notified
		}
	}
}
