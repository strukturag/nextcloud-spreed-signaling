/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentStringStringMap(t *testing.T) {
	var m ConcurrentStringStringMap
	if m.Len() != 0 {
		t.Errorf("Expected %d entries, got %d", 0, m.Len())
	}
	if v, found := m.Get("foo"); found {
		t.Errorf("Expected missing entry, got %s", v)
	}

	m.Set("foo", "bar")
	if m.Len() != 1 {
		t.Errorf("Expected %d entries, got %d", 1, m.Len())
	}
	if v, found := m.Get("foo"); !found {
		t.Errorf("Expected entry")
	} else if v != "bar" {
		t.Errorf("Expected bar, got %s", v)
	}

	m.Set("foo", "baz")
	if m.Len() != 1 {
		t.Errorf("Expected %d entries, got %d", 1, m.Len())
	}
	if v, found := m.Get("foo"); !found {
		t.Errorf("Expected entry")
	} else if v != "baz" {
		t.Errorf("Expected baz, got %s", v)
	}

	m.Set("lala", "lolo")
	if m.Len() != 2 {
		t.Errorf("Expected %d entries, got %d", 2, m.Len())
	}
	if v, found := m.Get("lala"); !found {
		t.Errorf("Expected entry")
	} else if v != "lolo" {
		t.Errorf("Expected lolo, got %s", v)
	}

	// Deleting missing entries doesn't do anything.
	m.Del("xyz")
	if m.Len() != 2 {
		t.Errorf("Expected %d entries, got %d", 2, m.Len())
	}
	if v, found := m.Get("foo"); !found {
		t.Errorf("Expected entry")
	} else if v != "baz" {
		t.Errorf("Expected baz, got %s", v)
	}
	if v, found := m.Get("lala"); !found {
		t.Errorf("Expected entry")
	} else if v != "lolo" {
		t.Errorf("Expected lolo, got %s", v)
	}

	m.Del("lala")
	if m.Len() != 1 {
		t.Errorf("Expected %d entries, got %d", 2, m.Len())
	}
	if v, found := m.Get("foo"); !found {
		t.Errorf("Expected entry")
	} else if v != "baz" {
		t.Errorf("Expected baz, got %s", v)
	}
	if v, found := m.Get("lala"); found {
		t.Errorf("Expected missing entry, got %s", v)
	}
	m.Clear()

	var wg sync.WaitGroup
	concurrency := 100
	count := 1000
	for x := 0; x < concurrency; x++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()

			key := "key-" + strconv.Itoa(x)
			for y := 0; y < count; y = y + 1 {
				value := newRandomString(32)
				m.Set(key, value)
				if v, found := m.Get(key); !found {
					t.Errorf("Expected entry for key %s", key)
					return
				} else if v != value {
					t.Errorf("Expected value %s for key %s, got %s", value, key, v)
					return
				}
			}
		}(x)
	}
	wg.Wait()
	if m.Len() != concurrency {
		t.Errorf("Expected %d entries, got %d", concurrency, m.Len())
	}
}
