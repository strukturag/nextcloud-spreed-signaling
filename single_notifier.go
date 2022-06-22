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
package signaling

import (
	"context"
	"sync"
)

type SingleWaiter struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *SingleWaiter) Wait(ctx context.Context) error {
	select {
	case <-w.ctx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type SingleNotifier struct {
	sync.Mutex

	waiter  *SingleWaiter
	waiters map[*SingleWaiter]bool
}

func (n *SingleNotifier) NewWaiter() *SingleWaiter {
	n.Lock()
	defer n.Unlock()

	if n.waiter == nil {
		ctx, cancel := context.WithCancel(context.Background())
		n.waiter = &SingleWaiter{
			ctx:    ctx,
			cancel: cancel,
		}
	}

	if n.waiters == nil {
		n.waiters = make(map[*SingleWaiter]bool)
	}

	w := &SingleWaiter{
		ctx:    n.waiter.ctx,
		cancel: n.waiter.cancel,
	}
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
