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
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	natsserver "github.com/nats-io/nats-server/v2/test"
)

func startLocalNatsServer(t *testing.T) string {
	opts := natsserver.DefaultTestOptions
	opts.Port = -1
	opts.Cluster.Name = "testing"
	srv := natsserver.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv.ClientURL()
}

func CreateLocalNatsClientForTest(t *testing.T) NatsClient {
	url := startLocalNatsServer(t)
	result, err := NewNatsClient(url)
	require.NoError(t, err)
	t.Cleanup(func() {
		result.Close()
	})
	return result
}

func testNatsClient_Subscribe(t *testing.T, client NatsClient) {
	require := require.New(t)
	assert := assert.New(t)
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	require.NoError(err)
	ch := make(chan struct{})

	var received atomic.Int32
	max := int32(20)
	ready := make(chan struct{})
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		close(ready)
		for {
			select {
			case <-dest:
				total := received.Add(1)
				if total == max {
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
	for i := int32(0); i < max; i++ {
		assert.NoError(client.Publish("foo", []byte("hello")))

		// Allow NATS goroutines to process messages.
		time.Sleep(10 * time.Millisecond)
	}
	<-ch

	require.EqualValues(max, received.Load(), "Received wrong # of messages")
}

func TestNatsClient_Subscribe(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_Subscribe(t, client)
	})
}

func testNatsClient_PublishAfterClose(t *testing.T, client NatsClient) {
	client.Close()

	assert.ErrorIs(t, client.Publish("foo", "bar"), nats.ErrConnectionClosed)
}

func TestNatsClient_PublishAfterClose(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_PublishAfterClose(t, client)
	})
}

func testNatsClient_SubscribeAfterClose(t *testing.T, client NatsClient) {
	client.Close()

	ch := make(chan *nats.Msg)
	_, err := client.Subscribe("foo", ch)
	assert.ErrorIs(t, err, nats.ErrConnectionClosed)
}

func TestNatsClient_SubscribeAfterClose(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLocalNatsClientForTest(t)

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

func TestNatsClient_BadSubjects(t *testing.T) {
	CatchLogForTest(t)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_BadSubjects(t, client)
	})
}
