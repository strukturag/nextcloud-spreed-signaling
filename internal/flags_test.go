/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
package internal

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlags(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	var f Flags
	assert.EqualValues(0, f.Get())
	assert.True(f.Add(1))
	assert.EqualValues(1, f.Get())
	assert.False(f.Add(1))
	assert.EqualValues(1, f.Get())
	assert.True(f.Add(2))
	assert.EqualValues(3, f.Get())
	assert.True(f.Remove(1))
	assert.EqualValues(2, f.Get())
	assert.False(f.Remove(1))
	assert.EqualValues(2, f.Get())
	assert.True(f.Add(3))
	assert.EqualValues(3, f.Get())
	assert.True(f.Remove(1))
	assert.EqualValues(2, f.Get())
}

func runConcurrentFlags(t *testing.T, count int, f func()) {
	var start sync.WaitGroup
	start.Add(1)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	for range count {
		ready.Add(1)
		done.Go(func() {
			ready.Done()
			start.Wait()
			f()
		})
	}
	ready.Wait()
	start.Done()
	done.Wait()
}

func TestFlagsConcurrentAdd(t *testing.T) {
	t.Parallel()
	var flags Flags

	var added atomic.Int32
	runConcurrentFlags(t, 100, func() {
		if flags.Add(1) {
			added.Add(1)
		}
	})
	assert.EqualValues(t, 1, added.Load(), "expected only one successful attempt")
}

func TestFlagsConcurrentRemove(t *testing.T) {
	t.Parallel()
	var flags Flags
	flags.Set(1)

	var removed atomic.Int32
	runConcurrentFlags(t, 100, func() {
		if flags.Remove(1) {
			removed.Add(1)
		}
	})
	assert.EqualValues(t, 1, removed.Load(), "expected only one successful attempt")
}

func TestFlagsConcurrentSet(t *testing.T) {
	t.Parallel()
	var flags Flags

	var set atomic.Int32
	runConcurrentFlags(t, 100, func() {
		if flags.Set(1) {
			set.Add(1)
		}
	})
	assert.EqualValues(t, 1, set.Load(), "expected only one successful attempt")
}
