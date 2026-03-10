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
package async

import (
	"context"
	"sync"
)

type rootWaiter struct {
	key string
	ch  chan struct{}
}

func (w *rootWaiter) notify() {
	close(w.ch)
}

type Waiter struct {
	key string
	ch  <-chan struct{}
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

	// +checklocks:Mutex
	waiters map[string]*rootWaiter
	// +checklocks:Mutex
	waiterMap map[string]map[*Waiter]bool
}

type ReleaseFunc func()

func (n *Notifier) NewWaiter(key string) (*Waiter, ReleaseFunc) {
	n.Lock()
	defer n.Unlock()

	waiter, found := n.waiters[key]
	if !found {
		waiter = &rootWaiter{
			key: key,
			ch:  make(chan struct{}),
		}

		if n.waiters == nil {
			n.waiters = make(map[string]*rootWaiter)
		}
		if n.waiterMap == nil {
			n.waiterMap = make(map[string]map[*Waiter]bool)
		}
		n.waiters[key] = waiter
		if _, found := n.waiterMap[key]; !found {
			n.waiterMap[key] = make(map[*Waiter]bool)
		}
	}

	w := &Waiter{
		key: key,
		ch:  waiter.ch,
	}
	n.waiterMap[key][w] = true
	releaseFunc := func() {
		n.release(w)
	}
	return w, releaseFunc
}

func (n *Notifier) Reset() {
	n.Lock()
	defer n.Unlock()

	for _, w := range n.waiters {
		w.notify()
	}
	n.waiters = nil
	n.waiterMap = nil
}

func (n *Notifier) release(w *Waiter) {
	n.Lock()
	defer n.Unlock()

	if waiters, found := n.waiterMap[w.key]; found {
		if _, found := waiters[w]; found {
			delete(waiters, w)
			if len(waiters) == 0 {
				if root, found := n.waiters[w.key]; found {
					delete(n.waiters, w.key)
					root.notify()
				}
			}
		}
	}
}

func (n *Notifier) Notify(key string) {
	n.Lock()
	defer n.Unlock()

	if w, found := n.waiters[key]; found {
		w.notify()
		delete(n.waiters, w.key)
		delete(n.waiterMap, w.key)
	}
}
