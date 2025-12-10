//go:build !go1.25

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
package test

import (
	"testing"
	"testing/synctest"
)

func SynctestTest(t *testing.T, f func(t *testing.T)) {
	t.Helper()

	synctest.Run(func() {
		t.Run("synctest", func(t *testing.T) {
			// synctest of Go 1.25 doesn't support "t.Parallel()" but we can't prevent
			// this here. Invalid calls will be detected when running with Go 1.25.
			f(t)
		})
	})
}
