/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package internal

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestRegisterDebugHandlers(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	var enabled atomic.Bool
	var wrapped atomic.Int32
	r := mux.NewRouter()
	RegisterDebugHandlers(r, enabled.Load, func(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			wrapped.Add(1)
			f(w, r)
		}
	})

	request := func(path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return w.Code
	}

	paths := []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/goroutine"}
	for _, path := range paths {
		assert.Equal(http.StatusNotFound, request(path), "path %s", path)
	}
	assert.EqualValues(0, wrapped.Load())

	enabled.Store(true)
	for _, path := range paths {
		assert.Equal(http.StatusOK, request(path), "path %s", path)
	}
	assert.EqualValues(len(paths), wrapped.Load())
	assert.Equal(http.StatusTemporaryRedirect, request("/debug/pprof"))

	enabled.Store(false)
	for _, path := range paths {
		assert.Equal(http.StatusNotFound, request(path), "path %s", path)
	}
}
