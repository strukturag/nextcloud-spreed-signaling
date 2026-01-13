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
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	natstest "github.com/strukturag/nextcloud-spreed-signaling/nats/test"
)

var (
	testTimeout = 10 * time.Second

	EventBackendsForTest = []string{
		"loopback",
		"nats",
	}
)

func GetAsyncEventsForTest(t *testing.T) events.AsyncEvents {
	var events events.AsyncEvents
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

func getRealAsyncEventsForTest(t *testing.T) events.AsyncEvents {
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	server, _ := natstest.StartLocalServer(t)
	events, err := events.NewAsyncEvents(ctx, server.ClientURL())
	require.NoError(t, err)
	return events
}

type natsEvents interface {
	GetNatsClient() nats.Client
}

func getLoopbackAsyncEventsForTest(t *testing.T) events.AsyncEvents {
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	events, err := events.NewAsyncEvents(ctx, nats.LoopbackUrl)
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		e, ok := (events.(natsEvents))
		if !ok {
			// Only can wait for NATS events.
			return
		}

		natstest.WaitForSubscriptionsEmpty(ctx, t, e.GetNatsClient())
	})
	return events
}

func WaitForAsyncEventsFlushed(ctx context.Context, t *testing.T, events events.AsyncEvents) {
	t.Helper()

	e, ok := (events.(natsEvents))
	if !ok {
		// Only can wait for NATS events.
		return
	}

	client, ok := e.GetNatsClient().(*nats.NativeClient)
	if !ok {
		// The loopback NATS clients is executing all events synchronously.
		return
	}

	assert.NoError(t, client.FlushWithContext(ctx))
}
