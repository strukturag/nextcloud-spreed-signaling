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

func testNatsClient_Request(t *testing.T, client NatsClient) {
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		msg := <-dest
		if err := client.Publish(msg.Reply, "world"); err != nil {
			t.Error(err)
			return
		}
		if err := sub.Unsubscribe(); err != nil {
			t.Error("Unsubscribe failed with err:", err)
			return
		}
	}()
	reply, err := client.Request("foo", []byte("hello"), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	var response string
	if err := client.Decode(reply, &response); err != nil {
		t.Fatal(err)
	}
	if response != "world" {
		t.Fatalf("expected 'world', got '%s'", string(reply.Data))
	}
}

func TestNatsClient_Request(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client, shutdown := CreateLocalNatsClientForTest(t)
		defer shutdown()

		testNatsClient_Request(t, client)
	})
}

func testNatsClient_RequestTimeout(t *testing.T, client NatsClient) {
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		msg := <-dest
		time.Sleep(200 * time.Millisecond)
		if err := client.Publish(msg.Reply, []byte("world")); err != nil {
			if err != nats.ErrConnectionClosed {
				t.Error(err)
			}
			return
		}
		if err := sub.Unsubscribe(); err != nil {
			t.Error("Unsubscribe failed with err:", err)
			return
		}
	}()
	reply, err := client.Request("foo", []byte("hello"), 100*time.Millisecond)
	if err == nil {
		t.Fatalf("Request should have timed out, reeived %+v", reply)
	} else if err != nats.ErrTimeout {
		t.Fatalf("Request should have timed out, received error %s", err)
	}
}

func TestNatsClient_RequestTimeout(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client, shutdown := CreateLocalNatsClientForTest(t)
		defer shutdown()

		testNatsClient_RequestTimeout(t, client)
	})
}

func testNatsClient_RequestNoReply(t *testing.T, client NatsClient) {
	timeout := 100 * time.Millisecond
	start := time.Now()
	reply, err := client.Request("foo", []byte("hello"), timeout)
	end := time.Now()
	if err == nil {
		t.Fatalf("Request should have failed without responsers, reeived %+v", reply)
	} else if err != nats.ErrNoResponders {
		t.Fatalf("Request should have failed without responsers, received error %s", err)
	}
	if end.Sub(start) >= timeout {
		t.Errorf("Should have failed immediately but took %s", end.Sub(start))
	}
}

func TestNatsClient_RequestNoReply(t *testing.T) {
	ensureNoGoroutinesLeak(t, func() {
		client, shutdown := CreateLocalNatsClientForTest(t)
		defer shutdown()

		testNatsClient_RequestNoReply(t, client)
	})
}
