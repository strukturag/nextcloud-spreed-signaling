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
	"time"

	"github.com/stretchr/testify/assert"
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, events.Close(ctx))
	})
	return events
}

func getRealAsyncEventsForTest(t *testing.T) AsyncEvents {
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	server, _ := startLocalNatsServer(t)
	events, err := NewAsyncEvents(ctx, server.ClientURL())
	if err != nil {
		require.NoError(t, err)
	}
	return events
}

func getLoopbackAsyncEventsForTest(t *testing.T) AsyncEvents {
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	events, err := NewAsyncEvents(ctx, NatsLoopbackUrl)
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
