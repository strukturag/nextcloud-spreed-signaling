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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		result.Close()
	})
	return result
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
	ready := make(chan bool)
	quit := make(chan bool)
	go func() {
		ready <- true
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
	<-ready
	for i := int32(0); i < max; i++ {
		if err := client.Publish("foo", []byte("hello")); err != nil {
			t.Error(err)
		}

		// Allow NATS goroutines to process messages.
		time.Sleep(10 * time.Millisecond)
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
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_Subscribe(t, client)
	})
}

func testNatsClient_PublishAfterClose(t *testing.T, client NatsClient) {
	client.Close()

	if err := client.Publish("foo", "bar"); err != nats.ErrConnectionClosed {
		t.Errorf("Expected %v, got %v", nats.ErrConnectionClosed, err)
	}
}

func TestNatsClient_PublishAfterClose(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_PublishAfterClose(t, client)
	})
}

func testNatsClient_SubscribeAfterClose(t *testing.T, client NatsClient) {
	client.Close()

	ch := make(chan *nats.Msg)
	if _, err := client.Subscribe("foo", ch); err != nats.ErrConnectionClosed {
		t.Errorf("Expected %v, got %v", nats.ErrConnectionClosed, err)
	}
}

func TestNatsClient_SubscribeAfterClose(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_SubscribeAfterClose(t, client)
	})
}

func testNatsClient_BadSubjects(t *testing.T, client NatsClient) {
	subjects := []string{
		"foo bar",
		"foo.",
	}

	ch := make(chan *nats.Msg)
	for _, s := range subjects {
		if _, err := client.Subscribe(s, ch); err != nats.ErrBadSubject {
			t.Errorf("Expected %v for subject %s, got %v", nats.ErrBadSubject, s, err)
		}
	}
}

func TestNatsClient_BadSubjects(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client := CreateLocalNatsClientForTest(t)

		testNatsClient_BadSubjects(t, client)
	})
}
