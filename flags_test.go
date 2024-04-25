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
package signaling

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestFlags(t *testing.T) {
	var f Flags
	if f.Get() != 0 {
		t.Fatalf("Expected flags 0, got %d", f.Get())
	}
	if !f.Add(1) {
		t.Error("expected true")
	}
	if f.Get() != 1 {
		t.Fatalf("Expected flags 1, got %d", f.Get())
	}
	if f.Add(1) {
		t.Error("expected false")
	}
	if f.Get() != 1 {
		t.Fatalf("Expected flags 1, got %d", f.Get())
	}
	if !f.Add(2) {
		t.Error("expected true")
	}
	if f.Get() != 3 {
		t.Fatalf("Expected flags 3, got %d", f.Get())
	}
	if !f.Remove(1) {
		t.Error("expected true")
	}
	if f.Get() != 2 {
		t.Fatalf("Expected flags 2, got %d", f.Get())
	}
	if f.Remove(1) {
		t.Error("expected false")
	}
	if f.Get() != 2 {
		t.Fatalf("Expected flags 2, got %d", f.Get())
	}
	if !f.Add(3) {
		t.Error("expected true")
	}
	if f.Get() != 3 {
		t.Fatalf("Expected flags 3, got %d", f.Get())
	}
	if !f.Remove(1) {
		t.Error("expected true")
	}
	if f.Get() != 2 {
		t.Fatalf("Expected flags 2, got %d", f.Get())
	}
}

func runConcurrentFlags(t *testing.T, count int, f func()) {
	var start sync.WaitGroup
	start.Add(1)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	for i := 0; i < count; i++ {
		done.Add(1)
		ready.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			start.Wait()
			f()
		}()
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
	if added.Load() != 1 {
		t.Errorf("expected only one successfull attempt, got %d", added.Load())
	}
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
	if removed.Load() != 1 {
		t.Errorf("expected only one successfull attempt, got %d", removed.Load())
	}
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
	if set.Load() != 1 {
		t.Errorf("expected only one successfull attempt, got %d", set.Load())
	}
}
