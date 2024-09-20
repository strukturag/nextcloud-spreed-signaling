/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func NewDeferredExecutorForTest(t *testing.T, queueSize int) *DeferredExecutor {
	log := GetLoggerForTest(t)
	return NewDeferredExecutor(log, queueSize)
}

func TestDeferredExecutor_MultiClose(t *testing.T) {
	e := NewDeferredExecutorForTest(t, 0)
	defer e.waitForStop()

	e.Close()
	e.Close()
}

func TestDeferredExecutor_QueueSize(t *testing.T) {
	t.Parallel()
	e := NewDeferredExecutorForTest(t, 0)
	defer e.waitForStop()
	defer e.Close()

	delay := 100 * time.Millisecond
	e.Execute(func() {
		time.Sleep(delay)
	})

	// The queue will block until the first command finishes.
	a := time.Now()
	e.Execute(func() {
		time.Sleep(time.Millisecond)
	})
	b := time.Now()
	delta := b.Sub(a)
	// Allow one millisecond less delay to account for time variance on CI runners.
	assert.GreaterOrEqual(t, delta+time.Millisecond, delay)
}

func TestDeferredExecutor_Order(t *testing.T) {
	e := NewDeferredExecutorForTest(t, 64)
	defer e.waitForStop()
	defer e.Close()

	var entries []int
	getFunc := func(x int) func() {
		return func() {
			entries = append(entries, x)
		}
	}

	done := make(chan struct{})
	for x := 0; x < 10; x++ {
		e.Execute(getFunc(x))
	}

	e.Execute(func() {
		close(done)
	})
	<-done

	for x := 0; x < 10; x++ {
		assert.Equal(t, entries[x], x, "Unexpected at position %d", x)
	}
}

func TestDeferredExecutor_CloseFromFunc(t *testing.T) {
	e := NewDeferredExecutorForTest(t, 64)
	defer e.waitForStop()

	done := make(chan struct{})
	e.Execute(func() {
		defer close(done)
		e.Close()
	})

	<-done
}

func TestDeferredExecutor_DeferAfterClose(t *testing.T) {
	e := NewDeferredExecutorForTest(t, 64)
	defer e.waitForStop()

	e.Close()

	e.Execute(func() {
		assert.Fail(t, "method should not have been called")
	})
}

func TestDeferredExecutor_WaitForStopTwice(t *testing.T) {
	e := NewDeferredExecutorForTest(t, 64)
	defer e.waitForStop()

	e.Close()

	e.waitForStop()
}
