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
package events

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/nats"
)

func TestAsyncQueue_SlowHandler(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	logger := logtest.NewLoggerForTest(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	queue := NewAsyncQueue(logger, "test")
	done := make(chan struct{})
	defer close(done)

	release := make(chan struct{})
	received := make(chan string, 1)
	go queue.Run(done, func(msg *nats.Msg) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		received <- string(msg.Data)
	})

	// Messages can be added while the handler is blocked.
	count := 1000
	for i := range count {
		queue.Push(&nats.Msg{Data: []byte(strconv.Itoa(i))})
	}

	close(release)
	for i := range count {
		select {
		case data := <-received:
			assert.Equal(strconv.Itoa(i), data)
		case <-ctx.Done():
			require.NoError(ctx.Err(), "did not receive message %d", i)
		}
	}
}

func TestAsyncQueue_Done(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	queue := NewAsyncQueue(logger, "test")
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		queue.Run(done, func(msg *nats.Msg) {
			assert.Fail(t, "should not receive messages", "received %s", string(msg.Data))
		})
	}()

	close(done)
	select {
	case <-stopped:
	case <-ctx.Done():
		assert.NoError(t, ctx.Err(), "queue was not stopped")
	}
}

func TestAsyncQueue_Barrier(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	logger := logtest.NewLoggerForTest(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	queue := NewAsyncQueue(logger, "test")

	// The barrier is closed after the previous messages have been processed.
	queue.Push(&nats.Msg{Data: []byte("1")})
	barrier := queue.Barrier()
	queue.Push(&nats.Msg{Data: []byte("2")})

	done := make(chan struct{})
	defer close(done)

	release := make(chan struct{})
	received := make(chan string, 2)
	go queue.Run(done, func(msg *nats.Msg) {
		received <- string(msg.Data)
		if string(msg.Data) == "1" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})

	select {
	case data := <-received:
		assert.Equal("1", data)
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	select {
	case <-barrier:
		assert.Fail("barrier closed before previous message was processed")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-barrier:
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}
	select {
	case data := <-received:
		assert.Equal("2", data)
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}
}
