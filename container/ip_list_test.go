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
package container

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIPList(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	a, err := ParseIPList("127.0.0.1, 192.168.0.1, 192.168.1.1/24")
	require.NoError(err)
	require.False(a.Empty())
	require.Equal(`[127.0.0.1/32, 192.168.0.1/32, 192.168.1.0/24]`, a.String())

	contained := []string{
		"127.0.0.1",
		"192.168.0.1",
		"192.168.1.1",
		"192.168.1.100",
	}
	notContained := []string{
		"192.168.0.2",
		"10.1.2.3",
	}

	for _, addr := range contained {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			assert := assert.New(t)
			if ip := net.ParseIP(addr); assert.NotNil(ip, "error parsing %s", addr) {
				assert.True(a.Contains(ip), "should contain %s", addr)
			}
		})
	}

	for _, addr := range notContained {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			assert := assert.New(t)
			if ip := net.ParseIP(addr); assert.NotNil(ip, "error parsing %s", addr) {
				assert.False(a.Contains(ip), "should not contain %s", addr)
			}
		})
	}
}

func TestDefaultAllowedIPs(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	ips := DefaultAllowedIPs()
	assert.True(ips.Contains(net.ParseIP("127.0.0.1")))
	assert.False(ips.Contains(net.ParseIP("127.1.0.1")))
	assert.True(ips.Contains(net.ParseIP("::1")))
	assert.False(ips.Contains(net.ParseIP("1.1.1.1")))
}

func TestDefaultPrivateIPs(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	ips := DefaultPrivateIPs()
	assert.True(ips.Contains(net.ParseIP("127.0.0.1")))
	assert.True(ips.Contains(net.ParseIP("127.1.0.1")))
	assert.True(ips.Contains(net.ParseIP("::1")))
	assert.True(ips.Contains(net.ParseIP("10.1.2.3")))
	assert.True(ips.Contains(net.ParseIP("172.16.17.18")))
	assert.True(ips.Contains(net.ParseIP("192.168.10.20")))
	assert.False(ips.Contains(net.ParseIP("1.1.1.1")))
}
