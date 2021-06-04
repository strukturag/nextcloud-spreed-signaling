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

	natsserver "github.com/nats-io/nats-server/v2/test"
)

func startLocalNatsServer() (string, func()) {
	opts := natsserver.DefaultTestOptions
	opts.Port = -1
	opts.Cluster.Name = "testing"
	srv := natsserver.RunServer(&opts)
	shutdown := func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	}
	return srv.ClientURL(), shutdown
}

func CreateLocalNatsClientForTest(t *testing.T) (NatsClient, func()) {
	url, shutdown := startLocalNatsServer()
	result, err := NewNatsClient(url)
	if err != nil {
		t.Fatal(err)
	}
	return result, func() {
		result.Close()
		shutdown()
	}
}

func testNatsClient_Subscribe(t *testing.T, client NatsClient) {
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan bool)

	received := int32(0)
	max := int32(20)
	quit := make(chan bool)
	go func() {
		for {
			select {
			case <-dest:
				total := atomic.AddInt32(&received, 1)
				if total == max {
					err := sub.Unsubscribe()
					if err != nil {
						t.Errorf("Unsubscribe failed with err: %s", err)
						return
					}
					ch <- true
				}
			case <-quit:
				return
			}
		}
	}()
	for i := int32(0); i < max; i++ {
		if err := client.Publish("foo", []byte("hello")); err != nil {
			t.Error(err)
		}

		// Allow NATS goroutines to process messages.
		time.Sleep(time.Millisecond)
	}
	<-ch

	r := atomic.LoadInt32(&received)
	if r != max {
		t.Fatalf("Received wrong # of messages: %d vs %d", r, max)
	}
	quit <- true
}

func TestNatsClient_Subscribe(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client, shutdown := CreateLocalNatsClientForTest(t)
		defer shutdown()

		testNatsClient_Subscribe(t, client)
	})
}
