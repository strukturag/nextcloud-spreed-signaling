/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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

type SingleWaiter struct {
	root bool
	ch   chan struct{}
	once sync.Once
}

func newSingleWaiter() *SingleWaiter {
	return &SingleWaiter{
		root: true,
		ch:   make(chan struct{}),
	}
}

func (w *SingleWaiter) subWaiter() *SingleWaiter {
	return &SingleWaiter{
		ch: w.ch,
	}
}

func (w *SingleWaiter) Wait(ctx context.Context) error {
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *SingleWaiter) cancel() {
	if !w.root {
		return
	}

	w.once.Do(func() {
		close(w.ch)
	})
}

type SingleNotifier struct {
	sync.Mutex

	// +checklocks:Mutex
	waiter *SingleWaiter
	// +checklocks:Mutex
	waiters map[*SingleWaiter]bool
}

func (n *SingleNotifier) NewWaiter() *SingleWaiter {
	n.Lock()
	defer n.Unlock()

	if n.waiter == nil {
		n.waiter = newSingleWaiter()
	}

	if n.waiters == nil {
		n.waiters = make(map[*SingleWaiter]bool)
	}

	w := n.waiter.subWaiter()
	n.waiters[w] = true
	return w
}

func (n *SingleNotifier) Reset() {
	n.Lock()
	defer n.Unlock()

	if n.waiter != nil {
		n.waiter.cancel()
		n.waiter = nil
	}
	n.waiters = nil
}

func (n *SingleNotifier) Release(w *SingleWaiter) {
	n.Lock()
	defer n.Unlock()

	if _, found := n.waiters[w]; found {
		delete(n.waiters, w)
		if len(n.waiters) == 0 {
			n.waiters = nil
			if n.waiter != nil {
				n.waiter.cancel()
				n.waiter = nil
			}
		}
	}
}

func (n *SingleNotifier) Notify() {
	n.Lock()
	defer n.Unlock()

	if n.waiter != nil {
		n.waiter.cancel()
	}
	n.waiters = nil
}
