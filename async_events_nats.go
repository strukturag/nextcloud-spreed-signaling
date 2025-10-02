/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

func GetSubjectForBackendRoomId(roomId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("backend.room", roomId)
	}

	return GetEncodedSubject("backend.room", roomId+"|"+backend.Id())
}

func GetSubjectForRoomId(roomId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("room", roomId)
	}

	return GetEncodedSubject("room", roomId+"|"+backend.Id())
}

func GetSubjectForUserId(userId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("user", userId)
	}

	return GetEncodedSubject("user", userId+"|"+backend.Id())
}

func GetSubjectForSessionId(sessionId PublicSessionId, backend *Backend) string {
	return fmt.Sprintf("session.%s", sessionId)
}

type asyncSubscriberNats struct {
	key    string
	client NatsClient

	receiver     chan *nats.Msg
	closeChan    chan struct{}
	subscription NatsSubscription

	processMessage func(*nats.Msg)
}

func newAsyncSubscriberNats(key string, client NatsClient) (*asyncSubscriberNats, error) {
	receiver := make(chan *nats.Msg, 64)
	sub, err := client.Subscribe(key, receiver)
	if err != nil {
		return nil, err
	}

	result := &asyncSubscriberNats{
		key:    key,
		client: client,

		receiver:     receiver,
		closeChan:    make(chan struct{}),
		subscription: sub,
	}
	return result, nil
}

func (s *asyncSubscriberNats) run() {
	defer func() {
		if err := s.subscription.Unsubscribe(); err != nil {
			log.Printf("Error unsubscribing %s: %s", s.key, err)
		}
	}()

	for {
		select {
		case msg := <-s.receiver:
			s.processMessage(msg)
			for count := len(s.receiver); count > 0; count-- {
				s.processMessage(<-s.receiver)
			}
		case <-s.closeChan:
			return
		}
	}
}

func (s *asyncSubscriberNats) close() {
	close(s.closeChan)
}

type asyncBackendRoomSubscriberNats struct {
	*asyncSubscriberNats
	asyncBackendRoomSubscriber
}

func newAsyncBackendRoomSubscriberNats(key string, client NatsClient) (*asyncBackendRoomSubscriberNats, error) {
	sub, err := newAsyncSubscriberNats(key, client)
	if err != nil {
		return nil, err
	}

	result := &asyncBackendRoomSubscriberNats{
		asyncSubscriberNats: sub,
	}
	result.processMessage = result.doProcessMessage
	go result.run()
	return result, nil
}

func (s *asyncBackendRoomSubscriberNats) doProcessMessage(msg *nats.Msg) {
	var message AsyncMessage
	if err := s.client.Decode(msg, &message); err != nil {
		log.Printf("Could not decode NATS message %+v, %s", msg, err)
		return
	}

	s.processBackendRoomRequest(&message)
}

type asyncRoomSubscriberNats struct {
	asyncRoomSubscriber
	*asyncSubscriberNats
}

func newAsyncRoomSubscriberNats(key string, client NatsClient) (*asyncRoomSubscriberNats, error) {
	sub, err := newAsyncSubscriberNats(key, client)
	if err != nil {
		return nil, err
	}

	result := &asyncRoomSubscriberNats{
		asyncSubscriberNats: sub,
	}
	result.processMessage = result.doProcessMessage
	go result.run()
	return result, nil
}

func (s *asyncRoomSubscriberNats) doProcessMessage(msg *nats.Msg) {
	var message AsyncMessage
	if err := s.client.Decode(msg, &message); err != nil {
		log.Printf("Could not decode nats message %+v, %s", msg, err)
		return
	}

	s.processAsyncRoomMessage(&message)
}

type asyncUserSubscriberNats struct {
	*asyncSubscriberNats
	asyncUserSubscriber
}

func newAsyncUserSubscriberNats(key string, client NatsClient) (*asyncUserSubscriberNats, error) {
	sub, err := newAsyncSubscriberNats(key, client)
	if err != nil {
		return nil, err
	}

	result := &asyncUserSubscriberNats{
		asyncSubscriberNats: sub,
	}
	result.processMessage = result.doProcessMessage
	go result.run()
	return result, nil
}

func (s *asyncUserSubscriberNats) doProcessMessage(msg *nats.Msg) {
	var message AsyncMessage
	if err := s.client.Decode(msg, &message); err != nil {
		log.Printf("Could not decode nats message %+v, %s", msg, err)
		return
	}

	s.processAsyncUserMessage(&message)
}

type asyncSessionSubscriberNats struct {
	*asyncSubscriberNats
	asyncSessionSubscriber
}

func newAsyncSessionSubscriberNats(key string, client NatsClient) (*asyncSessionSubscriberNats, error) {
	sub, err := newAsyncSubscriberNats(key, client)
	if err != nil {
		return nil, err
	}

	result := &asyncSessionSubscriberNats{
		asyncSubscriberNats: sub,
	}
	result.processMessage = result.doProcessMessage
	go result.run()
	return result, nil
}

func (s *asyncSessionSubscriberNats) doProcessMessage(msg *nats.Msg) {
	var message AsyncMessage
	if err := s.client.Decode(msg, &message); err != nil {
		log.Printf("Could not decode nats message %+v, %s", msg, err)
		return
	}

	s.processAsyncSessionMessage(&message)
}

type asyncEventsNats struct {
	mu     sync.Mutex
	client NatsClient

	// +checklocks:mu
	backendRoomSubscriptions map[string]*asyncBackendRoomSubscriberNats
	// +checklocks:mu
	roomSubscriptions map[string]*asyncRoomSubscriberNats
	// +checklocks:mu
	userSubscriptions map[string]*asyncUserSubscriberNats
	// +checklocks:mu
	sessionSubscriptions map[string]*asyncSessionSubscriberNats
}

func NewAsyncEventsNats(client NatsClient) (AsyncEvents, error) {
	events := &asyncEventsNats{
		client: client,

		backendRoomSubscriptions: make(map[string]*asyncBackendRoomSubscriberNats),
		roomSubscriptions:        make(map[string]*asyncRoomSubscriberNats),
		userSubscriptions:        make(map[string]*asyncUserSubscriberNats),
		sessionSubscriptions:     make(map[string]*asyncSessionSubscriberNats),
	}
	return events, nil
}

func (e *asyncEventsNats) GetServerInfoNats() *BackendServerInfoNats {
	var nats *BackendServerInfoNats
	switch n := e.client.(type) {
	case *natsClient:
		nats = &BackendServerInfoNats{
			Urls: n.conn.Servers(),
		}
		if c := n.conn; c.IsConnected() {
			nats.Connected = true
			nats.ServerUrl = c.ConnectedUrl()
			nats.ServerID = c.ConnectedServerId()
			nats.ServerVersion = c.ConnectedServerVersion()
			nats.ClusterName = c.ConnectedClusterName()
		}
	case *LoopbackNatsClient:
		nats = &BackendServerInfoNats{
			Urls:      []string{NatsLoopbackUrl},
			Connected: true,
			ServerUrl: NatsLoopbackUrl,
		}
	}

	return nats
}

func (e *asyncEventsNats) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func(subscriptions map[string]*asyncBackendRoomSubscriberNats) {
		defer wg.Done()
		for _, sub := range subscriptions {
			sub.close()
		}
	}(e.backendRoomSubscriptions)
	wg.Add(1)
	go func(subscriptions map[string]*asyncRoomSubscriberNats) {
		defer wg.Done()
		for _, sub := range subscriptions {
			sub.close()
		}
	}(e.roomSubscriptions)
	wg.Add(1)
	go func(subscriptions map[string]*asyncUserSubscriberNats) {
		defer wg.Done()
		for _, sub := range subscriptions {
			sub.close()
		}
	}(e.userSubscriptions)
	wg.Add(1)
	go func(subscriptions map[string]*asyncSessionSubscriberNats) {
		defer wg.Done()
		for _, sub := range subscriptions {
			sub.close()
		}
	}(e.sessionSubscriptions)
	// Can't use clear(...) here as the maps are processed asynchronously by the
	// goroutines above.
	e.backendRoomSubscriptions = make(map[string]*asyncBackendRoomSubscriberNats)
	e.roomSubscriptions = make(map[string]*asyncRoomSubscriberNats)
	e.userSubscriptions = make(map[string]*asyncUserSubscriberNats)
	e.sessionSubscriptions = make(map[string]*asyncSessionSubscriberNats)
	wg.Wait()
	e.client.Close()
}

func (e *asyncEventsNats) RegisterBackendRoomListener(roomId string, backend *Backend, listener AsyncBackendRoomEventListener) error {
	key := GetSubjectForBackendRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.backendRoomSubscriptions[key]
	if !found {
		var err error
		if sub, err = newAsyncBackendRoomSubscriberNats(key, e.client); err != nil {
			return err
		}

		e.backendRoomSubscriptions[key] = sub
	}
	sub.addListener(listener)
	return nil
}

func (e *asyncEventsNats) UnregisterBackendRoomListener(roomId string, backend *Backend, listener AsyncBackendRoomEventListener) {
	key := GetSubjectForBackendRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.backendRoomSubscriptions[key]
	if !found {
		return
	}

	if !sub.removeListener(listener) {
		delete(e.backendRoomSubscriptions, key)
		sub.close()
	}
}

func (e *asyncEventsNats) RegisterRoomListener(roomId string, backend *Backend, listener AsyncRoomEventListener) error {
	key := GetSubjectForRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.roomSubscriptions[key]
	if !found {
		var err error
		if sub, err = newAsyncRoomSubscriberNats(key, e.client); err != nil {
			return err
		}

		e.roomSubscriptions[key] = sub
	}
	sub.addListener(listener)
	return nil
}

func (e *asyncEventsNats) UnregisterRoomListener(roomId string, backend *Backend, listener AsyncRoomEventListener) {
	key := GetSubjectForRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.roomSubscriptions[key]
	if !found {
		return
	}

	if !sub.removeListener(listener) {
		delete(e.roomSubscriptions, key)
		sub.close()
	}
}

func (e *asyncEventsNats) RegisterUserListener(roomId string, backend *Backend, listener AsyncUserEventListener) error {
	key := GetSubjectForUserId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.userSubscriptions[key]
	if !found {
		var err error
		if sub, err = newAsyncUserSubscriberNats(key, e.client); err != nil {
			return err
		}

		e.userSubscriptions[key] = sub
	}
	sub.addListener(listener)
	return nil
}

func (e *asyncEventsNats) UnregisterUserListener(roomId string, backend *Backend, listener AsyncUserEventListener) {
	key := GetSubjectForUserId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.userSubscriptions[key]
	if !found {
		return
	}

	if !sub.removeListener(listener) {
		delete(e.userSubscriptions, key)
		sub.close()
	}
}

func (e *asyncEventsNats) RegisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncSessionEventListener) error {
	key := GetSubjectForSessionId(sessionId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.sessionSubscriptions[key]
	if !found {
		var err error
		if sub, err = newAsyncSessionSubscriberNats(key, e.client); err != nil {
			return err
		}

		e.sessionSubscriptions[key] = sub
	}
	sub.addListener(listener)
	return nil
}

func (e *asyncEventsNats) UnregisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncSessionEventListener) {
	key := GetSubjectForSessionId(sessionId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()
	sub, found := e.sessionSubscriptions[key]
	if !found {
		return
	}

	if !sub.removeListener(listener) {
		delete(e.sessionSubscriptions, key)
		sub.close()
	}
}

func (e *asyncEventsNats) publish(subject string, message *AsyncMessage) error {
	message.SendTime = time.Now()
	return e.client.Publish(subject, message)
}

func (e *asyncEventsNats) PublishBackendRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error {
	subject := GetSubjectForBackendRoomId(roomId, backend)
	return e.publish(subject, message)
}

func (e *asyncEventsNats) PublishRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error {
	subject := GetSubjectForRoomId(roomId, backend)
	return e.publish(subject, message)
}

func (e *asyncEventsNats) PublishUserMessage(userId string, backend *Backend, message *AsyncMessage) error {
	subject := GetSubjectForUserId(userId, backend)
	return e.publish(subject, message)
}

func (e *asyncEventsNats) PublishSessionMessage(sessionId PublicSessionId, backend *Backend, message *AsyncMessage) error {
	subject := GetSubjectForSessionId(sessionId, backend)
	return e.publish(subject, message)
}
