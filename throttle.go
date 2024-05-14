/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"errors"
	"log"
	"sync"
	"time"
)

const (
	// By default, if more than 10 requests failed in 30 minutes, a bruteforce
	// attack is detected and the client will be blocked.

	// maxBruteforceAttempts specifies the number of failed requests that may
	// happen during "maxBruteforceDurationThreshold" until it is seen as
	// "bruteforce" attempt.
	maxBruteforceAttempts = 10

	// maxBruteforceDurationThreshold specifies the duration during which the number of
	// failed requests may not exceed "maxBruteforceAttempts" to be seen as
	// "bruteforce" attempt.
	maxBruteforceDurationThreshold = 30 * time.Minute

	// maxBruteforceAge specifies the age for which failed attempts are remembered.
	maxBruteforceAge = 12 * time.Hour

	// maxThrottleDelay specifies the maxium time to sleep for failed requests.
	maxThrottleDelay = 25 * time.Second
)

var (
	ErrBruteforceDetected = errors.New("bruteforce detected")
)

type ThrottleFunc func(ctx context.Context)

type Throttler interface {
	Close()

	CheckBruteforce(ctx context.Context, client string, action string) (ThrottleFunc, error)
}

type throttleEntry struct {
	ts time.Time
}

type memoryThrottler struct {
	getNow  func() time.Time
	doDelay func(context.Context, time.Duration)

	mu      sync.RWMutex
	clients map[string]map[string][]throttleEntry

	closer *Closer
}

func NewMemoryThrottler() (Throttler, error) {
	result := &memoryThrottler{
		getNow: time.Now,

		clients: make(map[string]map[string][]throttleEntry),

		closer: NewCloser(),
	}
	result.doDelay = result.delay
	go result.housekeeping()
	return result, nil
}

func intPow(n, m int) int {
	if m == 0 {
		return 1
	}

	result := n
	for i := 2; i <= m; i++ {
		result *= n
	}
	return result
}

func (t *memoryThrottler) getEntries(client string, action string) []throttleEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	actions := t.clients[client]
	if len(actions) == 0 {
		return nil
	}

	entries := actions[action]
	return entries
}

func (t *memoryThrottler) setEntries(client string, action string, entries []throttleEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()

	actions := t.clients[client]
	if len(actions) == 0 {
		if len(entries) == 0 {
			return
		}

		actions = make(map[string][]throttleEntry)
		t.clients[client] = actions
	}

	if len(entries) > 0 {
		actions[action] = entries
	} else {
		delete(actions, action)
		if len(actions) == 0 {
			delete(t.clients, client)
		}
	}
}

func (t *memoryThrottler) addEntry(client string, action string, entry throttleEntry) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	actions, found := t.clients[client]
	if !found {
		t.clients[client] = map[string][]throttleEntry{
			action: {
				entry,
			},
		}
		return 1
	}

	entries, found := actions[action]
	if !found {
		actions[action] = []throttleEntry{
			entry,
		}
		return 1
	}

	actions[action] = append(entries, entry)
	return len(entries) + 1
}

func (t *memoryThrottler) housekeeping() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for !t.closer.IsClosed() {
		select {
		case now := <-ticker.C:
			t.cleanup(now)
		case <-t.closer.C:
		}
	}
}

func (t *memoryThrottler) filterEntries(entries []throttleEntry, now time.Time) []throttleEntry {
	start := 0
	l := len(entries)
	delta := now.Sub(entries[start].ts)
	for delta > maxBruteforceAge {
		start++
		if start == l {
			break
		}
		delta = now.Sub(entries[start].ts)
	}

	if start == l {
		// No entries remaining, client is unknown.
		return nil
	}

	if start > 0 {
		entries = append([]throttleEntry{}, entries[start:]...)
	}
	return entries
}

func (t *memoryThrottler) cleanup(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for client, actions := range t.clients {
		for action, entries := range actions {
			newEntries := t.filterEntries(entries, now)
			if newl := len(newEntries); newl == 0 {
				delete(actions, action)
			} else if newl != len(entries) {
				actions[action] = newEntries
			}
		}

		if len(actions) == 0 {
			delete(t.clients, client)
		}
	}
}

func (t *memoryThrottler) Close() {
	t.closer.Close()
}

func (t *memoryThrottler) getDelay(count int) time.Duration {
	delay := time.Duration(100*intPow(2, count)) * time.Millisecond
	if delay > maxThrottleDelay {
		delay = maxThrottleDelay
	}
	return delay
}

func (t *memoryThrottler) CheckBruteforce(ctx context.Context, client string, action string) (ThrottleFunc, error) {
	now := t.getNow()
	doThrottle := func(ctx context.Context) {
		t.throttle(ctx, client, action, now)
	}

	entries := t.getEntries(client, action)
	l := len(entries)
	if l == 0 {
		return doThrottle, nil
	}

	if l >= maxBruteforceAttempts {
		delta := now.Sub(entries[l-maxBruteforceAttempts].ts)
		if delta <= maxBruteforceDurationThreshold {
			log.Printf("Detected bruteforce attempt on \"%s\" from %s", action, client)
			return doThrottle, ErrBruteforceDetected
		}
	}

	// Remove old entries.
	newEntries := t.filterEntries(entries, now)
	if newl := len(newEntries); newl == 0 {
		t.setEntries(client, action, nil)
		return doThrottle, nil
	} else if newl != l {
		t.setEntries(client, action, newEntries)
	}

	return doThrottle, nil
}

func (t *memoryThrottler) throttle(ctx context.Context, client string, action string, now time.Time) {
	entry := throttleEntry{
		ts: now,
	}
	count := t.addEntry(client, action, entry)
	delay := t.getDelay(count - 1)
	log.Printf("Failed attempt on \"%s\" from %s, throttling by %s", action, client, delay)
	t.doDelay(ctx, delay)
}

func (t *memoryThrottler) delay(ctx context.Context, duration time.Duration) {
	c, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	<-c.Done()
}
