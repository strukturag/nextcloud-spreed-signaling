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
	"container/list"
	"sync"
)

type cacheEntry struct {
	key   string
	value interface{}
}

type LruCache struct {
	size    int
	mu      sync.Mutex
	entries *list.List
	data    map[string]*list.Element
}

func NewLruCache(size int) *LruCache {
	return &LruCache{
		size:    size,
		entries: list.New(),
		data:    make(map[string]*list.Element),
	}
}

func (c *LruCache) Set(key string, value interface{}) {
	c.mu.Lock()
	if v, found := c.data[key]; found {
		c.entries.MoveToFront(v)
		v.Value.(*cacheEntry).value = value
		c.mu.Unlock()
		return
	}

	v := c.entries.PushFront(&cacheEntry{
		key:   key,
		value: value,
	})
	c.data[key] = v
	if c.size > 0 && c.entries.Len() > c.size {
		c.removeOldestLocked()
	}
	c.mu.Unlock()
}

func (c *LruCache) Get(key string) interface{} {
	c.mu.Lock()
	if v, found := c.data[key]; found {
		c.entries.MoveToFront(v)
		value := v.Value.(*cacheEntry).value
		c.mu.Unlock()
		return value
	}

	c.mu.Unlock()
	return nil
}

func (c *LruCache) Remove(key string) {
	c.mu.Lock()
	if v, found := c.data[key]; found {
		c.removeElement(v)
	}
	c.mu.Unlock()
}

func (c *LruCache) removeOldestLocked() {
	v := c.entries.Back()
	if v != nil {
		c.removeElement(v)
	}
}

func (c *LruCache) RemoveOldest() {
	c.mu.Lock()
	c.removeOldestLocked()
	c.mu.Unlock()
}

func (c *LruCache) removeElement(e *list.Element) {
	c.entries.Remove(e)
	entry := e.Value.(*cacheEntry)
	delete(c.data, entry.key)
}

func (c *LruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries.Len()
}
