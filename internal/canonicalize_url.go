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
package internal

import (
	"net/url"
	"strings"
)

func hasStandardPort(u *url.URL) bool {
	switch u.Scheme {
	case "http":
		return u.Port() == "80"
	case "https":
		return u.Port() == "443"
	default:
		return false
	}
}

func CanonicalizeUrl(u *url.URL) (*url.URL, bool) {
	var changed bool
	if strings.Contains(u.Host, ":") && hasStandardPort(u) {
		u.Host = u.Hostname()
		changed = true
	}
	return u, changed
}

func CanonicalizeUrlString(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return s, err
	}

	if strings.Contains(u.Host, ":") && hasStandardPort(u) {
		u.Host = u.Hostname()
		s = u.String()
	}
	return s, nil
}
