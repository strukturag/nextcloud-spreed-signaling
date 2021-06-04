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
	"encoding/json"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
)

type LoopbackNatsClient struct {
	mu            sync.Mutex
	subscriptions map[string]map[*loopbackNatsSubscription]bool
}

func NewLoopbackNatsClient() (NatsClient, error) {
	return &LoopbackNatsClient{
		subscriptions: make(map[string]map[*loopbackNatsSubscription]bool),
	}, nil
}

func (c *LoopbackNatsClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, subs := range c.subscriptions {
		for sub := range subs {
			sub.Unsubscribe() // nolint
		}
	}

	c.subscriptions = nil
}

type loopbackNatsSubscription struct {
	subject  string
	client   *LoopbackNatsClient
	ch       chan *nats.Msg
	incoming []*nats.Msg
	cond     sync.Cond
	quit     bool
}

func (s *loopbackNatsSubscription) Unsubscribe() error {
	s.cond.L.Lock()
	if !s.quit {
		s.quit = true
		s.cond.Signal()
	}
	s.cond.L.Unlock()

	s.client.unsubscribe(s)
	return nil
}

func (s *loopbackNatsSubscription) queue(msg *nats.Msg) {
	s.cond.L.Lock()
	s.incoming = append(s.incoming, msg)
	if len(s.incoming) == 1 {
		s.cond.Signal()
	}
	s.cond.L.Unlock()
}

func (s *loopbackNatsSubscription) run() {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	for !s.quit {
		for !s.quit && len(s.incoming) == 0 {
			s.cond.Wait()
		}

		for !s.quit && len(s.incoming) > 0 {
			msg := s.incoming[0]
			s.incoming = s.incoming[1:]
			s.cond.L.Unlock()
			s.ch <- msg
			s.cond.L.Lock()
		}
	}
}

func (c *LoopbackNatsClient) Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.subscribe(subject, ch)
}

func (c *LoopbackNatsClient) subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error) {
	if strings.HasSuffix(subject, ".") || strings.Contains(subject, " ") {
		return nil, nats.ErrBadSubject
	}

	if c.subscriptions == nil {
		return nil, nats.ErrConnectionClosed
	}

	s := &loopbackNatsSubscription{
		subject: subject,
		client:  c,
		ch:      ch,
	}
	s.cond.L = &sync.Mutex{}
	subs, found := c.subscriptions[subject]
	if !found {
		subs = make(map[*loopbackNatsSubscription]bool)
		c.subscriptions[subject] = subs
	}
	subs[s] = true

	go s.run()
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

func (c *LoopbackNatsClient) Publish(subject string, message interface{}) error {
	if strings.HasSuffix(subject, ".") || strings.Contains(subject, " ") {
		return nats.ErrBadSubject
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions == nil {
		return nats.ErrConnectionClosed
	}

	if subs, found := c.subscriptions[subject]; found {
		msg := &nats.Msg{
			Subject: subject,
		}
		var err error
		if msg.Data, err = json.Marshal(message); err != nil {
			return err
		}
		for s := range subs {
			s.queue(msg)
		}
	}
	return nil
}

func (c *LoopbackNatsClient) PublishNats(subject string, message *NatsMessage) error {
	return c.Publish(subject, message)
}

func (c *LoopbackNatsClient) PublishMessage(subject string, message *ServerMessage) error {
	msg := &NatsMessage{
		Type:    "message",
		Message: message,
	}
	return c.PublishNats(subject, msg)
}

func (c *LoopbackNatsClient) PublishBackendServerRoomRequest(subject string, message *BackendServerRoomRequest) error {
	msg := &NatsMessage{
		Type: "room",
		Room: message,
	}
	return c.PublishNats(subject, msg)
}

func (c *LoopbackNatsClient) Decode(msg *nats.Msg, v interface{}) error {
	return json.Unmarshal(msg.Data, v)
}
