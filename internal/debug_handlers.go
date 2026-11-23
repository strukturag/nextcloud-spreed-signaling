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
	"net/http/pprof"
	runtimepprof "runtime/pprof"

	"github.com/gorilla/mux"
)

// RegisterDebugHandlers installs the pprof handlers below "/debug/pprof".
// Requests are only processed if "enabled" returns true, otherwise a
// "404 Not Found" is returned. The handlers are wrapped with "wrap" before
// being registered.
func RegisterDebugHandlers(r *mux.Router, enabled func() bool, wrap func(func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request)) {
	handle := func(s *mux.Router, path string, f func(http.ResponseWriter, *http.Request)) {
		wrapped := wrap(f)
		s.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if !enabled() {
				http.NotFound(w, r)
				return
			}

			wrapped(w, r)
		})
	}

	s := r.PathPrefix("/debug/pprof").Subrouter()
	handle(s, "", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/debug/pprof/", http.StatusTemporaryRedirect)
	})
	handle(s, "/", pprof.Index)
	handle(s, "/cmdline", pprof.Cmdline)
	handle(s, "/profile", pprof.Profile)
	handle(s, "/symbol", pprof.Symbol)
	handle(s, "/trace", pprof.Trace)
	for _, profile := range runtimepprof.Profiles() {
		handle(s, "/"+profile.Name(), pprof.Handler(profile.Name()).ServeHTTP)
	}
}
