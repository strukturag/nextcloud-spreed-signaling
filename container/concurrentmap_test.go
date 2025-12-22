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
package container

import (
	"crypto/rand"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConcurrentStringStringMap(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	var m ConcurrentMap[string, string]
	assert.Equal(0, m.Len())
	v, found := m.Get("foo")
	assert.False(found, "Expected missing entry, got %s", v)

	m.Set("foo", "bar")
	assert.Equal(1, m.Len())
	if v, found := m.Get("foo"); assert.True(found) {
		assert.Equal("bar", v)
	}

	m.Set("foo", "baz")
	assert.Equal(1, m.Len())
	if v, found := m.Get("foo"); assert.True(found) {
		assert.Equal("baz", v)
	}

	m.Set("lala", "lolo")
	assert.Equal(2, m.Len())
	if v, found := m.Get("lala"); assert.True(found) {
		assert.Equal("lolo", v)
	}

	// Deleting missing entries doesn't do anything.
	m.Del("xyz")
	assert.Equal(2, m.Len())
	if v, found := m.Get("foo"); assert.True(found) {
		assert.Equal("baz", v)
	}
	if v, found := m.Get("lala"); assert.True(found) {
		assert.Equal("lolo", v)
	}

	m.Del("lala")
	assert.Equal(1, m.Len())
	if v, found := m.Get("foo"); assert.True(found) {
		assert.Equal("baz", v)
	}
	v, found = m.Get("lala")
	assert.False(found, "Expected missing entry, got %s", v)
	m.Clear()

	var wg sync.WaitGroup
	concurrency := 100
	count := 1000
	for x := range concurrency {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()

			key := "key-" + strconv.Itoa(x)
			rnd := rand.Text()
			for y := range count {
				value := rnd + "-" + strconv.Itoa(y)
				m.Set(key, value)
				if v, found := m.Get(key); !found {
					assert.True(found, "Expected entry for key %s", key)
					return
				} else if v != value {
					assert.Equal(value, v, "Unexpected value for key %s", key)
					return
				}
			}
		}(x)
	}
	wg.Wait()
	assert.Equal(concurrency, m.Len())
}
