/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2018 struktur AG
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
	"bytes"
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/go-nats"
)

func (c *LoopbackNatsClient) waitForSubscriptionsEmpty(ctx context.Context, t *testing.T) {
	for {
		c.mu.Lock()
		count := len(c.subscriptions)
		c.mu.Unlock()
		if count == 0 {
			break
		}

		select {
		case <-ctx.Done():
			c.mu.Lock()
			t.Errorf("Error waiting for subscriptions %+v to terminate: %s", c.subscriptions, ctx.Err())
			c.mu.Unlock()
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func CreateLoopbackNatsClientForTest(t *testing.T) NatsClient {
	result, err := NewLoopbackNatsClient()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLoopbackNatsClient_Subscribe(t *testing.T) {
	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	base := runtime.NumGoroutine()

	client := CreateLoopbackNatsClientForTest(t)
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
		client.Publish("foo", []byte("hello"))
	}
	<-ch

	r := atomic.LoadInt32(&received)
	if r != max {
		t.Fatalf("Received wrong # of messages: %d vs %d", r, max)
	}
	quit <- true

	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	delta := (runtime.NumGoroutine() - base)
	if delta > 0 {
		t.Fatalf("%d Go routines still exist post Close()", delta)
	}
}

func TestLoopbackNatsClient_Request(t *testing.T) {
	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	base := runtime.NumGoroutine()

	client := CreateLoopbackNatsClientForTest(t)
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		msg := <-dest
		if err := client.Publish(msg.Reply, []byte("world")); err != nil {
			t.Error(err)
			return
		}
		if err := sub.Unsubscribe(); err != nil {
			t.Error("Unsubscribe failed with err:", err)
			return
		}
	}()
	reply, err := client.Request("foo", []byte("hello"), 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	var response []byte
	if err := client.Decode(reply, &response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, []byte("world")) {
		t.Fatalf("expected 'world', got '%s'", string(reply.Data))
	}

	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	delta := (runtime.NumGoroutine() - base)
	if delta > 0 {
		t.Fatalf("%d Go routines still exist post Close()", delta)
	}
}

func TestLoopbackNatsClient_RequestTimeout(t *testing.T) {
	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	base := runtime.NumGoroutine()

	client := CreateLoopbackNatsClientForTest(t)
	dest := make(chan *nats.Msg)
	sub, err := client.Subscribe("foo", dest)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		msg := <-dest
		time.Sleep(200 * time.Millisecond)
		if err := client.Publish(msg.Reply, []byte("world")); err != nil {
			t.Error(err)
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

	// Give time for things to settle before capturing the number of
	// go routines
	time.Sleep(500 * time.Millisecond)

	delta := (runtime.NumGoroutine() - base)
	if delta > 0 {
		t.Fatalf("%d Go routines still exist post Close()", delta)
	}
}

func TestLoopbackNatsClient_RequestTimeoutNoReply(t *testing.T) {
	client := CreateLoopbackNatsClientForTest(t)
	timeout := 100 * time.Millisecond
	start := time.Now()
	reply, err := client.Request("foo", []byte("hello"), timeout)
	end := time.Now()
	if err == nil {
		t.Fatalf("Request should have timed out, reeived %+v", reply)
	} else if err != nats.ErrTimeout {
		t.Fatalf("Request should have timed out, received error %s", err)
	}
	if end.Sub(start) < timeout {
		t.Errorf("Expected a delay of %s but had %s", timeout, end.Sub(start))
	}
}
