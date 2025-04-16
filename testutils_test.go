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
	"io"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

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
