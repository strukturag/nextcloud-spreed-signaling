/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package test

import (
	"sync"
	"testing"
)

type Storage[T any] struct {
	mu sync.Mutex
	// +checklocks:mu
	entries map[string]T
}

func (s *Storage[T]) cleanup(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, key)
	if len(s.entries) == 0 {
		s.entries = nil
	}
}

func (s *Storage[T]) Set(tb testing.TB, value T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := tb.Name()
	if _, found := s.entries[key]; !found {
		tb.Cleanup(func() {
			s.cleanup(key)
		})
	}

	if s.entries == nil {
		s.entries = make(map[string]T)
	}
	s.entries[key] = value
}

func (s *Storage[T]) Get(tb testing.TB) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := tb.Name()
	if value, found := s.entries[key]; found {
		return value, true
	}

	var defaultValue T
	return defaultValue, false
}

func (s *Storage[T]) Del(tb testing.TB) {
	key := tb.Name()
	s.cleanup(key)
}
