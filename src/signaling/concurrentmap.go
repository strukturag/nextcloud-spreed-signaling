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
	"sync"
)

type ConcurrentStringStringMap struct {
	sync.Mutex
	d map[string]string
}

func (m *ConcurrentStringStringMap) Set(key, value string) {
	m.Lock()
	defer m.Unlock()
	if m.d == nil {
		m.d = make(map[string]string)
	}
	m.d[key] = value
}

func (m *ConcurrentStringStringMap) Get(key string) (string, bool) {
	m.Lock()
	defer m.Unlock()
	s, found := m.d[key]
	return s, found
}

func (m *ConcurrentStringStringMap) Del(key string) {
	m.Lock()
	defer m.Unlock()
	delete(m.d, key)
}

func (m *ConcurrentStringStringMap) Len() int {
	m.Lock()
	defer m.Unlock()
	return len(m.d)
}

func (m *ConcurrentStringStringMap) Clear() {
	m.Lock()
	defer m.Unlock()
	m.d = nil
}
