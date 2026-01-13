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
package test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/dns"
)

func TestDnsMonitor(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	lookup := NewMockLookup()
	ips := []net.IP{
		net.ParseIP("1.2.3.4"),
	}
	lookup.Set("domain.invalid", ips)
	monitor := NewMonitorForTest(t, time.Second, lookup)

	called := make(chan struct{})
	callback1 := func(entry *dns.MonitorEntry, all []net.IP, add []net.IP, keep []net.IP, remove []net.IP) {
		defer func() {
			called <- struct{}{}
		}()

		assert.Equal(ips, all)
		assert.Equal(ips, add)
		assert.Empty(keep)
		assert.Empty(remove)
	}

	entry1, err := monitor.Add("domain.invalid", callback1)
	require.NoError(err)

	t.Cleanup(func() {
		monitor.Remove(entry1)
	})

	<-called
}
