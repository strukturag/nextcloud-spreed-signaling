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
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrDuplicateSignaler = errors.New("duplicate signaler") // +checklocksignore: Global readonly variable.
	ErrAlreadyReleased   = errors.New("already released")
)

type notifierEntry struct {
	sync.Mutex

	ref atomic.Int32
	key string
	ch  chan struct{}

	// +checklocks:Mutex
	signaler *Signaler
	// +checklocks:Mutex
	waiter map[*Waiter]bool
}

func (w *notifierEntry) setSignaler(signaler *Signaler) {
	w.Lock()
	defer w.Unlock()

	if w.signaler != nil {
		panic(ErrDuplicateSignaler)
	}

	w.signaler = signaler
}

func (w *notifierEntry) clearSignaler(signaler *Signaler) {
	w.Lock()
	defer w.Unlock()

	if w.signaler != signaler {
		panic("unknown signaler")
	}

	w.signaler = nil
}

func (w *notifierEntry) addWaiter(waiter *Waiter) {
	w.Lock()
	defer w.Unlock()

	w.waiter[waiter] = true
}

func (w *notifierEntry) removeWaiter(waiter *Waiter) {
	w.Lock()
	defer w.Unlock()

	delete(w.waiter, waiter)
}

func (w *notifierEntry) notify() {
	select {
	case <-w.ch:
		// Already closed
	default:
		close(w.ch)
	}
}

type Waiter struct {
	entry atomic.Pointer[notifierEntry]
}

func (w *Waiter) Wait(ctx context.Context) error {
	entry := w.entry.Load()
	if entry == nil {
		return nil
	}

	select {
	case <-entry.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Signaler struct {
	entry atomic.Pointer[notifierEntry]
}

func (s *Signaler) Signal() {
	if entry := s.entry.Load(); entry != nil {
		entry.notify()
	}
}

type Notifier struct {
	sync.Mutex

	// +checklocks:Mutex
	waiters map[string]*notifierEntry
}

type ReleaseFunc func()

func (n *Notifier) createEntry(key string) *notifierEntry {
	n.Lock()
	defer n.Unlock()

	waiter, found := n.waiters[key]
	if !found {
		waiter = &notifierEntry{
			key:    key,
			ch:     make(chan struct{}),
			waiter: make(map[*Waiter]bool),
		}

		if n.waiters == nil {
			n.waiters = make(map[string]*notifierEntry)
		}
		n.waiters[key] = waiter
	}

	waiter.ref.Add(1)
	return waiter
}

func (n *Notifier) NewSignaler(key string) (*Signaler, ReleaseFunc) {
	entry := n.createEntry(key)

	s := &Signaler{}
	s.entry.Store(entry)
	entry.setSignaler(s)
	releaseFunc := func() {
		n.releaseSignaler(s)
	}
	return s, releaseFunc
}

func (n *Notifier) NewWaiter(key string) (*Waiter, ReleaseFunc) {
	entry := n.createEntry(key)

	w := &Waiter{}
	w.entry.Store(entry)
	entry.addWaiter(w)
	releaseFunc := func() {
		n.releaseWaiter(w)
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
}

func (n *Notifier) releaseSignaler(s *Signaler) {
	entry := s.entry.Swap(nil)
	if entry == nil {
		return
	}

	entry.clearSignaler(s)
	if entry.ref.Add(-1) == 0 {
		n.Lock()
		defer n.Unlock()

		if e, found := n.waiters[entry.key]; found {
			e.notify()
			delete(n.waiters, e.key)
		}
	}
}
func (n *Notifier) releaseWaiter(w *Waiter) {
	entry := w.entry.Swap(nil)
	if entry == nil {
		return
	}

	entry.removeWaiter(w)
	if entry.ref.Add(-1) == 0 {
		n.Lock()
		defer n.Unlock()

		if e, found := n.waiters[entry.key]; found {
			e.notify()
			delete(n.waiters, e.key)
		}
	}
}
