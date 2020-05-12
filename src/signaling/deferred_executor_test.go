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
)

func TestDeferredExecutor_MultiClose(t *testing.T) {
	e := NewDeferredExecutor(0)
	defer e.waitForStop()

	e.Close()
	e.Close()
}

func TestDeferredExecutor_QueueSize(t *testing.T) {
	e := NewDeferredExecutor(0)
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
	if delta < delay {
		t.Errorf("Expected a delay of %s, got %s", delay, delta)
	}
}

func TestDeferredExecutor_Order(t *testing.T) {
	e := NewDeferredExecutor(64)
	defer e.waitForStop()
	defer e.Close()

	var entries []int
	getFunc := func(x int) func() {
		return func() {
			entries = append(entries, x)
		}
	}

	done := make(chan bool)
	for x := 0; x < 10; x += 1 {
		e.Execute(getFunc(x))
	}

	e.Execute(func() {
		done <- true
	})
	<-done

	for x := 0; x < 10; x += 1 {
		if entries[x] != x {
			t.Errorf("Expected %d at position %d, got %d", x, x, entries[x])
		}
	}
}

func TestDeferredExecutor_CloseFromFunc(t *testing.T) {
	e := NewDeferredExecutor(64)
	defer e.waitForStop()

	done := make(chan bool)
	e.Execute(func() {
		e.Close()
		done <- true
	})

	<-done
}

func TestDeferredExecutor_DeferAfterClose(t *testing.T) {
	e := NewDeferredExecutor(64)
	defer e.waitForStop()

	e.Close()

	e.Execute(func() {
		t.Error("method should not have been called")
	})
}
