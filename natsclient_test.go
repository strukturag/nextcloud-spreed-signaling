/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func startLocalNatsServer(t *testing.T) (*server.Server, int) {
	t.Helper()
	return startLocalNatsServerPort(t, server.RANDOM_PORT)
}

func startLocalNatsServerPort(t *testing.T, port int) (*server.Server, int) {
	t.Helper()
	opts := natsserver.DefaultTestOptions
	opts.Port = port
	opts.Cluster.Name = "testing"
	srv := natsserver.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv, opts.Port
}

func CreateLocalNatsClientForTest(t *testing.T, options ...nats.Option) (*server.Server, int, NatsClient) {
	t.Helper()
	server, port := startLocalNatsServer(t)
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	result, err := NewNatsClient(ctx, server.ClientURL(), options...)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, result.Close(ctx))
	})
	return server, port, result
}

func testNatsClient_Subscribe(t *testing.T, client NatsClient) {
	require := require.New(t)
	assert := assert.New(t)
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	require.NoError(err)
	ch := make(chan struct{})

	var received atomic.Int32
	maxPublish := int32(20)
	ready := make(chan struct{})
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		close(ready)
		for {
			select {
			case <-dest:
				total := received.Add(1)
				if total == maxPublish {
					if err := sub.Unsubscribe(); !assert.NoError(err) {
						return
					}
					close(ch)
				}
			case <-quit:
				return
			}
		}
	}()
	<-ready
	for range maxPublish {
		assert.NoError(client.Publish("foo", []byte("hello")))

		// Allow NATS goroutines to process messages.
		time.Sleep(10 * time.Millisecond)
	}
	<-ch

	require.Equal(maxPublish, received.Load(), "Received wrong # of messages")
}

func TestNatsClient_Subscribe(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		_, _, client := CreateLocalNatsClientForTest(t)

		testNatsClient_Subscribe(t, client)
	})
}

func testNatsClient_PublishAfterClose(t *testing.T, client NatsClient) {
	assert.NoError(t, client.Close(t.Context()))

	assert.ErrorIs(t, client.Publish("foo", "bar"), nats.ErrConnectionClosed)
}

func TestNatsClient_PublishAfterClose(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		_, _, client := CreateLocalNatsClientForTest(t)

		testNatsClient_PublishAfterClose(t, client)
	})
}

func testNatsClient_SubscribeAfterClose(t *testing.T, client NatsClient) {
	assert.NoError(t, client.Close(t.Context()))

	ch := make(chan *nats.Msg)
	_, err := client.Subscribe("foo", ch)
	assert.ErrorIs(t, err, nats.ErrConnectionClosed)
}

func TestNatsClient_SubscribeAfterClose(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		_, _, client := CreateLocalNatsClientForTest(t)

		testNatsClient_SubscribeAfterClose(t, client)
	})
}

func testNatsClient_BadSubjects(t *testing.T, client NatsClient) {
	assert := assert.New(t)
	subjects := []string{
		"foo bar",
		"foo.",
	}

	ch := make(chan *nats.Msg)
	for _, s := range subjects {
		_, err := client.Subscribe(s, ch)
		assert.ErrorIs(err, nats.ErrBadSubject, "Expected error for subject %s", s)
	}
}

func TestNatsClient_BadSubjects(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		_, _, client := CreateLocalNatsClientForTest(t)

		testNatsClient_BadSubjects(t, client)
	})
}

func TestNatsClient_MaxReconnects(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)
		reconnectWait := time.Millisecond
		server, port, client := CreateLocalNatsClientForTest(t,
			nats.ReconnectWait(reconnectWait),
			nats.ReconnectJitter(0, 0),
		)
		c, ok := client.(*natsClient)
		require.True(ok, "wrong class: %T", client)
		require.True(c.conn.IsConnected(), "not connected initially")
		assert.Equal(server.ID(), c.conn.ConnectedServerId())

		server.Shutdown()
		server.WaitForShutdown()

		// The NATS client tries to reconnect a maximum of 100 times by default.
		time.Sleep(100 * reconnectWait)
		for i := 0; i < 1000 && c.conn.IsConnected(); i++ {
			time.Sleep(time.Millisecond)
		}
		require.False(c.conn.IsConnected(), "should be disconnected after server shutdown")

		server, _ = startLocalNatsServerPort(t, port)

		// Wait for automatic reconnection
		for i := 0; i < 1000 && !c.conn.IsConnected(); i++ {
			time.Sleep(time.Millisecond)
		}
		require.True(c.conn.IsConnected(), "not connected after restart")
		assert.Equal(server.ID(), c.conn.ConnectedServerId())
	})
}
