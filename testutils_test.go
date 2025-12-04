/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var listenSignalOnce sync.Once

func ensureNoGoroutinesLeak(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	// Make sure test is not executed with "t.Parallel()"
	t.Setenv("PARALLEL_CHECK", "1")

	// The signal package will start a goroutine the first time "signal.Notify"
	// is called. Do so outside the function under test so the signal goroutine
	// will not be shown as "leaking".
	listenSignalOnce.Do(func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		go func() {
			for {
				<-ch
			}
		}()
	})

	profile := pprof.Lookup("goroutine")
	// Give time for things to settle before capturing the number of
	// go routines
	var before int
	timeout := time.Now().Add(time.Second)
	for time.Now().Before(timeout) {
		before = profile.Count()
		time.Sleep(10 * time.Millisecond)
		if profile.Count() == before {
			break
		}
	}
	var prev bytes.Buffer
	dumpGoroutines("Before:", &prev)

	t.Run("leakcheck", f)

	var after int
	// Give time for things to settle before capturing the number of
	// go routines
	timeout = time.Now().Add(time.Second)
	for time.Now().Before(timeout) {
		after = profile.Count()
		if after == before {
			break
		}
	}

	if after != before {
		io.Copy(os.Stderr, &prev) // nolint
		dumpGoroutines("After:", os.Stderr)
		require.Equal(t, before, after, "Number of Go routines has changed")
	}
}

func dumpGoroutines(prefix string, w io.Writer) {
	if prefix != "" {
		io.WriteString(w, prefix+"\n") // nolint
	}
	profile := pprof.Lookup("goroutine")
	profile.WriteTo(w, 2) // nolint
}

func WaitForUsersJoined(ctx context.Context, t *testing.T, client1 *TestClient, hello1 *ServerMessage, client2 *TestClient, hello2 *ServerMessage) {
	t.Helper()
	// We will receive "joined" events for all clients. The ordering is not
	// defined as messages are processed and sent by asynchronous event handlers.
	client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
}

func MustSucceed1[T any, A1 any](t *testing.T, f func(a1 A1) (T, bool), a1 A1) T {
	t.Helper()
	result, ok := f(a1)
	if !ok {
		t.FailNow()
	}
	return result
}

func MustSucceed2[T any, A1 any, A2 any](t *testing.T, f func(a1 A1, a2 A2) (T, bool), a1 A1, a2 A2) T {
	t.Helper()
	result, ok := f(a1, a2)
	if !ok {
		t.FailNow()
	}
	return result
}

func MustSucceed3[T any, A1 any, A2 any, A3 any](t *testing.T, f func(a1 A1, a2 A2, a3 A3) (T, bool), a1 A1, a2 A2, a3 A3) T {
	t.Helper()
	result, ok := f(a1, a2, a3)
	if !ok {
		t.FailNow()
	}
	return result
}

func AssertEqualSerialized(t *testing.T, expected any, actual any, msgAndArgs ...any) bool {
	t.Helper()

	e, err := json.MarshalIndent(expected, "", "  ")
	if !assert.NoError(t, err) {
		return false
	}

	a, err := json.MarshalIndent(actual, "", "  ")
	if !assert.NoError(t, err) {
		return false
	}

	return assert.Equal(t, string(a), string(e), msgAndArgs...)
}

type testStorage[T any] struct {
	mu sync.Mutex
	// +checklocks:mu
	entries map[string]T // +checklocksignore: Not supported yet, see https://github.com/google/gvisor/issues/11671
}

func (s *testStorage[T]) cleanup(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, key)
	if len(s.entries) == 0 {
		s.entries = nil
	}
}

func (s *testStorage[T]) Set(t *testing.T, value T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := t.Name()
	if _, found := s.entries[key]; !found {
		t.Cleanup(func() {
			s.cleanup(key)
		})
	}

	if s.entries == nil {
		s.entries = make(map[string]T)
	}
	s.entries[key] = value
}

func (s *testStorage[T]) Get(t *testing.T) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := t.Name()
	if value, found := s.entries[key]; found {
		return value, true
	}

	var defaultValue T
	return defaultValue, false
}

func (s *testStorage[T]) Del(t *testing.T) {
	key := t.Name()
	s.cleanup(key)
}
