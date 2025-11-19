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
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	// Sessions expire if they have not been used for one minute.
	sessionExpirationTime = time.Minute
)

type remotePublisherData struct {
	id       signaling.PublicSessionId
	hostname string
	port     int
	rtcpPort int
}

type ProxySession struct {
	logger    signaling.Logger
	proxy     *ProxyServer
	id        signaling.PublicSessionId
	sid       uint64
	lastUsed  atomic.Int64
	ctx       context.Context
	closeFunc context.CancelFunc

	clientLock sync.Mutex
	// +checklocks:clientLock
	client *ProxyClient
	// +checklocks:clientLock
	pendingMessages []*signaling.ProxyServerMessage

	publishersLock sync.Mutex
	// +checklocks:publishersLock
	publishers map[string]signaling.McuPublisher
	// +checklocks:publishersLock
	publisherIds map[signaling.McuPublisher]string

	subscribersLock sync.Mutex
	// +checklocks:subscribersLock
	subscribers map[string]signaling.McuSubscriber
	// +checklocks:subscribersLock
	subscriberIds map[signaling.McuSubscriber]string

	remotePublishersLock sync.Mutex
	// +checklocks:remotePublishersLock
	remotePublishers map[signaling.McuRemoteAwarePublisher]map[string]*remotePublisherData
}

func NewProxySession(proxy *ProxyServer, sid uint64, id signaling.PublicSessionId) *ProxySession {
	ctx, closeFunc := context.WithCancel(context.Background())
	result := &ProxySession{
		logger:    proxy.logger,
		proxy:     proxy,
		id:        id,
		sid:       sid,
		ctx:       ctx,
		closeFunc: closeFunc,

		publishers:   make(map[string]signaling.McuPublisher),
		publisherIds: make(map[signaling.McuPublisher]string),

		subscribers:   make(map[string]signaling.McuSubscriber),
		subscriberIds: make(map[signaling.McuSubscriber]string),
	}
	result.MarkUsed()
	return result
}

func (s *ProxySession) Context() context.Context {
	return s.ctx
}

func (s *ProxySession) PublicId() signaling.PublicSessionId {
	return s.id
}

func (s *ProxySession) Sid() uint64 {
	return s.sid
}

func (s *ProxySession) LastUsed() time.Time {
	lastUsed := s.lastUsed.Load()
	return time.Unix(0, lastUsed)
}

func (s *ProxySession) IsExpired() bool {
	expiresAt := s.LastUsed().Add(sessionExpirationTime)
	return expiresAt.Before(time.Now())
}

func (s *ProxySession) MarkUsed() {
	now := time.Now()
	s.lastUsed.Store(now.UnixNano())
}

func (s *ProxySession) Close() {
	prev := s.SetClient(nil)
	if prev != nil {
		reason := "session_closed"
		if s.IsExpired() {
			reason = "session_expired"
		}
		prev.SendMessage(&signaling.ProxyServerMessage{
			Type: "bye",
			Bye: &signaling.ByeProxyServerMessage{
				Reason: reason,
			},
		})
	}

	s.closeFunc()
	s.clearPublishers()
	s.clearSubscribers()
	s.clearRemotePublishers()
	s.proxy.DeleteSession(s.Sid())
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

func (s *ProxySession) OnUpdateOffer(client signaling.McuClient, offer api.StringMap) {
	id := s.proxy.GetClientId(client)
	if id == "" {
		s.logger.Printf("Received offer %+v from unknown %s client %s (%+v)", offer, client.StreamType(), client.Id(), client)
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "payload",
		Payload: &signaling.PayloadProxyServerMessage{
			Type:     "offer",
			ClientId: id,
			Payload: api.StringMap{
				"offer": offer,
			},
		},
	}
	s.sendMessage(msg)
}

func (s *ProxySession) OnIceCandidate(client signaling.McuClient, candidate any) {
	id := s.proxy.GetClientId(client)
	if id == "" {
		s.logger.Printf("Received candidate %+v from unknown %s client %s (%+v)", candidate, client.StreamType(), client.Id(), client)
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "payload",
		Payload: &signaling.PayloadProxyServerMessage{
			Type:     "candidate",
			ClientId: id,
			Payload: api.StringMap{
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
		s.logger.Printf("Received ice completed event from unknown %s client %s (%+v)", client.StreamType(), client.Id(), client)
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

func (s *ProxySession) SubscriberSidUpdated(subscriber signaling.McuSubscriber) {
	id := s.proxy.GetClientId(subscriber)
	if id == "" {
		s.logger.Printf("Received subscriber sid updated event from unknown %s subscriber %s (%+v)", subscriber.StreamType(), subscriber.Id(), subscriber)
		return
	}

	msg := &signaling.ProxyServerMessage{
		Type: "event",
		Event: &signaling.EventProxyServerMessage{
			Type:     "subscriber-sid-updated",
			ClientId: id,
			Sid:      subscriber.Sid(),
		},
	}
	s.sendMessage(msg)
}

func (s *ProxySession) PublisherClosed(publisher signaling.McuPublisher) {
	if id := s.DeletePublisher(publisher); id != "" {
		if s.proxy.DeleteClient(id, publisher) {
			statsPublishersCurrent.WithLabelValues(string(publisher.StreamType())).Dec()
		}

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
		if s.proxy.DeleteClient(id, subscriber) {
			statsSubscribersCurrent.WithLabelValues(string(subscriber.StreamType())).Dec()
		}

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
	s.publisherIds[publisher] = id
}

func (s *ProxySession) DeletePublisher(publisher signaling.McuPublisher) string {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	id, found := s.publisherIds[publisher]
	if !found {
		return ""
	}

	delete(s.publishers, id)
	delete(s.publisherIds, publisher)
	if rp, ok := publisher.(signaling.McuRemoteAwarePublisher); ok {
		s.remotePublishersLock.Lock()
		defer s.remotePublishersLock.Unlock()
		delete(s.remotePublishers, rp)
	}
	go s.proxy.PublisherDeleted(publisher)
	return id
}

func (s *ProxySession) StoreSubscriber(ctx context.Context, id string, subscriber signaling.McuSubscriber) {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	s.subscribers[id] = subscriber
	s.subscriberIds[subscriber] = id
}

func (s *ProxySession) DeleteSubscriber(subscriber signaling.McuSubscriber) string {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	id, found := s.subscriberIds[subscriber]
	if !found {
		return ""
	}

	delete(s.subscribers, id)
	delete(s.subscriberIds, subscriber)
	return id
}

func (s *ProxySession) clearPublishers() {
	s.publishersLock.Lock()
	defer s.publishersLock.Unlock()

	go func(publishers map[string]signaling.McuPublisher) {
		for id, publisher := range publishers {
			if s.proxy.DeleteClient(id, publisher) {
				statsPublishersCurrent.WithLabelValues(string(publisher.StreamType())).Dec()
			}
			publisher.Close(context.Background())
		}
	}(s.publishers)
	// Can't use clear(...) here as the map is processed by the goroutine above.
	s.publishers = make(map[string]signaling.McuPublisher)
	clear(s.publisherIds)
}

func (s *ProxySession) clearRemotePublishers() {
	s.remotePublishersLock.Lock()
	defer s.remotePublishersLock.Unlock()

	go func(remotePublishers map[signaling.McuRemoteAwarePublisher]map[string]*remotePublisherData) {
		for publisher, entries := range remotePublishers {
			for _, data := range entries {
				if err := publisher.UnpublishRemote(context.Background(), s.PublicId(), data.hostname, data.port, data.rtcpPort); err != nil {
					s.logger.Printf("Error unpublishing %s %s from remote %s: %s", publisher.StreamType(), publisher.Id(), data.hostname, err)
				}
			}
		}
	}(s.remotePublishers)
	s.remotePublishers = nil
}

func (s *ProxySession) clearSubscribers() {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	go func(subscribers map[string]signaling.McuSubscriber) {
		for id, subscriber := range subscribers {
			if s.proxy.DeleteClient(id, subscriber) {
				statsSubscribersCurrent.WithLabelValues(string(subscriber.StreamType())).Dec()
			}
			subscriber.Close(context.Background())
		}
	}(s.subscribers)
	// Can't use clear(...) here as the map is processed by the goroutine above.
	s.subscribers = make(map[string]signaling.McuSubscriber)
	clear(s.subscriberIds)
}

func (s *ProxySession) NotifyDisconnected() {
	s.clearPublishers()
	s.clearSubscribers()
	s.clearRemotePublishers()
}

func (s *ProxySession) AddRemotePublisher(publisher signaling.McuRemoteAwarePublisher, hostname string, port int, rtcpPort int) bool {
	s.remotePublishersLock.Lock()
	defer s.remotePublishersLock.Unlock()

	remote, found := s.remotePublishers[publisher]
	if !found {
		remote = make(map[string]*remotePublisherData)
		if s.remotePublishers == nil {
			s.remotePublishers = make(map[signaling.McuRemoteAwarePublisher]map[string]*remotePublisherData)
		}
		s.remotePublishers[publisher] = remote
	}

	key := fmt.Sprintf("%s:%d%d", hostname, port, rtcpPort)
	if _, found := remote[key]; found {
		return false
	}

	data := &remotePublisherData{
		id:       publisher.PublisherId(),
		hostname: hostname,
		port:     port,
		rtcpPort: rtcpPort,
	}
	remote[key] = data
	return true
}

func (s *ProxySession) RemoveRemotePublisher(publisher signaling.McuRemoteAwarePublisher, hostname string, port int, rtcpPort int) {
	s.remotePublishersLock.Lock()
	defer s.remotePublishersLock.Unlock()

	remote, found := s.remotePublishers[publisher]
	if !found {
		return
	}

	key := fmt.Sprintf("%s:%d%d", hostname, port, rtcpPort)
	delete(remote, key)
	if len(remote) == 0 {
		delete(s.remotePublishers, publisher)
		if len(s.remotePublishers) == 0 {
			s.remotePublishers = nil
		}
	}
}

func (s *ProxySession) OnPublisherDeleted(publisher signaling.McuPublisher) {
	if publisher, ok := publisher.(signaling.McuRemoteAwarePublisher); ok {
		s.OnRemoteAwarePublisherDeleted(publisher)
	}
}

func (s *ProxySession) OnRemoteAwarePublisherDeleted(publisher signaling.McuRemoteAwarePublisher) {
	s.remotePublishersLock.Lock()
	defer s.remotePublishersLock.Unlock()

	if entries, found := s.remotePublishers[publisher]; found {
		delete(s.remotePublishers, publisher)

		for _, entry := range entries {
			msg := &signaling.ProxyServerMessage{
				Type: "event",
				Event: &signaling.EventProxyServerMessage{
					Type:     "publisher-closed",
					ClientId: string(entry.id),
				},
			}
			s.sendMessage(msg)
		}
	}
}

func (s *ProxySession) OnRemotePublisherDeleted(publisherId signaling.PublicSessionId) {
	s.subscribersLock.Lock()
	defer s.subscribersLock.Unlock()

	for id, sub := range s.subscribers {
		if sub.Publisher() == publisherId {
			delete(s.subscribers, id)
			delete(s.subscriberIds, sub)

			s.logger.Printf("Remote subscriber %s was closed, closing %s subscriber %s", publisherId, sub.StreamType(), sub.Id())
			go sub.Close(context.Background())
		}
	}
}
