/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"io"
	"log"
	"testing"
)

var (
	prevWriter io.Writer
	prevFlags  int
)

func init() {
	prevWriter = log.Writer()
	prevFlags = log.Flags()
}

type testLogWriter struct {
	t testing.TB
}

func (w *testLogWriter) Write(b []byte) (int, error) {
	w.t.Helper()
	w.t.Logf("%s", string(b))
	return len(b), nil
}

func CatchLogForTest(t testing.TB) {
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})

	log.SetOutput(&testLogWriter{t})
	log.SetFlags(prevFlags | log.Lshortfile)
}
