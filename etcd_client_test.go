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
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/lease"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
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

func NewEtcdForTestWithTls(t *testing.T, withTLS bool) (*embed.Etcd, string, string) {
	t.Helper()

	require := require.New(t)
	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	os.Chmod(cfg.Dir, 0700) // nolint
	cfg.LogLevel = "warn"
	cfg.Name = "signalingtest"

	u, err := url.Parse(etcdListenUrl)
	require.NoError(err)

	var keyfile string
	var certfile string
	if withTLS {
		u.Scheme = "https"

		tmpdir := t.TempDir()
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)
		keyfile = path.Join(tmpdir, "etcd.key")
		require.NoError(WritePrivateKey(key, keyfile))
		cfg.ClientTLSInfo.KeyFile = keyfile
		cfg.PeerTLSInfo.KeyFile = keyfile

		cert := GenerateSelfSignedCertificateForTesting(t, 1024, "etcd", key)
		certfile = path.Join(tmpdir, "etcd.pem")
		require.NoError(os.WriteFile(certfile, cert, 0755))
		cfg.ClientTLSInfo.CertFile = certfile
		cfg.ClientTLSInfo.TrustedCAFile = certfile
		cfg.PeerTLSInfo.CertFile = certfile
		cfg.PeerTLSInfo.TrustedCAFile = certfile
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
		cfg.InitialCluster = "signalingtest=" + peerListener.String()
		cfg.ZapLoggerBuilder = embed.NewZapLoggerBuilder(zaptest.NewLogger(t, zaptest.Level(zap.WarnLevel)))
		etcd, err = embed.StartEtcd(cfg)
		if isErrorAddressAlreadyInUse(err) {
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

	return etcd, keyfile, certfile
}

func NewEtcdForTest(t *testing.T) *embed.Etcd {
	t.Helper()

	etcd, _, _ := NewEtcdForTestWithTls(t, false)
	return etcd
}

func NewEtcdClientForTest(t *testing.T) (*embed.Etcd, *EtcdClient) {
	etcd := NewEtcdForTest(t)

	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().ListenClientUrls[0].String())
	config.AddOption("etcd", "loglevel", "error")

	logger := NewLoggerForTest(t)
	client, err := NewEtcdClient(logger, config, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
	})
	return etcd, client
}

func NewEtcdClientWithTLSForTest(t *testing.T) (*embed.Etcd, *EtcdClient) {
	etcd, keyfile, certfile := NewEtcdForTestWithTls(t, true)

	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().ListenClientUrls[0].String())
	config.AddOption("etcd", "loglevel", "error")
	config.AddOption("etcd", "clientkey", keyfile)
	config.AddOption("etcd", "clientcert", certfile)
	config.AddOption("etcd", "cacert", certfile)

	logger := NewLoggerForTest(t)
	client, err := NewEtcdClient(logger, config, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
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
	t.Parallel()
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	require := require.New(t)
	etcd, client := NewEtcdClientForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	if info := client.GetServerInfoEtcd(); assert.NotNil(info) {
		assert.NotEmpty(info.Active)
		assert.Equal([]string{
			etcd.Config().ListenClientUrls[0].String(),
		}, info.Endpoints)
		if connected := info.Connected; assert.NotNil(connected) {
			assert.False(*connected)
		}
	}

	require.NoError(client.WaitForConnection(ctx))

	if info := client.GetServerInfoEtcd(); assert.NotNil(info) {
		assert.NotEmpty(info.Active)
		assert.Equal([]string{
			etcd.Config().ListenClientUrls[0].String(),
		}, info.Endpoints)
		if connected := info.Connected; assert.NotNil(connected) {
			assert.True(*connected)
		}
	}

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		assert.EqualValues(0, response.Count)
	}

	SetEtcdValue(etcd, "foo", []byte("bar"))

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		if assert.EqualValues(1, response.Count) {
			assert.Equal("foo", string(response.Kvs[0].Key))
			assert.Equal("bar", string(response.Kvs[0].Value))
		}
	}
}

func Test_EtcdClientTLS_Get(t *testing.T) {
	t.Parallel()
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	require := require.New(t)
	etcd, client := NewEtcdClientWithTLSForTest(t)

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	if info := client.GetServerInfoEtcd(); assert.NotNil(info) {
		assert.NotEmpty(info.Active)
		assert.Equal([]string{
			etcd.Config().ListenClientUrls[0].String(),
		}, info.Endpoints)
		if connected := info.Connected; assert.NotNil(connected) {
			assert.False(*connected)
		}
	}

	require.NoError(client.WaitForConnection(ctx))

	if info := client.GetServerInfoEtcd(); assert.NotNil(info) {
		assert.NotEmpty(info.Active)
		assert.Equal([]string{
			etcd.Config().ListenClientUrls[0].String(),
		}, info.Endpoints)
		if connected := info.Connected; assert.NotNil(connected) {
			assert.True(*connected)
		}
	}

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		assert.EqualValues(0, response.Count)
	}

	SetEtcdValue(etcd, "foo", []byte("bar"))

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		if assert.EqualValues(1, response.Count) {
			assert.Equal("foo", string(response.Kvs[0].Key))
			assert.Equal("bar", string(response.Kvs[0].Value))
		}
	}
}

func Test_EtcdClient_GetPrefix(t *testing.T) {
	t.Parallel()
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	etcd, client := NewEtcdClientForTest(t)

	if response, err := client.Get(ctx, "foo"); assert.NoError(err) {
		assert.EqualValues(0, response.Count)
	}

	SetEtcdValue(etcd, "foo", []byte("1"))
	SetEtcdValue(etcd, "foo/lala", []byte("2"))
	SetEtcdValue(etcd, "lala/foo", []byte("3"))

	if response, err := client.Get(ctx, "foo", clientv3.WithPrefix()); assert.NoError(err) {
		if assert.EqualValues(2, response.Count) {
			assert.Equal("foo", string(response.Kvs[0].Key))
			assert.Equal("1", string(response.Kvs[0].Value))
			assert.Equal("foo/lala", string(response.Kvs[1].Key))
			assert.Equal("2", string(response.Kvs[1].Value))
		}
	}
}

type etcdEvent struct {
	t     mvccpb.Event_EventType
	key   string
	value string

	prevValue string
}

type EtcdClientTestListener struct {
	t *testing.T

	ctx    context.Context
	cancel context.CancelFunc

	initial chan struct{}
	events  chan etcdEvent
}

func NewEtcdClientTestListener(ctx context.Context, t *testing.T) *EtcdClientTestListener {
	ctx, cancel := context.WithCancel(ctx)
	return &EtcdClientTestListener{
		t: t,

		ctx:    ctx,
		cancel: cancel,

		initial: make(chan struct{}),
		events:  make(chan etcdEvent),
	}
}

func (l *EtcdClientTestListener) Close() {
	l.cancel()
}

func (l *EtcdClientTestListener) EtcdClientCreated(client *EtcdClient) {
	go func() {
		assert := assert.New(l.t)
		if err := client.WaitForConnection(l.ctx); !assert.NoError(err) {
			return
		}

		ctx, cancel := context.WithTimeout(l.ctx, time.Second)
		defer cancel()

		response, err := client.Get(ctx, "foo", clientv3.WithPrefix())
		if assert.NoError(err) && assert.EqualValues(1, response.Count) {
			assert.Equal("foo/a", string(response.Kvs[0].Key))
			assert.Equal("1", string(response.Kvs[0].Value))
		}

		close(l.initial)
		nextRevision := response.Header.Revision + 1
		for l.ctx.Err() == nil {
			var err error
			nextRevision, err = client.Watch(clientv3.WithRequireLeader(l.ctx), "foo", nextRevision, l, clientv3.WithPrefix())
			assert.NoError(err)
		}
	}()
}

func (l *EtcdClientTestListener) EtcdWatchCreated(client *EtcdClient, key string) {
}

func (l *EtcdClientTestListener) EtcdKeyUpdated(client *EtcdClient, key string, value []byte, prevValue []byte) {
	evt := etcdEvent{
		t:     clientv3.EventTypePut,
		key:   string(key),
		value: string(value),
	}
	if len(prevValue) > 0 {
		evt.prevValue = string(prevValue)
	}
	l.events <- evt
}

func (l *EtcdClientTestListener) EtcdKeyDeleted(client *EtcdClient, key string, prevValue []byte) {
	evt := etcdEvent{
		t:   clientv3.EventTypeDelete,
		key: string(key),
	}
	if len(prevValue) > 0 {
		evt.prevValue = string(prevValue)
	}
	l.events <- evt
}

func Test_EtcdClient_Watch(t *testing.T) {
	t.Parallel()
	logger := NewLoggerForTest(t)
	ctx := NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	etcd, client := NewEtcdClientForTest(t)

	SetEtcdValue(etcd, "foo/a", []byte("1"))

	listener := NewEtcdClientTestListener(ctx, t)
	defer listener.Close()

	client.AddListener(listener)
	defer client.RemoveListener(listener)

	<-listener.initial

	SetEtcdValue(etcd, "foo/b", []byte("2"))
	event := <-listener.events
	assert.Equal(clientv3.EventTypePut, event.t)
	assert.Equal("foo/b", event.key)
	assert.Equal("2", event.value)

	SetEtcdValue(etcd, "foo/a", []byte("3"))
	event = <-listener.events
	assert.Equal(clientv3.EventTypePut, event.t)
	assert.Equal("foo/a", event.key)
	assert.Equal("3", event.value)

	DeleteEtcdValue(etcd, "foo/a")
	event = <-listener.events
	assert.Equal(clientv3.EventTypeDelete, event.t)
	assert.Equal("foo/a", event.key)
	assert.Equal("3", event.prevValue)
}
