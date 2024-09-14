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
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

var (
	loggers   map[*testing.T]*zap.Logger
	loggersMu sync.Mutex
)

func removeLoggerForTest(t *testing.T) {
	loggersMu.Lock()
	defer loggersMu.Unlock()

	delete(loggers, t)
}

func GetLoggerForTest(t *testing.T) *zap.Logger {
	loggersMu.Lock()
	defer loggersMu.Unlock()

	log, found := loggers[t]
	if !found {
		if loggers == nil {
			loggers = make(map[*testing.T]*zap.Logger)
		}
		log = zaptest.NewLogger(t)
		loggers[t] = log
		t.Cleanup(func() {
			removeLoggerForTest(t)
		})
	}
	return log
}
