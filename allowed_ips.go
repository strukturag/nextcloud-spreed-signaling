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
	"fmt"
	"net"
	"strings"
)

type AllowedIps struct {
	allowed []*net.IPNet
}

func (a *AllowedIps) Empty() bool {
	return len(a.allowed) == 0
}

func (a *AllowedIps) Allowed(ip net.IP) bool {
	for _, i := range a.allowed {
		if i.Contains(ip) {
			return true
		}
	}

	return false
}

func parseIPNet(s string) (*net.IPNet, error) {
	var ipnet *net.IPNet
	if strings.ContainsRune(s, '/') {
		var err error
		if _, ipnet, err = net.ParseCIDR(s); err != nil {
			return nil, fmt.Errorf("invalid IP address/subnet %s: %w", s, err)
		}
	} else {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address %s", s)
		}

		ipnet = &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(len(ip)*8, len(ip)*8),
		}
	}

	return ipnet, nil
}

func ParseAllowedIps(allowed string) (*AllowedIps, error) {
	var allowedIps []*net.IPNet
	for _, ip := range strings.Split(allowed, ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			i, err := parseIPNet(ip)
			if err != nil {
				return nil, err
			}
			allowedIps = append(allowedIps, i)
		}
	}

	result := &AllowedIps{
		allowed: allowedIps,
	}
	return result, nil
}

func DefaultAllowedIps() *AllowedIps {
	allowedIps := []*net.IPNet{
		{
			IP:   net.ParseIP("127.0.0.1"),
			Mask: net.CIDRMask(32, 32),
		},
	}

	result := &AllowedIps{
		allowed: allowedIps,
	}
	return result
}
