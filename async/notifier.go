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
	"time"
)

const notifiedKeyTTL = 30 * time.Second

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
	// +checklocks:Mutex
	// Sticky: remembers recent notifications so late waiters don't miss them.
	notifiedAt map[string]time.Time
}

type ReleaseFunc func()

func (n *Notifier) NewWaiter(key string) (*Waiter, ReleaseFunc) {
	n.Lock()
	defer n.Unlock()

	// If this key was notified recently, return an already-closed channel
	// so the caller retries immediately without waiting.
	if t, ok := n.notifiedAt[key]; ok && time.Since(t) < notifiedKeyTTL {
		ch := make(chan struct{})
		close(ch)
		w := &Waiter{key: key, ch: ch}
		return w, func() {}
	}

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
	n.notifiedAt = nil
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

	if n.notifiedAt == nil {
		n.notifiedAt = make(map[string]time.Time)
	}
	n.notifiedAt[key] = time.Now()

	if w, found := n.waiters[key]; found {
		w.notify()
		delete(n.waiters, w.key)
		delete(n.waiterMap, w.key)
	}
}
