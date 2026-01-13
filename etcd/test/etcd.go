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
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/lease"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

var (
	etcdListenUrl = "http://localhost:8080"
)

type EtcdServer struct {
	embed *embed.Etcd
}

func (s *EtcdServer) URL() *url.URL {
	return &s.embed.Config().ListenClientUrls[0]
}

func (s *EtcdServer) SetValue(key string, value []byte) {
	if kv := s.embed.Server.KV(); kv != nil {
		kv.Put([]byte(key), value, lease.NoLease)
		kv.Commit()
	}
}

func (s *EtcdServer) DeleteValue(key string) {
	if kv := s.embed.Server.KV(); kv != nil {
		kv.DeleteRange([]byte(key), nil)
		kv.Commit()
	}
}

func NewServerForTest(t *testing.T) *EtcdServer {
	t.Helper()
	require := require.New(t)
	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	os.Chmod(cfg.Dir, 0700) // nolint
	cfg.LogLevel = "warn"
	cfg.Name = "signalingtest"
	cfg.ZapLoggerBuilder = embed.NewZapLoggerBuilder(zaptest.NewLogger(t, zaptest.Level(zap.WarnLevel)))

	u, err := url.Parse(etcdListenUrl)
	require.NoError(err)

	// Find a free port to bind the server to.
	var etcd *embed.Etcd
	for port := 50000; port < 50100; port++ {
		u.Host = net.JoinHostPort("localhost", strconv.Itoa(port))
		cfg.ListenClientUrls = []url.URL{*u}
		cfg.AdvertiseClientUrls = []url.URL{*u}
		httpListener := u
		httpListener.Host = net.JoinHostPort("localhost", strconv.Itoa(port+1))
		cfg.ListenClientHttpUrls = []url.URL{*httpListener}
		peerListener := u
		peerListener.Host = net.JoinHostPort("localhost", strconv.Itoa(port+2))
		cfg.ListenPeerUrls = []url.URL{*peerListener}
		cfg.AdvertisePeerUrls = []url.URL{*peerListener}
		cfg.InitialCluster = "signalingtest=" + peerListener.String()
		etcd, err = embed.StartEtcd(cfg)
		if test.IsErrorAddressAlreadyInUse(err) {
			continue
		}

		require.NoError(err)
		break
	}
	require.NotNil(etcd, "could not find free port")

	t.Cleanup(func() {
		etcd.Close()
		<-etcd.Server.StopNotify()
	})
	// Wait for server to be ready.
	<-etcd.Server.ReadyNotify()

	server := &EtcdServer{
		embed: etcd,
	}
	return server
}

func NewEtcdClientForTest(t *testing.T, server *EtcdServer) etcd.Client {
	t.Helper()

	logger := logtest.NewLoggerForTest(t)

	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", server.URL().String())

	client, err := etcd.NewClient(logger, config, "")
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, client.Close())
	})

	return client
}

type testWatch struct {
	key string
	op  clientv3.Op
	rev int64

	watcher etcd.ClientWatcher
}

type testClient struct {
	mu     sync.Mutex
	server *Server

	// +checklocks:mu
	closed    bool
	closeCh   chan struct{}
	processCh chan func()
	// +checklocks:mu
	listeners []etcd.ClientListener
	// +checklocks:mu
	watchers []*testWatch
}

func newTestClient(server *Server) *testClient {
	client := &testClient{
		server:    server,
		closeCh:   make(chan struct{}),
		processCh: make(chan func(), 1),
	}
	go func() {
		defer close(client.closeCh)
		for {
			f := <-client.processCh
			if f == nil {
				return
			}

			f()
		}
	}()
	return client
}

func (c *testClient) IsConfigured() bool {
	return true
}

func (c *testClient) WaitForConnection(ctx context.Context) error {
	return nil
}

func (c *testClient) GetServerInfoEtcd() *etcd.BackendServerInfoEtcd {
	return &etcd.BackendServerInfoEtcd{}
}

func (c *testClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	c.server.removeClient(c)
	close(c.processCh)
	<-c.closeCh
	return nil
}

func (c *testClient) AddListener(listener etcd.ClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.listeners = append(c.listeners, listener)
	c.processCh <- func() {
		listener.EtcdClientCreated(c)
	}
}

func (c *testClient) RemoveListener(listener etcd.ClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.listeners = slices.DeleteFunc(c.listeners, func(l etcd.ClientListener) bool {
		return l == listener
	})
}

func (c *testClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	keys, values, revision := c.server.getValues(key, 0, opts...)
	response := &clientv3.GetResponse{
		Count: int64(len(values)),
		Header: &etcdserverpb.ResponseHeader{
			Revision: revision,
		},
	}
	for idx, key := range keys {
		response.Kvs = append(response.Kvs, &mvccpb.KeyValue{
			Key:   []byte(key),
			Value: values[idx],
		})
	}
	return response, nil
}

func (c *testClient) notifyUpdated(key string, oldValue []byte, newValue []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	for _, w := range c.watchers {
		if withPrefix := w.op.IsOptsWithPrefix(); (withPrefix && strings.HasPrefix(key, w.key)) || (!withPrefix && key == w.key) {
			c.processCh <- func() {
				w.watcher.EtcdKeyUpdated(c, key, newValue, oldValue)
			}
		}
	}
}

func (c *testClient) notifyDeleted(key string, oldValue []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	for _, w := range c.watchers {
		if withPrefix := w.op.IsOptsWithPrefix(); (withPrefix && strings.HasPrefix(key, w.key)) || (!withPrefix && key == w.key) {
			c.processCh <- func() {
				w.watcher.EtcdKeyDeleted(c, key, oldValue)
			}
		}
	}
}

func (c *testClient) addWatcher(w *testWatch, opts ...clientv3.OpOption) error {
	keys, values, _ := c.server.getValues(w.key, w.rev, opts...)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("closed")
	}

	c.watchers = append(c.watchers, w)
	c.processCh <- func() {
		w.watcher.EtcdWatchCreated(c, w.key)
	}

	for idx, key := range keys {
		c.processCh <- func() {
			w.watcher.EtcdKeyUpdated(c, key, values[idx], nil)
		}
	}

	return nil
}

func (c *testClient) Watch(ctx context.Context, key string, nextRevision int64, watcher etcd.ClientWatcher, opts ...clientv3.OpOption) (int64, error) {
	w := &testWatch{
		key: key,
		rev: nextRevision,

		watcher: watcher,
	}
	for _, o := range opts {
		o(&w.op)
	}

	if err := c.addWatcher(w, opts...); err != nil {
		return 0, err
	}

	select {
	case <-c.closeCh:
		// Client is closed.
	case <-ctx.Done():
		// Watch context was cancelled / timed out.
	}
	return c.server.getRevision(), nil
}

type testServerValue struct {
	value    []byte
	revision int64
}

type Server struct {
	t  *testing.T
	mu sync.Mutex
	// +checklocks:mu
	clients []*testClient
	// +checklocks:mu
	values map[string]*testServerValue
	// +checklocks:mu
	revision int64
}

func (s *Server) newClient() *testClient {
	client := newTestClient(s)
	s.addClient(client)
	return client
}

func (s *Server) addClient(client *testClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clients = append(s.clients, client)
}

func (s *Server) removeClient(client *testClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clients = slices.DeleteFunc(s.clients, func(c *testClient) bool {
		return c == client
	})
}

func (s *Server) getRevision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.revision
}

func (s *Server) getValues(key string, minRevision int64, opts ...clientv3.OpOption) (keys []string, values [][]byte, revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var op clientv3.Op
	for _, o := range opts {
		o(&op)
	}
	if op.IsOptsWithPrefix() {
		for k, value := range s.values {
			if minRevision > 0 && value.revision < minRevision {
				continue
			}
			if strings.HasPrefix(k, key) {
				keys = append(keys, k)
				values = append(values, value.value)
			}
		}
	} else {
		if value, found := s.values[key]; found && (minRevision == 0 || value.revision >= minRevision) {
			keys = append(keys, key)
			values = append(values, value.value)
		}
	}

	revision = s.revision
	return
}

func (s *Server) SetValue(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, found := s.values[key]
	if found && bytes.Equal(prev.value, value) {
		return
	}

	if s.values == nil {
		s.values = make(map[string]*testServerValue)
	}
	if prev == nil {
		prev = &testServerValue{}
		s.values[key] = prev
	}
	s.revision++
	prevValue := prev.value
	prev.value = value
	prev.revision = s.revision

	for _, c := range s.clients {
		c.notifyUpdated(key, prevValue, value)
	}
}

func (s *Server) DeleteValue(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, found := s.values[key]
	if !found {
		return
	}

	delete(s.values, key)
	s.revision++

	for _, c := range s.clients {
		c.notifyDeleted(key, prev.value)
	}
}

func NewClientForTest(t *testing.T) (*Server, etcd.Client) {
	t.Helper()
	server := &Server{
		t:        t,
		revision: 1,
	}
	client := server.newClient()
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
	})
	return server, client
}
