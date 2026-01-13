/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
)

var (
	testTimeout = 10 * time.Second
)

type updateEvent struct {
	key   string
	value string
	prev  []byte
}

type deleteEvent struct {
	key  string
	prev []byte
}

type testWatcher struct {
	created chan struct{}
	updated chan updateEvent
	deleted chan deleteEvent
}

func newTestWatcher() *testWatcher {
	return &testWatcher{
		created: make(chan struct{}),
		updated: make(chan updateEvent),
		deleted: make(chan deleteEvent),
	}
}

func (w *testWatcher) EtcdWatchCreated(client etcd.Client, key string) {
	close(w.created)
}

func (w *testWatcher) EtcdKeyUpdated(client etcd.Client, key string, value []byte, prevValue []byte) {
	w.updated <- updateEvent{
		key:   key,
		value: string(value),
		prev:  prevValue,
	}
}

func (w *testWatcher) EtcdKeyDeleted(client etcd.Client, key string, prevValue []byte) {
	w.deleted <- deleteEvent{
		key:  key,
		prev: prevValue,
	}
}

type serverInterface interface {
	SetValue(key string, value []byte)
	DeleteValue(key string)
}

type testClientListener struct {
	called chan struct{}
}

func (l *testClientListener) EtcdClientCreated(c etcd.Client) {
	close(l.called)
}

func testServerWatch(t *testing.T, server serverInterface, client etcd.Client) {
	require := require.New(t)
	assert := assert.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	assert.True(client.IsConfigured(), "should be configured")
	require.NoError(client.WaitForConnection(ctx))

	listener := &testClientListener{
		called: make(chan struct{}),
	}
	client.AddListener(listener)
	defer client.RemoveListener(listener)

	select {
	case <-listener.called:
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	watcher := newTestWatcher()

	go func() {
		if _, err := client.Watch(cancelCtx, "foo", 0, watcher); err != nil {
			assert.ErrorIs(err, context.Canceled)
		}
	}()

	select {
	case <-watcher.created:
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	key := "foo"
	value := "bar"
	server.SetValue("foo", []byte(value))
	select {
	case evt := <-watcher.updated:
		assert.Equal(key, evt.key)
		assert.Equal(value, evt.value)
		assert.Empty(evt.prev)
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		assert.EqualValues(1, response.Count)
		if assert.Len(response.Kvs, 1) {
			assert.Equal(key, string(response.Kvs[0].Key))
			assert.Equal(value, string(response.Kvs[0].Value))
		}
	}
	if response, err := client.Get(ctx, "f"); assert.NoError(err) {
		assert.EqualValues(0, response.Count)
		assert.Empty(response.Kvs)
	}
	if response, err := client.Get(ctx, "f", clientv3.WithPrefix()); assert.NoError(err) {
		assert.EqualValues(1, response.Count)
		if assert.Len(response.Kvs, 1) {
			assert.Equal(key, string(response.Kvs[0].Key))
			assert.Equal(value, string(response.Kvs[0].Value))
		}
	}

	server.DeleteValue("foo")
	select {
	case evt := <-watcher.deleted:
		assert.Equal(key, evt.key)
		assert.Equal(value, string(evt.prev))
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	select {
	case evt := <-watcher.updated:
		assert.Fail("unexpected update event", "got %+v", evt)
	case evt := <-watcher.deleted:
		assert.Fail("unexpected deleted event", "got %+v", evt)
	default:
	}
}

func TestServerWatch_Mock(t *testing.T) {
	t.Parallel()

	server, client := NewClientForTest(t)
	testServerWatch(t, server, client)
}

func TestServerWatch_Real(t *testing.T) {
	t.Parallel()

	server := NewServerForTest(t)
	client := NewEtcdClientForTest(t, server)
	testServerWatch(t, server, client)
}

func testServerWatchInitialData(t *testing.T, server serverInterface, client etcd.Client) {
	require := require.New(t)
	assert := assert.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	key := "foo"
	value := "bar"
	server.SetValue("foo", []byte(value))

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	watcher := newTestWatcher()

	go func() {
		if _, err := client.Watch(cancelCtx, "foo", 1, watcher); err != nil {
			assert.ErrorIs(err, context.Canceled)
		}
	}()

	select {
	case <-watcher.created:
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	select {
	case evt := <-watcher.updated:
		assert.Equal(key, evt.key)
		assert.Equal(value, evt.value)
		assert.Empty(evt.prev)
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	select {
	case evt := <-watcher.updated:
		assert.Fail("unexpected update event", "got %+v", evt)
	case evt := <-watcher.deleted:
		assert.Fail("unexpected deleted event", "got %+v", evt)
	default:
	}
}

func TestServerWatchInitialData_Mock(t *testing.T) {
	t.Parallel()

	server, client := NewClientForTest(t)
	testServerWatchInitialData(t, server, client)
}

func TestServerWatchInitialData_Real(t *testing.T) {
	t.Parallel()

	server := NewServerForTest(t)
	client := NewEtcdClientForTest(t, server)
	testServerWatchInitialData(t, server, client)
}

func testServerWatchInitialOldData(t *testing.T, server serverInterface, client etcd.Client) {
	require := require.New(t)
	assert := assert.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	key := "foo"
	value := "bar"
	server.SetValue("foo", []byte(value))

	response, err := client.Get(ctx, key)
	require.NoError(err)

	if assert.EqualValues(1, response.Count) && assert.Len(response.Kvs, 1) {
		assert.Equal(key, string(response.Kvs[0].Key))
		assert.Equal(value, string(response.Kvs[0].Value))
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	watcher := newTestWatcher()

	go func() {
		if _, err := client.Watch(cancelCtx, "foo", response.Header.GetRevision()+1, watcher); err != nil {
			assert.ErrorIs(err, context.Canceled)
		}
	}()

	select {
	case <-watcher.created:
	case <-ctx.Done():
		require.NoError(ctx.Err())
	}

	select {
	case evt := <-watcher.updated:
		assert.Fail("unexpected update event", "got %+v", evt)
	case evt := <-watcher.deleted:
		assert.Fail("unexpected deleted event", "got %+v", evt)
	default:
	}
}

func TestServerWatchInitialOldData_Mock(t *testing.T) {
	t.Parallel()

	server, client := NewClientForTest(t)
	testServerWatchInitialOldData(t, server, client)
}

func TestServerWatchInitialOldData_Real(t *testing.T) {
	t.Parallel()

	server := NewServerForTest(t)
	client := NewEtcdClientForTest(t, server)
	testServerWatchInitialOldData(t, server, client)
}
