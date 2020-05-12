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
	"fmt"
	"testing"
)

func TestLruUnbound(t *testing.T) {
	lru := NewLruCache(0)
	count := 10
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		lru.Set(key, i)
	}
	if lru.Len() != count {
		t.Errorf("Expected %d entries, got %d", count, lru.Len())
	}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}
	// The first key ("0") is now the oldest.
	lru.RemoveOldest()
	if lru.Len() != count-1 {
		t.Errorf("Expected %d entries after RemoveOldest, got %d", count-1, lru.Len())
	}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if i == 0 {
			if value != nil {
				t.Errorf("The value for key %s should have been removed", key)
			}
			continue
		} else if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}

	// NOTE: Key "0" no longer exists below, so make sure to not set it again.

	// Using the same keys will update the ordering.
	for i := count - 1; i >= 1; i-- {
		key := fmt.Sprintf("%d", i)
		lru.Set(key, i)
	}
	if lru.Len() != count-1 {
		t.Errorf("Expected %d entries, got %d", count-1, lru.Len())
	}
	// NOTE: The same ordering as the Set calls above.
	for i := count - 1; i >= 1; i-- {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}

	// The last key ("9") is now the oldest.
	lru.RemoveOldest()
	if lru.Len() != count-2 {
		t.Errorf("Expected %d entries after RemoveOldest, got %d", count-2, lru.Len())
	}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if i == 0 || i == count-1 {
			if value != nil {
				t.Errorf("The value for key %s should have been removed", key)
			}
			continue
		} else if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}

	// Remove an arbitrary key from the cache
	key := fmt.Sprintf("%d", count/2)
	lru.Remove(key)
	if lru.Len() != count-3 {
		t.Errorf("Expected %d entries after RemoveOldest, got %d", count-3, lru.Len())
	}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if i == 0 || i == count-1 || i == count/2 {
			if value != nil {
				t.Errorf("The value for key %s should have been removed", key)
			}
			continue
		} else if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}
}

func TestLruBound(t *testing.T) {
	size := 2
	lru := NewLruCache(size)
	count := 10
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		lru.Set(key, i)
	}
	if lru.Len() != size {
		t.Errorf("Expected %d entries, got %d", size, lru.Len())
	}
	// Only the last "size" entries have been stored.
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%d", i)
		value := lru.Get(key)
		if i < count-size {
			if value != nil {
				t.Errorf("The value for key %s should have been removed", key)
			}
			continue
		} else if value == nil {
			t.Errorf("No value found for %s", key)
			continue
		} else if value.(int) != i {
			t.Errorf("Expected value to be %d, got %d", value.(int), i)
		}
	}
}
