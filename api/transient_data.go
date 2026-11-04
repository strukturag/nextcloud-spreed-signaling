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
package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"
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
	// Held while modifying the data and notifying the listeners, so they get
	// notified in the order the changes were made. Must be acquired before
	// "mu". The listeners are notified without holding "mu", so they may be
	// removed while being notified. They may not modify the data or add
	// listeners while being notified.
	notifyMu sync.Mutex

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

// transientNotification is a message to send to listeners after a change.
type transientNotification struct {
	listeners []TransientListener
	message   *ServerMessage
}

func (n *transientNotification) send() {
	if n == nil {
		return
	}

	for _, listener := range n.listeners {
		listener.SendMessage(n.message)
	}
}

// +checklocks:t.mu
func (t *TransientData) newNotification(message *ServerMessage) *transientNotification {
	if len(t.listeners) == 0 {
		return nil
	}

	return &transientNotification{
		listeners: slices.Collect(maps.Keys(t.listeners)),
		message:   message,
	}
}

// +checklocks:t.mu
func (t *TransientData) notifySet(key string, prev, value any) *transientNotification {
	msg := &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type:     "set",
			Key:      key,
			Value:    value,
			OldValue: prev,
		},
	}
	return t.newNotification(msg)
}

// +checklocks:t.mu
func (t *TransientData) notifyDeleted(key string, prev *TransientDataEntry) *transientNotification {
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
	return t.newNotification(msg)
}

// AddListener adds a new listener to be notified about changes.
func (t *TransientData) AddListener(listener TransientListener) {
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	if msg := t.addListener(listener); msg != nil {
		listener.SendMessage(msg)
	}
}

// +checklocks:t.notifyMu
func (t *TransientData) addListener(listener TransientListener) *ServerMessage {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.listeners == nil {
		t.listeners = make(map[TransientListener]bool)
	}
	t.listeners[listener] = true
	if len(t.data) == 0 {
		return nil
	}

	data := make(StringMap, len(t.data))
	for k, v := range t.data {
		data[k] = v.Value
	}
	return &ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type: "initial",
			Data: data,
		},
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
		t.notifyMu.Lock()
		defer t.notifyMu.Unlock()

		_, notification := t.compareAndRemove(key, value)
		notification.send()
	})
	if t.timers == nil {
		t.timers = make(map[string]*time.Timer)
	}
	t.timers[key] = timer
}

// +checklocks:t.mu
func (t *TransientData) doSet(key string, value any, prev *TransientDataEntry, ttl time.Duration) *transientNotification {
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
	t.removeAfterTTL(key, value, ttl)
	return t.notifySet(key, oldValue, value)
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

	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	changed, notification := t.setTTL(key, value, ttl)
	notification.send()
	return changed
}

// +checklocks:t.notifyMu
func (t *TransientData) setTTL(key string, value any, ttl time.Duration) (bool, *transientNotification) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if found && reflect.DeepEqual(prev.Value, value) {
		t.updateTTL(key, value, ttl)
		return false, nil
	}

	return true, t.doSet(key, value, prev, ttl)
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

	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	changed, notification := t.compareAndSetTTL(key, old, value, ttl)
	notification.send()
	return changed
}

// +checklocks:t.notifyMu
func (t *TransientData) compareAndSetTTL(key string, old, value any, ttl time.Duration) (bool, *transientNotification) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if old != nil && (!found || !reflect.DeepEqual(prev.Value, old)) {
		return false, nil
	} else if old == nil && found {
		return false, nil
	}

	return true, t.doSet(key, value, prev, ttl)
}

// +checklocks:t.mu
func (t *TransientData) doRemove(key string, prev *TransientDataEntry) *transientNotification {
	delete(t.data, key)
	if old, found := t.timers[key]; found {
		old.Stop()
		delete(t.timers, key)
	}
	return t.notifyDeleted(key, prev)
}

// Remove deletes the value with the given key and notifies listeners
// if the key was removed.
func (t *TransientData) Remove(key string) bool {
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	removed, notification := t.remove(key)
	notification.send()
	return removed
}

// +checklocks:t.notifyMu
func (t *TransientData) remove(key string) (bool, *transientNotification) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if !found {
		return false, nil
	}

	return true, t.doRemove(key, prev)
}

// CompareAndRemove deletes the value with the given key if it has a given value
// and notifies listeners if the key was removed.
func (t *TransientData) CompareAndRemove(key string, old any) bool {
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	removed, notification := t.compareAndRemove(key, old)
	notification.send()
	return removed
}

// +checklocks:t.notifyMu
func (t *TransientData) compareAndRemove(key string, old any) (bool, *transientNotification) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev, found := t.data[key]
	if !found || !reflect.DeepEqual(prev.Value, old) {
		return false, nil
	}

	return true, t.doRemove(key, prev)
}

// GetData returns a copy of the internal data.
func (t *TransientData) GetData() StringMap {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.data) == 0 {
		return nil
	}

	result := make(StringMap, len(t.data))
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

	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()

	t.setInitial(data).send()
}

// +checklocks:t.notifyMu
func (t *TransientData) setInitial(data TransientDataEntries) *transientNotification {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.data == nil {
		t.data = make(TransientDataEntries)
	}

	now := time.Now()
	msgData := make(StringMap, len(data))
	for k, v := range data {
		if _, found := t.data[k]; found {
			// Entry already present (i.e. was set by regular event).
			continue
		}

		if e := v.Expires; !e.IsZero() {
			if now.After(e) {
				// Already expired
				continue
			}

			t.removeAfterTTL(k, v.Value, e.Sub(now))
		}
		msgData[k] = v.Value
		t.data[k] = v
	}
	if len(msgData) == 0 {
		return nil
	}

	return t.newNotification(&ServerMessage{
		Type: "transient",
		TransientData: &TransientDataServerMessage{
			Type: "initial",
			Data: msgData,
		},
	})
}
