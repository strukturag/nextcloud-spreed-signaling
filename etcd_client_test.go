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
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/lease"
)

var (
	etcdListenUrl = "http://localhost:8080"
)

func isErrorAddressAlreadyInUse(err error) bool {
	var eOsSyscall *os.SyscallError
	if !errors.As(err, &eOsSyscall) {
		return false
	}
	var errErrno syscall.Errno // doesn't need a "*" (ptr) because it's already a ptr (uintptr)
	if !errors.As(eOsSyscall, &errErrno) {
		return false
	}
	if errErrno == syscall.EADDRINUSE {
		return true
	}
	const WSAEADDRINUSE = 10048
	if runtime.GOOS == "windows" && errErrno == WSAEADDRINUSE {
		return true
	}
	return false
}

func NewEtcdForTest(t *testing.T) *embed.Etcd {
	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	os.Chmod(cfg.Dir, 0700) // nolint
	cfg.LogLevel = "warn"

	u, err := url.Parse(etcdListenUrl)
	if err != nil {
		t.Fatal(err)
	}

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
		cfg.InitialCluster = "default=" + peerListener.String()
		etcd, err = embed.StartEtcd(cfg)
		if isErrorAddressAlreadyInUse(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		break
	}
	if etcd == nil {
		t.Fatal("could not find free port")
	}

	t.Cleanup(func() {
		etcd.Close()
	})
	// Wait for server to be ready.
	<-etcd.Server.ReadyNotify()

	return etcd
}

func NewEtcdClientForTest(t *testing.T) (*embed.Etcd, *EtcdClient) {
	etcd := NewEtcdForTest(t)

	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().ListenClientUrls[0].String())

	client, err := NewEtcdClient(config, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return etcd, client
}

func SetEtcdValue(etcd *embed.Etcd, key string, value []byte) {
	if kv := etcd.Server.KV(); kv != nil {
		kv.Put([]byte(key), value, lease.NoLease)
		kv.Commit()
	}
}

func DeleteEtcdValue(etcd *embed.Etcd, key string) {
	if kv := etcd.Server.KV(); kv != nil {
		kv.DeleteRange([]byte(key), nil)
		kv.Commit()
	}
}

func Test_EtcdClient_Get(t *testing.T) {
	etcd, client := NewEtcdClientForTest(t)

	if response, err := client.Get(context.Background(), "foo"); err != nil {
		t.Error(err)
	} else if response.Count != 0 {
		t.Errorf("expected 0 response, got %d", response.Count)
	}

	SetEtcdValue(etcd, "foo", []byte("bar"))

	if response, err := client.Get(context.Background(), "foo"); err != nil {
		t.Error(err)
	} else if response.Count != 1 {
		t.Errorf("expected 1 responses, got %d", response.Count)
	} else if string(response.Kvs[0].Key) != "foo" {
		t.Errorf("expected key \"foo\", got \"%s\"", string(response.Kvs[0].Key))
	} else if string(response.Kvs[0].Value) != "bar" {
		t.Errorf("expected value \"bar\", got \"%s\"", string(response.Kvs[0].Value))
	}
}

func Test_EtcdClient_GetPrefix(t *testing.T) {
	etcd, client := NewEtcdClientForTest(t)

	if response, err := client.Get(context.Background(), "foo"); err != nil {
		t.Error(err)
	} else if response.Count != 0 {
		t.Errorf("expected 0 response, got %d", response.Count)
	}

	SetEtcdValue(etcd, "foo", []byte("1"))
	SetEtcdValue(etcd, "foo/lala", []byte("2"))
	SetEtcdValue(etcd, "lala/foo", []byte("3"))

	if response, err := client.Get(context.Background(), "foo", clientv3.WithPrefix()); err != nil {
		t.Error(err)
	} else if response.Count != 2 {
		t.Errorf("expected 2 responses, got %d", response.Count)
	} else if string(response.Kvs[0].Key) != "foo" {
		t.Errorf("expected key \"foo\", got \"%s\"", string(response.Kvs[0].Key))
	} else if string(response.Kvs[0].Value) != "1" {
		t.Errorf("expected value \"1\", got \"%s\"", string(response.Kvs[0].Value))
	} else if string(response.Kvs[1].Key) != "foo/lala" {
		t.Errorf("expected key \"foo/lala\", got \"%s\"", string(response.Kvs[1].Key))
	} else if string(response.Kvs[1].Value) != "2" {
		t.Errorf("expected value \"2\", got \"%s\"", string(response.Kvs[1].Value))
	}
}

type etcdEvent struct {
	t     mvccpb.Event_EventType
	key   string
	value string
}

type EtcdClientTestListener struct {
	t *testing.T

	ctx    context.Context
	cancel context.CancelFunc

	initial   chan bool
	initialWg sync.WaitGroup
	events    chan etcdEvent
}

func NewEtcdClientTestListener(ctx context.Context, t *testing.T) *EtcdClientTestListener {
	ctx, cancel := context.WithCancel(ctx)
	return &EtcdClientTestListener{
		t: t,

		ctx:    ctx,
		cancel: cancel,

		initial: make(chan bool),
		events:  make(chan etcdEvent),
	}
}

func (l *EtcdClientTestListener) Close() {
	l.cancel()
}

func (l *EtcdClientTestListener) EtcdClientCreated(client *EtcdClient) {
	l.initialWg.Add(1)
	go func() {
		if err := client.Watch(clientv3.WithRequireLeader(l.ctx), "foo", l, clientv3.WithPrefix()); err != nil {
			l.t.Error(err)
		}
	}()

	go func() {
		client.WaitForConnection()

		ctx, cancel := context.WithTimeout(l.ctx, time.Second)
		defer cancel()

		if response, err := client.Get(ctx, "foo", clientv3.WithPrefix()); err != nil {
			l.t.Error(err)
		} else if response.Count != 1 {
			l.t.Errorf("expected 1 responses, got %d", response.Count)
		} else if string(response.Kvs[0].Key) != "foo/a" {
			l.t.Errorf("expected key \"foo/a\", got \"%s\"", string(response.Kvs[0].Key))
		} else if string(response.Kvs[0].Value) != "1" {
			l.t.Errorf("expected value \"1\", got \"%s\"", string(response.Kvs[0].Value))
		}
		l.initialWg.Wait()
		l.initial <- true
	}()
}

func (l *EtcdClientTestListener) EtcdWatchCreated(client *EtcdClient, key string) {
	l.initialWg.Done()
}

func (l *EtcdClientTestListener) EtcdKeyUpdated(client *EtcdClient, key string, value []byte) {
	l.events <- etcdEvent{
		t:     clientv3.EventTypePut,
		key:   string(key),
		value: string(value),
	}
}

func (l *EtcdClientTestListener) EtcdKeyDeleted(client *EtcdClient, key string) {
	l.events <- etcdEvent{
		t:   clientv3.EventTypeDelete,
		key: string(key),
	}
}

func Test_EtcdClient_Watch(t *testing.T) {
	etcd, client := NewEtcdClientForTest(t)

	SetEtcdValue(etcd, "foo/a", []byte("1"))

	listener := NewEtcdClientTestListener(context.Background(), t)
	defer listener.Close()

	client.AddListener(listener)
	defer client.RemoveListener(listener)

	<-listener.initial

	SetEtcdValue(etcd, "foo/b", []byte("2"))
	event := <-listener.events
	if event.t != clientv3.EventTypePut {
		t.Errorf("expected type %d, got %d", clientv3.EventTypePut, event.t)
	} else if event.key != "foo/b" {
		t.Errorf("expected key %s, got %s", "foo/b", event.key)
	} else if event.value != "2" {
		t.Errorf("expected value %s, got %s", "2", event.value)
	}

	DeleteEtcdValue(etcd, "foo/a")
	event = <-listener.events
	if event.t != clientv3.EventTypeDelete {
		t.Errorf("expected type %d, got %d", clientv3.EventTypeDelete, event.t)
	} else if event.key != "foo/a" {
		t.Errorf("expected key %s, got %s", "foo/a", event.key)
	}
}
