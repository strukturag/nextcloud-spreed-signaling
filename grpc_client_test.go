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
	"fmt"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func (c *GrpcClients) getWakeupChannelForTesting() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wakeupChanForTesting != nil {
		return c.wakeupChanForTesting
	}

	ch := make(chan struct{}, 1)
	c.wakeupChanForTesting = ch
	return ch
}

func NewGrpcClientsForTestWithConfig(t *testing.T, config *goconf.ConfigFile, etcdClient *EtcdClient, lookup *mockDnsLookup) (*GrpcClients, *DnsMonitor) {
	dnsMonitor := newDnsMonitorForTest(t, time.Hour, lookup) // will be updated manually
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	client, err := NewGrpcClients(ctx, config, etcdClient, dnsMonitor, "0.0.0")
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	return client, dnsMonitor
}

func NewGrpcClientsForTest(t *testing.T, addr string, lookup *mockDnsLookup) (*GrpcClients, *DnsMonitor) {
	config := goconf.NewConfigFile()
	config.AddOption("grpc", "targets", addr)
	config.AddOption("grpc", "dnsdiscovery", "true")

	return NewGrpcClientsForTestWithConfig(t, config, nil, lookup)
}

func NewGrpcClientsWithEtcdForTest(t *testing.T, etcd *embed.Etcd, lookup *mockDnsLookup) (*GrpcClients, *DnsMonitor) {
	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().ListenClientUrls[0].String())

	config.AddOption("grpc", "targettype", "etcd")
	config.AddOption("grpc", "targetprefix", "/grpctargets")

	logger := log.NewLoggerForTest(t)
	etcdClient, err := NewEtcdClient(logger, config, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, etcdClient.Close())
	})

	return NewGrpcClientsForTestWithConfig(t, config, etcdClient, lookup)
}

func drainWakeupChannel(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitForEvent(ctx context.Context, t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
		return
	case <-ctx.Done():
		assert.Fail(t, "timeout waiting for event")
	}
}

func Test_GrpcClients_EtcdInitial(t *testing.T) { // nolint:paralleltest
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		_, addr1 := NewGrpcServerForTest(t)
		_, addr2 := NewGrpcServerForTest(t)

		etcd := NewEtcdForTest(t)

		SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
		SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

		client, _ := NewGrpcClientsWithEtcdForTest(t, etcd, nil)
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		require.NoError(t, client.WaitForInitialized(ctx))

		clients := client.GetClients()
		assert.Len(t, clients, 2, "Expected two clients, got %+v", clients)
	})
}

func Test_GrpcClients_EtcdUpdate(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	etcd := NewEtcdForTest(t)
	client, _ := NewGrpcClientsWithEtcdForTest(t, etcd, nil)
	ch := client.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr2 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 2) {
		assert.Equal(addr1, clients[0].Target())
		assert.Equal(addr2, clients[1].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/one")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr2, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr3 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr3+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr3, clients[0].Target())
	}
}

func Test_GrpcClients_EtcdIgnoreSelf(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	etcd := NewEtcdForTest(t)
	client, _ := NewGrpcClientsWithEtcdForTest(t, etcd, nil)
	ch := client.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	server2, addr2 := NewGrpcServerForTest(t)
	server2.serverId = GrpcServerId
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	client.selfCheckWaitGroup.Wait()
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/two")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}
}

func Test_GrpcClients_DnsDiscovery(t *testing.T) { // nolint:paralleltest
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)
		lookup := newMockDnsLookupForTest(t)
		target := "testgrpc:12345"
		ip1 := net.ParseIP("192.168.0.1")
		ip2 := net.ParseIP("192.168.0.2")
		targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
		targetWithIp2 := fmt.Sprintf("%s (%s)", target, ip2)
		lookup.Set("testgrpc", []net.IP{ip1})
		client, dnsMonitor := NewGrpcClientsForTest(t, target, lookup)
		ch := client.getWakeupChannelForTesting()

		ctx, cancel := context.WithTimeout(ctx, testTimeout)
		defer cancel()

		// Wait for initial check to be done to make sure internal dnsmonitor goroutine is waiting.
		if err := dnsMonitor.waitForTicker(ctx); err != nil {
			require.NoError(err)
		}

		drainWakeupChannel(ch)
		dnsMonitor.checkHostnames()
		if clients := client.GetClients(); assert.Len(clients, 1) {
			assert.Equal(targetWithIp1, clients[0].Target())
			assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
		}

		lookup.Set("testgrpc", []net.IP{ip1, ip2})
		drainWakeupChannel(ch)
		dnsMonitor.checkHostnames()
		waitForEvent(ctx, t, ch)

		if clients := client.GetClients(); assert.Len(clients, 2) {
			assert.Equal(targetWithIp1, clients[0].Target())
			assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
			assert.Equal(targetWithIp2, clients[1].Target())
			assert.True(clients[1].ip.Equal(ip2), "Expected IP %s, got %s", ip2, clients[1].ip)
		}

		lookup.Set("testgrpc", []net.IP{ip2})
		drainWakeupChannel(ch)
		dnsMonitor.checkHostnames()
		waitForEvent(ctx, t, ch)

		if clients := client.GetClients(); assert.Len(clients, 1) {
			assert.Equal(targetWithIp2, clients[0].Target())
			assert.True(clients[0].ip.Equal(ip2), "Expected IP %s, got %s", ip2, clients[0].ip)
		}
	})
}

func Test_GrpcClients_DnsDiscoveryInitialFailed(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	lookup := newMockDnsLookupForTest(t)
	target := "testgrpc:12345"
	ip1 := net.ParseIP("192.168.0.1")
	targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
	client, dnsMonitor := NewGrpcClientsForTest(t, target, lookup)
	ch := client.getWakeupChannelForTesting()

	testCtx, testCtxCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCtxCancel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, client.WaitForInitialized(ctx))

	assert.Empty(client.GetClients())

	lookup.Set("testgrpc", []net.IP{ip1})
	drainWakeupChannel(ch)
	dnsMonitor.checkHostnames()
	waitForEvent(testCtx, t, ch)

	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(targetWithIp1, clients[0].Target())
		assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
	}
}

func Test_GrpcClients_Encryption(t *testing.T) { // nolint:paralleltest
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		require := require.New(t)
		serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)
		clientKey, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)

		serverCert := GenerateSelfSignedCertificateForTesting(t, 1024, "Server cert", serverKey)
		clientCert := GenerateSelfSignedCertificateForTesting(t, 1024, "Testing client", clientKey)

		dir := t.TempDir()
		serverPrivkeyFile := path.Join(dir, "server-privkey.pem")
		serverPubkeyFile := path.Join(dir, "server-pubkey.pem")
		serverCertFile := path.Join(dir, "server-cert.pem")
		WritePrivateKey(serverKey, serverPrivkeyFile)          // nolint
		WritePublicKey(&serverKey.PublicKey, serverPubkeyFile) // nolint
		os.WriteFile(serverCertFile, serverCert, 0755)         // nolint
		clientPrivkeyFile := path.Join(dir, "client-privkey.pem")
		clientPubkeyFile := path.Join(dir, "client-pubkey.pem")
		clientCertFile := path.Join(dir, "client-cert.pem")
		WritePrivateKey(clientKey, clientPrivkeyFile)          // nolint
		WritePublicKey(&clientKey.PublicKey, clientPubkeyFile) // nolint
		os.WriteFile(clientCertFile, clientCert, 0755)         // nolint

		serverConfig := goconf.NewConfigFile()
		serverConfig.AddOption("grpc", "servercertificate", serverCertFile)
		serverConfig.AddOption("grpc", "serverkey", serverPrivkeyFile)
		serverConfig.AddOption("grpc", "clientca", clientCertFile)
		_, addr := NewGrpcServerForTestWithConfig(t, serverConfig)

		clientConfig := goconf.NewConfigFile()
		clientConfig.AddOption("grpc", "targets", addr)
		clientConfig.AddOption("grpc", "clientcertificate", clientCertFile)
		clientConfig.AddOption("grpc", "clientkey", clientPrivkeyFile)
		clientConfig.AddOption("grpc", "serverca", serverCertFile)
		clients, _ := NewGrpcClientsForTestWithConfig(t, clientConfig, nil, nil)

		ctx, cancel1 := context.WithTimeout(context.Background(), time.Second)
		defer cancel1()

		require.NoError(clients.WaitForInitialized(ctx))

		for _, client := range clients.GetClients() {
			_, _, err := client.GetServerId(ctx)
			require.NoError(err)
		}
	})
}
