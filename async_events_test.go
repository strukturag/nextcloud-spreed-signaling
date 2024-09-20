/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	eventBackendsForTest = []string{
		"loopback",
		"nats",
	}
)

func getAsyncEventsForTest(t *testing.T) AsyncEvents {
	var events AsyncEvents
	if strings.HasSuffix(t.Name(), "/nats") {
		events = getRealAsyncEventsForTest(t)
	} else {
		events = getLoopbackAsyncEventsForTest(t)
	}
	t.Cleanup(func() {
		events.Close()
	})
	return events
}

func getRealAsyncEventsForTest(t *testing.T) AsyncEvents {
	url := startLocalNatsServer(t)
	log := GetLoggerForTest(t)
	events, err := NewAsyncEvents(log, url)
	if err != nil {
		require.NoError(t, err)
	}
	return events
}

func getLoopbackAsyncEventsForTest(t *testing.T) AsyncEvents {
	log := GetLoggerForTest(t)
	events, err := NewAsyncEvents(log, NatsLoopbackUrl)
	if err != nil {
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		nats := (events.(*asyncEventsNats)).client
		(nats).(*LoopbackNatsClient).waitForSubscriptionsEmpty(ctx, t)
	})
	return events
}
