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
	"reflect"
	"sync"
	"time"
)

type TransientListener interface {
	SendMessage(message *ServerMessage) bool
}

type TransientData struct {
	mu        sync.Mutex
	data      map[string]any
	listeners map[TransientListener]bool
	timers    map[string]*time.Timer
	ttlCh     chan<- struct{}
}

// NewTransientData creates a new transient data container.
func NewTransientData() *TransientData {
	return &TransientData{}
}

func (t *TransientData) sendMessageToListener(listener TransientListener, message *ServerMessage) {
	t.mu.Unlock()
	defer t.mu.Lock()

	listener.SendMessage(message)
}

func (t *TransientData) notifySet(key string, prev, value any) {
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type:     "set",
			Key:      key,
			OldValue: prev,
			Value:    value,
		},
	}
	for listener := range t.listeners {
		t.sendMessageToListener(listener, msg)
	}
}

func (t *TransientData) notifyDeleted(key string, prev any) {
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type:     "remove",
			Key:      key,
			OldValue: prev,
		},
	}
	for listener := range t.listeners {
		t.sendMessageToListener(listener, msg)
	}
}

// AddListener adds a new listener to be notified about changes.
func (t *TransientData) AddListener(listener TransientListener) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.listeners == nil {
		t.listeners = make(map[TransientListener]bool)
	}
	t.listeners[listener] = true
	if len(t.data) > 0 {
		msg := &ServerMessage{
			Type: "transient",
			TransientData: &TransientDataServerMessage{
				Type: "initial",
				Data: t.data,
			},
		}
		t.sendMessageToListener(listener, msg)
	}
}

// RemoveListener removes a previously registered listener.
func (t *TransientData) RemoveListener(listener TransientListener) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.listeners, listener)
}

func (t *TransientData) updateTTL(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		delete(t.timers, key)
	} else {
		t.removeAfterTTL(key, value, ttl)
	}
}

func (t *TransientData) removeAfterTTL(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		return
	}

	if old, found := t.timers[key]; found {
		old.Stop()
	}

	timer := time.AfterFunc(ttl, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		t.compareAndRemove(key, value)
		if t.ttlCh != nil {
			select {
			case t.ttlCh <- struct{}{}:
			default:
			}
		}
	})
	if t.timers == nil {
		t.timers = make(map[string]*time.Timer)
	}
	t.timers[key] = timer
}

func (t *TransientData) doSet(key string, value any, prev any, ttl time.Duration) {
	if t.data == nil {
		t.data = make(map[string]any)
	}
	t.data[key] = value
	t.notifySet(key, prev, value)
	t.removeAfterTTL(key, value, ttl)
}

// Set sets a new value for the given key and notifies listeners
// if the value has been changed.
func (t *TransientData) Set(key string, value any) bool {
	return t.SetTTL(key, value, 0)
}

// SetTTL sets a new value for the given key with a time-to-live and notifies
// listeners if the value has been changed.
func (t *TransientData) SetTTL(key string, value any, ttl time.Duration) bool {
	if value == nil {
		return t.Remove(key)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if found && reflect.DeepEqual(prev, value) {
		t.updateTTL(key, value, ttl)
		return false
	}

	t.doSet(key, value, prev, ttl)
	return true
}

// CompareAndSet sets a new value for the given key only for a given old value
// and notifies listeners if the value has been changed.
func (t *TransientData) CompareAndSet(key string, old, value any) bool {
	return t.CompareAndSetTTL(key, old, value, 0)
}

// CompareAndSetTTL sets a new value for the given key with a time-to-live,
// only for a given old value and notifies listeners if the value has been
// changed.
func (t *TransientData) CompareAndSetTTL(key string, old, value any, ttl time.Duration) bool {
	if value == nil {
		return t.CompareAndRemove(key, old)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if old != nil && (!found || !reflect.DeepEqual(prev, old)) {
		return false
	} else if old == nil && found {
		return false
	}

	t.doSet(key, value, prev, ttl)
	return true
}

func (t *TransientData) doRemove(key string, prev any) {
	delete(t.data, key)
	if old, found := t.timers[key]; found {
		old.Stop()
		delete(t.timers, key)
	}
	t.notifyDeleted(key, prev)
}

// Remove deletes the value with the given key and notifies listeners
// if the key was removed.
func (t *TransientData) Remove(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if !found {
		return false
	}

	t.doRemove(key, prev)
	return true
}

// CompareAndRemove deletes the value with the given key if it has a given value
// and notifies listeners if the key was removed.
func (t *TransientData) CompareAndRemove(key string, old any) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.compareAndRemove(key, old)
}

func (t *TransientData) compareAndRemove(key string, old any) bool {
	prev, found := t.data[key]
	if !found || !reflect.DeepEqual(prev, old) {
		return false
	}

	t.doRemove(key, prev)
	return true
}

// GetData returns a copy of the internal data.
func (t *TransientData) GetData() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make(map[string]any)
	for k, v := range t.data {
		result[k] = v
	}
	return result
}
