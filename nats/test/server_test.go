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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
)

func TestLocalServer(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	server, port := StartLocalServer(t)
	assert.NotEqual(0, port)

	ctx := log.NewLoggerContext(t.Context(), logtest.NewLoggerForTest(t))

	client, err := nats.NewClient(ctx, server.ClientURL())
	require.NoError(err)

	assert.NoError(client.Close(context.Background()))
}

func TestWaitForSubscriptionsEmpty(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	ctx := log.NewLoggerContext(t.Context(), logtest.NewLoggerForTest(t))

	client, err := nats.NewClient(ctx, nats.LoopbackUrl)
	require.NoError(err)
	defer func() {
		assert.NoError(client.Close(context.Background()))
	}()

	ch := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", ch)
	require.NoError(err)

	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		close(ready)
		WaitForSubscriptionsEmpty(ctx, t, client)
	}()

	<-ready

	require.NoError(sub.Unsubscribe())
	<-done
}
