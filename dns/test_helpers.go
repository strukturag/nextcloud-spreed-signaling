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
package dns

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type MockLookup struct {
	sync.RWMutex

	// +checklocks:RWMutex
	ips map[string][]net.IP
}

func NewMockLookupForTest(t *testing.T) *MockLookup {
	t.Helper()
	mock := &MockLookup{
		ips: make(map[string][]net.IP),
	}
	return mock
}

func (m *MockLookup) Set(host string, ips []net.IP) {
	m.Lock()
	defer m.Unlock()

	m.ips[host] = ips
}

func (m *MockLookup) Get(host string) []net.IP {
	m.Lock()
	defer m.Unlock()

	return m.ips[host]
}

func (m *MockLookup) lookup(host string) ([]net.IP, error) {
	m.RLock()
	defer m.RUnlock()

	ips, found := m.ips[host]
	if !found {
		return nil, &net.DNSError{
			Err:        "could not resolve " + host,
			Name:       host,
			IsNotFound: true,
		}
	}

	return append([]net.IP{}, ips...), nil
}

func NewMonitorForTest(t *testing.T, interval time.Duration, lookup *MockLookup) *Monitor {
	t.Helper()
	require := require.New(t)

	logger := log.NewLoggerForTest(t)
	var lookupFunc MonitorLookupFunc
	if lookup != nil {
		lookupFunc = lookup.lookup
	}
	monitor, err := NewMonitor(logger, interval, lookupFunc)
	require.NoError(err)

	t.Cleanup(func() {
		monitor.Stop()
	})

	require.NoError(monitor.Start())
	return monitor
}
