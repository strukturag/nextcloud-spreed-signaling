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
	"sync/atomic"
)

type notifierEntry struct {
	key string
	ch  chan struct{}
	// Set once "ch" is closed.
	notified atomic.Bool

	// Signalers and number of waiters using the entry.
	signalers map[*Signaler]bool // +checklocksignore: Only accessed while holding the lock of the notifier.
	waiters   int                // +checklocksignore: Only accessed while holding the lock of the notifier.
}

func newNotifierEntry(key string) *notifierEntry {
	return &notifierEntry{
		key:       key,
		ch:        make(chan struct{}),
		signalers: make(map[*Signaler]bool),
	}
}

func (e *notifierEntry) isNotified() bool {
	return e.notified.Load()
}

func (e *notifierEntry) notify() {
	// Can be called concurrently (e.g. by multiple signalers), so make sure the
	// channel is only closed once.
	if e.notified.CompareAndSwap(false, true) {
		close(e.ch)
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
	entries map[string]*notifierEntry
}

type ReleaseFunc func()

// +checklocks:n.Mutex
func (n *Notifier) getEntryLocked(key string, forSignaler bool) *notifierEntry {
	entry, found := n.entries[key]
	if !found || (forSignaler && entry.isNotified()) {
		// If the entry was notified already (e.g. by a previous signaler for the
		// same key that is still being closed), a new entry is used so waiters
		// for the new signaler don't get notified by the previous signaler.
		// Waiters of the previous entry were notified already.
		entry = newNotifierEntry(key)
		if n.entries == nil {
			n.entries = make(map[string]*notifierEntry)
		}
		n.entries[key] = entry
	}
	return entry
}

// NewSignaler creates a signaler for the given key. Multiple signalers can
// exist for the same key, e.g. if a new publisher is created while a previous
// one is still being closed.
func (n *Notifier) NewSignaler(key string) (*Signaler, ReleaseFunc) {
	n.Lock()
	defer n.Unlock()

	entry := n.getEntryLocked(key, true)
	s := &Signaler{}
	s.entry.Store(entry)
	entry.signalers[s] = true
	return s, func() {
		n.releaseSignaler(s)
	}
}

func (n *Notifier) NewWaiter(key string) (*Waiter, ReleaseFunc) {
	n.Lock()
	defer n.Unlock()

	entry := n.getEntryLocked(key, false)
	w := &Waiter{}
	w.entry.Store(entry)
	entry.waiters++
	return w, func() {
		n.releaseWaiter(w)
	}
}

// Reset notifies all current waiters. Existing signalers are kept and can
// notify waiters that are created afterwards, e.g. if a publisher connects
// again after the connection to the server was reset.
func (n *Notifier) Reset() {
	n.Lock()
	defer n.Unlock()

	var entries map[string]*notifierEntry
	for key, entry := range n.entries {
		entry.notify()
		if len(entry.signalers) == 0 {
			continue
		}

		// The waiters of the previous entry keep it until they are released.
		newEntry := newNotifierEntry(key)
		for s := range entry.signalers {
			s.entry.Store(newEntry)
			newEntry.signalers[s] = true
		}
		clear(entry.signalers)

		if entries == nil {
			entries = make(map[string]*notifierEntry)
		}
		entries[key] = newEntry
	}
	n.entries = entries
}

// releaseEntryLocked removes the entry from the notifier once it is no longer
// used by any signaler or waiter. The entry might have been replaced (or
// removed by "Reset") in the meantime.
//
// +checklocks:n.Mutex
func (n *Notifier) releaseEntryLocked(entry *notifierEntry) {
	if len(entry.signalers) > 0 || entry.waiters > 0 {
		return
	}

	entry.notify()
	if n.entries[entry.key] == entry {
		delete(n.entries, entry.key)
	}
}

func (n *Notifier) releaseSignaler(s *Signaler) {
	n.Lock()
	defer n.Unlock()

	entry := s.entry.Swap(nil)
	if entry == nil {
		return
	}

	delete(entry.signalers, s)
	n.releaseEntryLocked(entry)
}

func (n *Notifier) releaseWaiter(w *Waiter) {
	n.Lock()
	defer n.Unlock()

	entry := w.entry.Swap(nil)
	if entry == nil {
		return
	}

	entry.waiters--
	n.releaseEntryLocked(entry)
}
