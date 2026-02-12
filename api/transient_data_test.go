//go:build go1.25

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
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

func Test_TransientData(t *testing.T) {
	t.Parallel()
	test.SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		data := NewTransientData()
		assert.False(data.Set("foo", nil))
		assert.True(data.Set("foo", "bar"))
		assert.False(data.Set("foo", "bar"))
		assert.True(data.Set("foo", "baz"))
		assert.False(data.CompareAndSet("foo", "bar", "lala"))
		assert.True(data.CompareAndSet("foo", "baz", "lala"))
		assert.False(data.CompareAndSet("test", nil, nil))
		assert.True(data.CompareAndSet("test", nil, "123"))
		assert.False(data.CompareAndSet("test", nil, "456"))
		assert.False(data.CompareAndRemove("test", "1234"))
		assert.True(data.CompareAndRemove("test", "123"))
		assert.False(data.Remove("lala"))
		assert.True(data.Remove("foo"))

		assert.True(data.SetTTL("test", "1234", time.Millisecond))
		assert.Equal("1234", data.GetData()["test"])
		// Data is removed after the TTL
		start := time.Now()
		time.Sleep(time.Millisecond)
		synctest.Wait()
		assert.Equal(time.Millisecond, time.Since(start))
		assert.Nil(data.GetData()["test"])

		assert.True(data.SetTTL("test", "1234", time.Millisecond))
		assert.Equal("1234", data.GetData()["test"])
		assert.True(data.SetTTL("test", "2345", 3*time.Millisecond))
		assert.Equal("2345", data.GetData()["test"])
		start = time.Now()
		// Data is removed after the TTL only if the value still matches
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		assert.Equal("2345", data.GetData()["test"])
		// Data is removed after the (second) TTL
		time.Sleep(time.Millisecond)
		synctest.Wait()
		assert.Equal(3*time.Millisecond, time.Since(start))
		assert.Nil(data.GetData()["test"])

		// Setting existing key will update the TTL
		assert.True(data.SetTTL("test", "1234", time.Millisecond))
		assert.False(data.SetTTL("test", "1234", 3*time.Millisecond))
		start = time.Now()
		// Data still exists after the first TTL
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		assert.Equal("1234", data.GetData()["test"])
		// Data is removed after the (updated) TTL
		time.Sleep(time.Millisecond)
		synctest.Wait()
		assert.Equal(3*time.Millisecond, time.Since(start))
		assert.Nil(data.GetData()["test"])
	})
}

type MockTransientListener struct {
	mu      sync.Mutex
	sending chan struct{}
	done    chan struct{}

	// +checklocks:mu
	data *TransientData
}

func (l *MockTransientListener) SendMessage(message *ServerMessage) bool {
	close(l.sending)

	time.Sleep(10 * time.Millisecond)

	l.mu.Lock()
	defer l.mu.Unlock()
	defer close(l.done)

	time.Sleep(10 * time.Millisecond)

	return true
}

func (l *MockTransientListener) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.data.RemoveListener(l)
}

func Test_TransientDataDeadlock(t *testing.T) {
	t.Parallel()
	data := NewTransientData()

	listener := &MockTransientListener{
		sending: make(chan struct{}),
		done:    make(chan struct{}),

		data: data,
	}
	data.AddListener(listener)

	go func() {
		<-listener.sending
		listener.Close()
	}()

	data.Set("foo", "bar")
	<-listener.done
}

type initialDataListener struct {
	t *testing.T

	expected StringMap
	sent     atomic.Int32
}

func (l *initialDataListener) SendMessage(message *ServerMessage) bool {
	switch l.sent.Add(1) {
	case 1:
		if assert.Equal(l.t, "transient", message.Type) &&
			assert.NotNil(l.t, message.TransientData) &&
			assert.Equal(l.t, "initial", message.TransientData.Type) {
			assert.Equal(l.t, l.expected, message.TransientData.Data)
		}
	case 2:
		if assert.Equal(l.t, "transient", message.Type) &&
			assert.NotNil(l.t, message.TransientData) &&
			assert.Equal(l.t, "remove", message.TransientData.Type) {
			assert.Equal(l.t, "foo", message.TransientData.Key)
			assert.Equal(l.t, "bar", message.TransientData.OldValue)
		}
	default:
		assert.Fail(l.t, "unexpected message", "received %+v", message)
	}
	return true
}

func Test_TransientDataNotifyInitial(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	data := NewTransientData()
	assert.True(data.Set("foo", "bar"))

	listener := &initialDataListener{
		t: t,
		expected: StringMap{
			"foo": "bar",
		},
	}
	data.AddListener(listener)
	assert.EqualValues(1, listener.sent.Load())
}

func Test_TransientDataSetInitial(t *testing.T) {
	t.Parallel()
	test.SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)

		now := time.Now()
		data := NewTransientData()
		listener1 := &initialDataListener{
			t: t,
			expected: StringMap{
				"foo": "bar",
				"bar": 1234,
			},
		}
		data.AddListener(listener1)
		assert.EqualValues(0, listener1.sent.Load())

		data.SetInitial(TransientDataEntries{
			"foo":     NewTransientDataEntryWithExpires("bar", now.Add(time.Minute)),
			"bar":     NewTransientDataEntry(1234, 0),
			"expired": NewTransientDataEntryWithExpires(1234, now.Add(-time.Second)),
		})

		entries := data.GetEntries()
		assert.Equal(TransientDataEntries{
			"foo": NewTransientDataEntryWithExpires("bar", now.Add(time.Minute)),
			"bar": NewTransientDataEntry(1234, 0),
		}, entries)

		listener2 := &initialDataListener{
			t: t,
			expected: StringMap{
				"foo": "bar",
				"bar": 1234,
			},
		}
		data.AddListener(listener2)
		assert.EqualValues(1, listener1.sent.Load())
		assert.EqualValues(1, listener2.sent.Load())

		time.Sleep(time.Minute)
		synctest.Wait()
		assert.EqualValues(2, listener1.sent.Load())
		assert.EqualValues(2, listener2.sent.Load())
	})
}
