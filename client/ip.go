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
package client

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/strukturag/nextcloud-spreed-signaling/container"
)

var (
	DefaultTrustedProxies = container.DefaultPrivateIPs()
)

func GetRealUserIP(r *http.Request, trusted *container.IPList) string {
	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}

	ip := net.ParseIP(addr)
	if len(ip) == 0 {
		return addr
	}

	// Don't check any headers if the server can be reached by untrusted clients directly.
	if trusted == nil || !trusted.Contains(ip) {
		return addr
	}

	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		if ip := net.ParseIP(realIP); len(ip) > 0 {
			return realIP
		}
	}

	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For#selecting_an_ip_address
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	if len(forwarded) > 0 {
		slices.Reverse(forwarded)
		var lastTrusted string
		for _, hop := range forwarded {
			hop = strings.TrimSpace(hop)
			// Make sure to remove any port.
			if host, _, err := net.SplitHostPort(hop); err == nil {
				hop = host
			}

			ip := net.ParseIP(hop)
			if len(ip) == 0 {
				continue
			}

			if trusted.Contains(ip) {
				lastTrusted = hop
				continue
			}

			return hop
		}

		// If all entries in the "X-Forwarded-For" list are trusted, the left-most
		// will be the client IP. This can happen if a subnet is trusted and the
		// client also has an IP from this subnet.
		if lastTrusted != "" {
			return lastTrusted
		}
	}

	return addr
}
