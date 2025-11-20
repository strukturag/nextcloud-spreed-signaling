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
package signaling

import (
	"context"
	"log"
	"testing"
)

type loggerKey struct{}

var (
	ctxLogger loggerKey = struct{}{}
)

type Logger interface {
	Printf(format string, v ...any)
	Println(...any)
}

// NewLoggerContext returns a derieved context that stores the passed logger.
func NewLoggerContext(ctx context.Context, logger Logger) context.Context {
	if logger == nil {
		panic("logger is nil")
	}
	return context.WithValue(ctx, ctxLogger, logger)
}

// LoggerFromContext returns the logger to use for the passed context.
func LoggerFromContext(ctx context.Context) Logger {
	logger := ctx.Value(ctxLogger)
	if logger == nil {
		if testing.Testing() {
			panic("accessed global logger")
		}
		return log.Default()
	}

	return logger.(Logger)
}
