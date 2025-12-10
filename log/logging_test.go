/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGlobalLogger(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	defer func() {
		if err := recover(); assert.NotNil(err) {
			assert.Equal("accessed global logger", err)
		}
	}()

	logger := LoggerFromContext(t.Context())
	assert.Fail("should have paniced", "got logger %+v", logger)
}

func TestLoggerContext(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	testLogger := NewLoggerForTest(t)
	testLogger.Printf("Hello %s!", "world")

	ctx := NewLoggerContext(t.Context(), testLogger)
	logger2 := LoggerFromContext(ctx)
	assert.Equal(testLogger, logger2)
}

func TestNilLoggerContext(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	defer func() {
		if err := recover(); assert.NotNil(err) {
			assert.Equal("logger is nil", err)
		}
	}()

	ctx := NewLoggerContext(t.Context(), nil)
	assert.Fail("should have paniced", "got context %+v", ctx)
}
