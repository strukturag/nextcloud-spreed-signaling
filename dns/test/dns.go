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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/dns"
	"github.com/strukturag/nextcloud-spreed-signaling/dns/internal"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
)

type MockLookup = internal.MockLookup

func NewMockLookup() *MockLookup {
	return internal.NewMockLookup()
}

func NewMonitorForTest(t *testing.T, interval time.Duration, lookup *MockLookup) *dns.Monitor {
	t.Helper()
	require := require.New(t)

	logger := logtest.NewLoggerForTest(t)
	var lookupFunc dns.MonitorLookupFunc
	if lookup != nil {
		lookupFunc = lookup.Lookup
	}
	monitor, err := dns.NewMonitor(logger, interval, lookupFunc)
	require.NoError(err)

	t.Cleanup(func() {
		monitor.Stop()
	})

	require.NoError(monitor.Start())
	return monitor
}
