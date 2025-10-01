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

	sw *SingleWaiter
}

func (w *Waiter) Wait(ctx context.Context) error {
	return w.sw.Wait(ctx)
}

type Notifier struct {
	sync.Mutex

	// +checklocks:Mutex
	waiters map[string]*Waiter
	// +checklocks:Mutex
	waiterMap map[string]map[*Waiter]bool
}

func (n *Notifier) NewWaiter(key string) *Waiter {
	n.Lock()
	defer n.Unlock()

	waiter, found := n.waiters[key]
	if found {
		w := &Waiter{
			key: key,
			sw:  waiter.sw,
		}
		n.waiterMap[key][w] = true
		return w
	}

	waiter = &Waiter{
		key: key,
		sw:  newSingleWaiter(),
	}
	if n.waiters == nil {
		n.waiters = make(map[string]*Waiter)
	}
	if n.waiterMap == nil {
		n.waiterMap = make(map[string]map[*Waiter]bool)
	}
	n.waiters[key] = waiter
	if _, found := n.waiterMap[key]; !found {
		n.waiterMap[key] = make(map[*Waiter]bool)
	}
	n.waiterMap[key][waiter] = true
	return waiter
}

func (n *Notifier) Reset() {
	n.Lock()
	defer n.Unlock()

	for _, w := range n.waiters {
		w.sw.cancel()
	}
	n.waiters = nil
	n.waiterMap = nil
}

func (n *Notifier) Release(w *Waiter) {
	n.Lock()
	defer n.Unlock()

	if waiters, found := n.waiterMap[w.key]; found {
		if _, found := waiters[w]; found {
			delete(waiters, w)
			if len(waiters) == 0 {
				delete(n.waiters, w.key)
				w.sw.cancel()
			}
		}
	}
}

func (n *Notifier) Notify(key string) {
	n.Lock()
	defer n.Unlock()

	if w, found := n.waiters[key]; found {
		w.sw.cancel()
		delete(n.waiters, w.key)
		delete(n.waiterMap, w.key)
	}
}
