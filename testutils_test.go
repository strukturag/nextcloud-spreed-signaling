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
	"os"
	"runtime/pprof"
	"testing"
	"time"
)

func ensureNoGoroutinesLeak(t *testing.T, f func()) {
	profile := pprof.Lookup("goroutine")
	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)
	before := profile.Count()

	f()

	var after int
	// Give time for things to settle before capturing the number of
	// go routines
	timeout := time.Now().Add(time.Second)
	for time.Now().Before(timeout) {
		after = profile.Count()
		if after == before {
			break
		}
	}

	if after != before {
		profile.WriteTo(os.Stderr, 2) // nolint
		t.Fatalf("Number of Go routines has changed in %s from %d to %d", t.Name(), before, after)
	}
}
