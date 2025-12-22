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
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
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
	return string("session." + sessionId)
}

type asyncEventsNatsSubscriptions map[string]map[AsyncEventListener]NatsSubscription

type asyncEventsNats struct {
	mu     sync.Mutex
	client NatsClient
	logger log.Logger // +checklocksignore

	// +checklocks:mu
	backendRoomSubscriptions asyncEventsNatsSubscriptions
	// +checklocks:mu
	roomSubscriptions asyncEventsNatsSubscriptions
	// +checklocks:mu
	userSubscriptions asyncEventsNatsSubscriptions
	// +checklocks:mu
	sessionSubscriptions asyncEventsNatsSubscriptions
}

func NewAsyncEventsNats(logger log.Logger, client NatsClient) (AsyncEvents, error) {
	events := &asyncEventsNats{
		client: client,
		logger: logger,

		backendRoomSubscriptions: make(asyncEventsNatsSubscriptions),
		roomSubscriptions:        make(asyncEventsNatsSubscriptions),
		userSubscriptions:        make(asyncEventsNatsSubscriptions),
		sessionSubscriptions:     make(asyncEventsNatsSubscriptions),
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

func closeSubscriptions(logger log.Logger, wg *sync.WaitGroup, subscriptions asyncEventsNatsSubscriptions) {
	defer wg.Done()

	for subject, subs := range subscriptions {
		for _, sub := range subs {
			if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
				logger.Printf("Error unsubscribing %s: %s", subject, err)
			}
		}
	}
}

func (e *asyncEventsNats) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go closeSubscriptions(e.logger, &wg, e.backendRoomSubscriptions)
	wg.Add(1)
	go closeSubscriptions(e.logger, &wg, e.roomSubscriptions)
	wg.Add(1)
	go closeSubscriptions(e.logger, &wg, e.userSubscriptions)
	wg.Add(1)
	go closeSubscriptions(e.logger, &wg, e.sessionSubscriptions)
	// Can't use clear(...) here as the maps are processed asynchronously by the
	// goroutines above.
	e.backendRoomSubscriptions = make(asyncEventsNatsSubscriptions)
	e.roomSubscriptions = make(asyncEventsNatsSubscriptions)
	e.userSubscriptions = make(asyncEventsNatsSubscriptions)
	e.sessionSubscriptions = make(asyncEventsNatsSubscriptions)
	wg.Wait()
	return e.client.Close(ctx)
}

// +checklocks:e.mu
func (e *asyncEventsNats) registerListener(key string, subscriptions asyncEventsNatsSubscriptions, listener AsyncEventListener) error {
	subs, found := subscriptions[key]
	if !found {
		subs = make(map[AsyncEventListener]NatsSubscription)
		subscriptions[key] = subs
	} else if _, found := subs[listener]; found {
		return ErrAlreadyRegistered
	}

	sub, err := e.client.Subscribe(key, listener.AsyncChannel())
	if err != nil {
		return err
	}

	subs[listener] = sub
	return nil
}

// +checklocks:e.mu
func (e *asyncEventsNats) unregisterListener(key string, subscriptions asyncEventsNatsSubscriptions, listener AsyncEventListener) error {
	subs, found := subscriptions[key]
	if !found {
		return nil
	}

	sub, found := subs[listener]
	if !found {
		return nil
	}

	delete(subs, listener)
	if len(subs) == 0 {
		delete(subscriptions, key)
	}

	return sub.Unsubscribe()
}

func (e *asyncEventsNats) RegisterBackendRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForBackendRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.registerListener(key, e.backendRoomSubscriptions, listener)
}

func (e *asyncEventsNats) UnregisterBackendRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForBackendRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.unregisterListener(key, e.backendRoomSubscriptions, listener)
}

func (e *asyncEventsNats) RegisterRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.registerListener(key, e.roomSubscriptions, listener)
}

func (e *asyncEventsNats) UnregisterRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForRoomId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.unregisterListener(key, e.roomSubscriptions, listener)
}

func (e *asyncEventsNats) RegisterUserListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForUserId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.registerListener(key, e.userSubscriptions, listener)
}

func (e *asyncEventsNats) UnregisterUserListener(roomId string, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForUserId(roomId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.unregisterListener(key, e.userSubscriptions, listener)
}

func (e *asyncEventsNats) RegisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForSessionId(sessionId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.registerListener(key, e.sessionSubscriptions, listener)
}

func (e *asyncEventsNats) UnregisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncEventListener) error {
	key := GetSubjectForSessionId(sessionId, backend)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.unregisterListener(key, e.sessionSubscriptions, listener)
}

func (e *asyncEventsNats) publish(subject string, message *AsyncMessage) error {
	message.SendTime = time.Now().Truncate(time.Microsecond)
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
