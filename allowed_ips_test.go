/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
	"net"
	"testing"
)

func TestAllowedIps(t *testing.T) {
	a, err := ParseAllowedIps("127.0.0.1, 192.168.0.1, 192.168.1.1/24")
	if err != nil {
		t.Fatal(err)
	}
	if a.Empty() {
		t.Fatal("should not be empty")
	}

	allowed := []string{
		"127.0.0.1",
		"192.168.0.1",
		"192.168.1.1",
		"192.168.1.100",
	}
	notAllowed := []string{
		"192.168.0.2",
		"10.1.2.3",
	}

	for _, addr := range allowed {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Errorf("error parsing %s", addr)
			} else if !a.Allowed(ip) {
				t.Errorf("should allow %s", addr)
			}
		})
	}

	for _, addr := range notAllowed {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Errorf("error parsing %s", addr)
			} else if a.Allowed(ip) {
				t.Errorf("should not allow %s", addr)
			}
		})
	}
}
