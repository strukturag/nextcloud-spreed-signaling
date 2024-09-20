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
	"sync"

	"go.uber.org/zap"
)

type AsyncBackendRoomEventListener interface {
	ProcessBackendRoomRequest(message *AsyncMessage)
}

type AsyncRoomEventListener interface {
	ProcessAsyncRoomMessage(message *AsyncMessage)
}

type AsyncUserEventListener interface {
	ProcessAsyncUserMessage(message *AsyncMessage)
}

type AsyncSessionEventListener interface {
	ProcessAsyncSessionMessage(message *AsyncMessage)
}

type AsyncEvents interface {
	Close()

	RegisterBackendRoomListener(roomId string, backend *Backend, listener AsyncBackendRoomEventListener) error
	UnregisterBackendRoomListener(roomId string, backend *Backend, listener AsyncBackendRoomEventListener)

	RegisterRoomListener(roomId string, backend *Backend, listener AsyncRoomEventListener) error
	UnregisterRoomListener(roomId string, backend *Backend, listener AsyncRoomEventListener)

	RegisterUserListener(userId string, backend *Backend, listener AsyncUserEventListener) error
	UnregisterUserListener(userId string, backend *Backend, listener AsyncUserEventListener)

	RegisterSessionListener(sessionId string, backend *Backend, listener AsyncSessionEventListener) error
	UnregisterSessionListener(sessionId string, backend *Backend, listener AsyncSessionEventListener)

	PublishBackendRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error
	PublishRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error
	PublishUserMessage(userId string, backend *Backend, message *AsyncMessage) error
	PublishSessionMessage(sessionId string, backend *Backend, message *AsyncMessage) error
}

func NewAsyncEvents(log *zap.Logger, url string) (AsyncEvents, error) {
	client, err := NewNatsClient(log, url)
	if err != nil {
		return nil, err
	}

	return NewAsyncEventsNats(log, client)
}

type asyncBackendRoomSubscriber struct {
	mu sync.Mutex

	listeners map[AsyncBackendRoomEventListener]bool
}

func (s *asyncBackendRoomSubscriber) processBackendRoomRequest(message *AsyncMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for listener := range s.listeners {
		s.mu.Unlock()
		listener.ProcessBackendRoomRequest(message)
		s.mu.Lock()
	}
}

func (s *asyncBackendRoomSubscriber) addListener(listener AsyncBackendRoomEventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listeners == nil {
		s.listeners = make(map[AsyncBackendRoomEventListener]bool)
	}
	s.listeners[listener] = true
}

func (s *asyncBackendRoomSubscriber) removeListener(listener AsyncBackendRoomEventListener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, listener)
	return len(s.listeners) > 0
}

type asyncRoomSubscriber struct {
	mu sync.Mutex

	listeners map[AsyncRoomEventListener]bool
}

func (s *asyncRoomSubscriber) processAsyncRoomMessage(message *AsyncMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for listener := range s.listeners {
		s.mu.Unlock()
		listener.ProcessAsyncRoomMessage(message)
		s.mu.Lock()
	}
}

func (s *asyncRoomSubscriber) addListener(listener AsyncRoomEventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listeners == nil {
		s.listeners = make(map[AsyncRoomEventListener]bool)
	}
	s.listeners[listener] = true
}

func (s *asyncRoomSubscriber) removeListener(listener AsyncRoomEventListener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, listener)
	return len(s.listeners) > 0
}

type asyncUserSubscriber struct {
	mu sync.Mutex

	listeners map[AsyncUserEventListener]bool
}

func (s *asyncUserSubscriber) processAsyncUserMessage(message *AsyncMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for listener := range s.listeners {
		s.mu.Unlock()
		listener.ProcessAsyncUserMessage(message)
		s.mu.Lock()
	}
}

func (s *asyncUserSubscriber) addListener(listener AsyncUserEventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listeners == nil {
		s.listeners = make(map[AsyncUserEventListener]bool)
	}
	s.listeners[listener] = true
}

func (s *asyncUserSubscriber) removeListener(listener AsyncUserEventListener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, listener)
	return len(s.listeners) > 0
}

type asyncSessionSubscriber struct {
	mu sync.Mutex

	listeners map[AsyncSessionEventListener]bool
}

func (s *asyncSessionSubscriber) processAsyncSessionMessage(message *AsyncMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for listener := range s.listeners {
		s.mu.Unlock()
		listener.ProcessAsyncSessionMessage(message)
		s.mu.Lock()
	}
}

func (s *asyncSessionSubscriber) addListener(listener AsyncSessionEventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listeners == nil {
		s.listeners = make(map[AsyncSessionEventListener]bool)
	}
	s.listeners[listener] = true
}

func (s *asyncSessionSubscriber) removeListener(listener AsyncSessionEventListener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, listener)
	return len(s.listeners) > 0
}
