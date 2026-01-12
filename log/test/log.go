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
package test

import (
	"bytes"
	stdlog "log"
	"sync"
	"testing"

	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type testLogWriter struct {
	mu sync.Mutex
	t  testing.TB
}

func (w *testLogWriter) Write(b []byte) (int, error) {
	w.t.Helper()
	if !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeTestOutput(w.t, b)
}

var (
	testLoggers internal.TestStorage[log.Logger]
)

func NewLoggerForTest(t testing.TB) log.Logger {
	t.Helper()

	logger, found := testLoggers.Get(t)
	if !found {
		logger = stdlog.New(&testLogWriter{
			t: t,
		}, t.Name()+": ", stdlog.LstdFlags|stdlog.Lmicroseconds|stdlog.Lshortfile)

		testLoggers.Set(t, logger)
	}
	return logger
}
