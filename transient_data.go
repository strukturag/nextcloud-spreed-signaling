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
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	TransientSessionDataPrefix = "sd:"
)

type TransientListener interface {
	SendMessage(message *ServerMessage) bool
}

type TransientDataEntry struct {
	Value   any       `json:"value"`
	Expires time.Time `json:"expires,omitzero"`
}

func NewTransientDataEntry(value any, ttl time.Duration) *TransientDataEntry {
	entry := &TransientDataEntry{
		Value: value,
	}
	if ttl > 0 {
		entry.Expires = time.Now().Add(ttl)
	}
	return entry
}

func NewTransientDataEntryWithExpires(value any, expires time.Time) *TransientDataEntry {
	entry := &TransientDataEntry{
		Value:   value,
		Expires: expires,
	}
	return entry
}

func (e *TransientDataEntry) clone() *TransientDataEntry {
	result := *e
	return &result
}

func (e *TransientDataEntry) update(value any, ttl time.Duration) {
	e.Value = value
	if ttl > 0 {
		e.Expires = time.Now().Add(ttl)
	} else {
		e.Expires = time.Time{}
	}
}

type TransientDataEntries map[string]*TransientDataEntry

func (e TransientDataEntries) String() string {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", e, err)
	}

	return string(data)
}

type TransientData struct {
	mu sync.Mutex
	// +checklocks:mu
	data TransientDataEntries
	// +checklocks:mu
	listeners map[TransientListener]bool
	// +checklocks:mu
	timers map[string]*time.Timer
}

// NewTransientData creates a new transient data container.
func NewTransientData() *TransientData {
	return &TransientData{}
}

// +checklocks:t.mu
func (t *TransientData) sendMessageToListener(listener TransientListener, message *ServerMessage) {
	t.mu.Unlock()
	defer t.mu.Lock()

	listener.SendMessage(message)
}

// +checklocks:t.mu
func (t *TransientData) notifySet(key string, prev, value any) {
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type:     "set",
			Key:      key,
			Value:    value,
			OldValue: prev,
		},
	}
	for listener := range t.listeners {
		t.sendMessageToListener(listener, msg)
	}
}

// +checklocks:t.mu
func (t *TransientData) notifyDeleted(key string, prev *TransientDataEntry) {
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type: "remove",
			Key:  key,
		},
	}
	if prev != nil {
		msg.TransientData.OldValue = prev.Value
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
		data := make(api.StringMap, len(t.data))
		for k, v := range t.data {
			data[k] = v.Value
		}
		msg := &ServerMessage{
			Type: "transient",
			TransientData: &TransientDataServerMessage{
				Type: "initial",
				Data: data,
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

// +checklocks:t.mu
func (t *TransientData) updateTTL(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		if old, found := t.timers[key]; found {
			old.Stop()
			delete(t.timers, key)
		}
	} else {
		t.removeAfterTTL(key, value, ttl)
	}
}

// +checklocks:t.mu
func (t *TransientData) removeAfterTTL(key string, value any, ttl time.Duration) {
	if old, found := t.timers[key]; found {
		old.Stop()
	}

	if ttl <= 0 {
		delete(t.timers, key)
		return
	}

	timer := time.AfterFunc(ttl, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		t.compareAndRemove(key, value)
	})
	if t.timers == nil {
		t.timers = make(map[string]*time.Timer)
	}
	t.timers[key] = timer
}

// +checklocks:t.mu
func (t *TransientData) doSet(key string, value any, prev *TransientDataEntry, ttl time.Duration) {
	if t.data == nil {
		t.data = make(TransientDataEntries)
	}
	var oldValue any
	if prev == nil {
		entry := NewTransientDataEntry(value, ttl)
		t.data[key] = entry
	} else {
		oldValue = prev.Value
		prev.update(value, ttl)
	}
	t.notifySet(key, oldValue, value)
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
	if found && reflect.DeepEqual(prev.Value, value) {
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
	if old != nil && (!found || !reflect.DeepEqual(prev.Value, old)) {
		return false
	} else if old == nil && found {
		return false
	}

	t.doSet(key, value, prev, ttl)
	return true
}

// +checklocks:t.mu
func (t *TransientData) doRemove(key string, prev *TransientDataEntry) {
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

// +checklocks:t.mu
func (t *TransientData) compareAndRemove(key string, old any) bool {
	prev, found := t.data[key]
	if !found || !reflect.DeepEqual(prev.Value, old) {
		return false
	}

	t.doRemove(key, prev)
	return true
}

// GetData returns a copy of the internal data.
func (t *TransientData) GetData() api.StringMap {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.data) == 0 {
		return nil
	}

	result := make(api.StringMap, len(t.data))
	for k, entry := range t.data {
		result[k] = entry.Value
	}
	return result
}

// GetEntries returns a copy of the internal data entries.
func (t *TransientData) GetEntries() TransientDataEntries {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.data) == 0 {
		return nil
	}

	result := make(TransientDataEntries, len(t.data))
	for k, e := range t.data {
		result[k] = e.clone()
	}
	return result
}

// SetInitial sets the initial data and notifies listeners.
func (t *TransientData) SetInitial(data TransientDataEntries) {
	if len(data) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.data == nil {
		t.data = make(TransientDataEntries)
	}

	msgData := make(api.StringMap, len(data))
	for k, v := range data {
		if _, found := t.data[k]; found {
			// Entry already present (i.e. was set by regular event).
			continue
		}

		msgData[k] = v.Value
		t.data[k] = v
	}
	if len(msgData) == 0 {
		return
	}
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type: "initial",
			Data: msgData,
		},
	}
	for listener := range t.listeners {
		t.sendMessageToListener(listener, msg)
	}
}
