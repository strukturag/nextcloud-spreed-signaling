/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"container/list"
	"encoding/json"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
)

type LoopbackNatsClient struct {
	logger Logger

	mu sync.Mutex
	// +checklocks:mu
	subscriptions map[string]map[*loopbackNatsSubscription]bool

	// +checklocks:mu
	wakeup sync.Cond
	// +checklocks:mu
	incoming list.List
}

func NewLoopbackNatsClient(logger Logger) (NatsClient, error) {
	client := &LoopbackNatsClient{
		logger: logger,

		subscriptions: make(map[string]map[*loopbackNatsSubscription]bool),
	}
	client.wakeup.L = &client.mu
	go client.processMessages()
	return client, nil
}

func (c *LoopbackNatsClient) processMessages() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		for c.subscriptions != nil && c.incoming.Len() == 0 {
			c.wakeup.Wait()
		}
		if c.subscriptions == nil {
			// Client was closed.
			break
		}

		msg := c.incoming.Remove(c.incoming.Front()).(*nats.Msg)
		c.processMessage(msg)
	}
}

// +checklocks:c.mu
func (c *LoopbackNatsClient) processMessage(msg *nats.Msg) {
	subs, found := c.subscriptions[msg.Subject]
	if !found {
		return
	}

	channels := make([]chan *nats.Msg, 0, len(subs))
	for sub := range subs {
		channels = append(channels, sub.ch)
	}
	c.mu.Unlock()
	defer c.mu.Lock()
	for _, ch := range channels {
		select {
		case ch <- msg:
		default:
			c.logger.Printf("Slow consumer %s, dropping message", msg.Subject)
		}
	}
}

func (c *LoopbackNatsClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.subscriptions = nil
	c.incoming.Init()
	c.wakeup.Signal()
}

type loopbackNatsSubscription struct {
	subject string
	client  *LoopbackNatsClient

	ch chan *nats.Msg
}

func (s *loopbackNatsSubscription) Unsubscribe() error {
	s.client.unsubscribe(s)
	return nil
}

func (c *LoopbackNatsClient) Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error) {
	if strings.HasSuffix(subject, ".") || strings.Contains(subject, " ") {
		return nil, nats.ErrBadSubject
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions == nil {
		return nil, nats.ErrConnectionClosed
	}

	s := &loopbackNatsSubscription{
		subject: subject,
		client:  c,
		ch:      ch,
	}
	subs, found := c.subscriptions[subject]
	if !found {
		subs = make(map[*loopbackNatsSubscription]bool)
		c.subscriptions[subject] = subs
	}
	subs[s] = true

	return s, nil
}

func (c *LoopbackNatsClient) unsubscribe(s *loopbackNatsSubscription) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if subs, found := c.subscriptions[s.subject]; found {
		delete(subs, s)
		if len(subs) == 0 {
			delete(c.subscriptions, s.subject)
		}
	}
}

func (c *LoopbackNatsClient) Publish(subject string, message any) error {
	if strings.HasSuffix(subject, ".") || strings.Contains(subject, " ") {
		return nats.ErrBadSubject
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions == nil {
		return nats.ErrConnectionClosed
	}

	msg := &nats.Msg{
		Subject: subject,
	}
	var err error
	if msg.Data, err = json.Marshal(message); err != nil {
		return err
	}
	c.incoming.PushBack(msg)
	c.wakeup.Signal()
	return nil
}

func (c *LoopbackNatsClient) Decode(msg *nats.Msg, v any) error {
	return json.Unmarshal(msg.Data, v)
}
