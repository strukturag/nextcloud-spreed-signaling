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
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/nats"
)

func StartLocalServer(t *testing.T) (*server.Server, int) {
	t.Helper()
	return StartLocalServerPort(t, server.RANDOM_PORT)
}

func StartLocalServerPort(t *testing.T, port int) (*server.Server, int) {
	t.Helper()
	opts := test.DefaultTestOptions
	opts.Port = port
	opts.Cluster.Name = "testing"
	srv := test.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv, opts.Port
}

func WaitForSubscriptionsEmpty(ctx context.Context, t *testing.T, client nats.Client) {
	t.Helper()
	if c, ok := client.(*nats.LoopbackClient); assert.True(t, ok, "expected LoopbackNatsClient, got %T", client) {
		for {
			remaining := c.SubscriptionCount()
			if remaining == 0 {
				break
			}

			select {
			case <-ctx.Done():
				assert.NoError(t, ctx.Err(), "Error waiting for %d subscriptions to terminate", remaining)
				return
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
}
