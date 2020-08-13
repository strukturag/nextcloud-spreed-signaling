/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
package main

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/context"

	"signaling"
)

const (
	// Sessions expire if they have not been used for one minute.
	sessionExpirationTime = time.Minute
)

type ProxySession struct {
	proxy    *ProxyServer
	id       string
	sid      uint64
	lastUsed int64

	clientLock      sync.Mutex
	client          *ProxyClient
	pendingMessages []*signaling.ProxyServerMessage

	publishersLock sync.Mutex
	publishers     map[string]signaling.McuPublisher
	publisherIds   map[string]string

	subscribersLock sync.Mutex
	subscribers     map[string]signaling.McuSubscriber
	subscriberIds   map[string]string
}

func NewProxySession(proxy *ProxyServer, sid uint64, id string) *ProxySession {
	return &ProxySession{
		proxy:    proxy,
		id:       id,
		sid:      sid,
		lastUsed: time.Now().UnixNano(),

		publishers:   make(map[string]signaling.McuPublisher),
		publisherIds: make(map[string]string),

		subscribers:   make(map[string]signaling.McuSubscriber),
		subscriberIds: make(map[string]string),
	}
}

func (s *ProxySession) PublicId() string {
	return s.id
}

func (s *ProxySession) Sid() uint64 {
	return s.sid
}

func (s *ProxySession) LastUsed() time.Time {
	lastUsed := atomic.LoadInt64(&s.lastUsed)
	return time.Unix(0, lastUsed)
}

func (s *ProxySession) IsExpired() bool {
	expiresAt := s.LastUsed().Add(sessionExpirationTime)
	return expiresAt.Before(time.Now())
}

func (s *ProxySession) MarkUsed() {
	now := time.Now()
	atomic.StoreInt64(&s.lastUsed, now.UnixNano())
}

func (s *ProxySession) SetClient(client *ProxyClient) *ProxyClient {
	s.clientLock.Lock()
	prev := s.client
	s.client = client
	var messages []*signaling.ProxyServerMessage
	if client != nil {
		messages, s.pendingMessages = s.pendingMessages, nil
	}
	s.clientLock.Unlock()
	if prev != nil {
		prev.SetSession(nil)
	}
	if client != nil {
		s.MarkUsed()
		client.SetSession(s)
		for _, msg := range messages {
			client.SendMessage(msg)
		}
	}
	return prev
}

func (s *ProxySession) OnIceCandidate(client signaling.McuClient, candidate interface{}) {
	id := s.proxy.GetClientId(client)
	if id == "" {
		log.Printf("Received candidate %+v from unknown %s client %s (%+v)", candidate, client.StreamType(), client.Id(), client)
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "payload",
		Payload: &signaling.PayloadProxyServerMessage{
			Type:     "candidate",
			ClientId: id,
			Payload: map[string]interface{}{
				"candidate": candidate,
			},
		},
	}
	s.sendMessage(msg)
}

func (s *ProxySession) sendMessage(message *signaling.ProxyServerMessage) {
	var client *ProxyClient
	s.clientLock.Lock()
	client = s.client
	if client == nil {
		s.pendingMessages = append(s.pendingMessages, message)
	}
	s.clientLock.Unlock()
	if client != nil {
		client.SendMessage(message)
	}
}

func (s *ProxySession) OnIceCompleted(client signaling.McuClient) {
	id := s.proxy.GetClientId(client)
	if id == "" {
		log.Printf("Received ice completed event from unknown %s client %s (%+v)", client.StreamType(), client.Id(), client)
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type:     "ice-completed",
			ClientId: id,
		},
	}
	s.sendMessage(msg)
}

func (s *ProxySession) PublisherClosed(publisher signaling.McuPublisher) {
	if id := s.DeletePublisher(publisher); id != "" {
		s.proxy.DeleteClient(id, publisher)

		msg := &signaling.ProxyServerMessage{
			Type: "event",
			Event: &signaling.EventProxyServerMessage{
				Type:     "publisher-closed",
				ClientId: id,
			},
		}
		s.sendMessage(msg)
	}
}

func (s *ProxySession) SubscriberClosed(subscriber signaling.McuSubscriber) {
	if id := s.DeleteSubscriber(subscriber); id != "" {
		s.proxy.DeleteClient(id, subscriber)

		msg := &signaling.ProxyServerMessage{
			Type: "event",
			Event: &signaling.EventProxyServerMessage{
				Type:     "subscriber-closed",
				ClientId: id,
			},
		}
		s.sendMessage(msg)
	}
}

func (s *ProxySession) StorePublisher(ctx context.Context, id string, publisher signaling.McuPublisher) {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	s.publishers[id] = publisher
	s.publisherIds[publisher.Id()] = id
}

func (s *ProxySession) DeletePublisher(publisher signaling.McuPublisher) string {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	id, found := s.publisherIds[publisher.Id()]
	if !found {
		return ""
	}

	delete(s.publishers, id)
	delete(s.publisherIds, publisher.Id())
	return id
}

func (s *ProxySession) StoreSubscriber(ctx context.Context, id string, subscriber signaling.McuSubscriber) {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	s.subscribers[id] = subscriber
	s.subscriberIds[subscriber.Id()] = id
}

func (s *ProxySession) DeleteSubscriber(subscriber signaling.McuSubscriber) string {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	id, found := s.subscriberIds[subscriber.Id()]
	if !found {
		return ""
	}

	delete(s.subscribers, id)
	delete(s.subscriberIds, subscriber.Id())
	return id
}

func (s *ProxySession) clearPublishers() {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	go func(publishers map[string]signaling.McuPublisher) {
		for _, publisher := range publishers {
			publisher.Close(context.Background())
		}
	}(s.publishers)
	s.publishers = make(map[string]signaling.McuPublisher)
	s.publisherIds = make(map[string]string)
}

func (s *ProxySession) clearSubscribers() {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	go func(subscribers map[string]signaling.McuSubscriber) {
		for _, subscriber := range subscribers {
			subscriber.Close(context.Background())
		}
	}(s.subscribers)
	s.subscribers = make(map[string]signaling.McuSubscriber)
	s.subscriberIds = make(map[string]string)
}

func (s *ProxySession) NotifyDisconnected() {
	s.clearPublishers()
	s.clearSubscribers()
}
