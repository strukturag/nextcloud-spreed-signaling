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
package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/dns"
	dnstest "github.com/strukturag/nextcloud-spreed-signaling/dns/test"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

func NewClientsForTestWithConfig(t *testing.T, config *goconf.ConfigFile, etcdClient etcd.Client, lookup *dnstest.MockLookup) (*Clients, *dns.Monitor) {
	dnsMonitor := dnstest.NewMonitorForTest(t, time.Hour, lookup) // will be updated manually
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	client, err := NewClients(ctx, config, etcdClient, dnsMonitor, "0.0.0")
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	return client, dnsMonitor
}

func NewClientsForTest(t *testing.T, addr string, lookup *dnstest.MockLookup) (*Clients, *dns.Monitor) {
	config := goconf.NewConfigFile()
	config.AddOption("grpc", "targets", addr)
	config.AddOption("grpc", "dnsdiscovery", "true")

	return NewClientsForTestWithConfig(t, config, nil, lookup)
}

func NewClientsWithEtcdForTest(t *testing.T, embedEtcd *etcdtest.Server, lookup *dnstest.MockLookup) (*Clients, *dns.Monitor) {
	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", embedEtcd.URL().String())

	config.AddOption("grpc", "targettype", "etcd")
	config.AddOption("grpc", "targetprefix", "/grpctargets")

	logger := logtest.NewLoggerForTest(t)
	etcdClient, err := etcd.NewClient(logger, config, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, etcdClient.Close())
	})

	return NewClientsForTestWithConfig(t, config, etcdClient, lookup)
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

func Test_GrpcClients_DnsDiscovery(t *testing.T) { // nolint:paralleltest
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)
		lookup := dnstest.NewMockLookup()
		target := "testgrpc:12345"
		ip1 := net.ParseIP("192.168.0.1")
		ip2 := net.ParseIP("192.168.0.2")
		targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
		targetWithIp2 := fmt.Sprintf("%s (%s)", target, ip2)
		lookup.Set("testgrpc", []net.IP{ip1})
		client, dnsMonitor := NewClientsForTest(t, target, lookup)
		ch := client.GetWakeupChannelForTesting()

		ctx, cancel := context.WithTimeout(ctx, testTimeout)
		defer cancel()

		// Wait for initial check to be done to make sure internal dnsmonitor goroutine is waiting.
		if err := dnsMonitor.WaitForTicker(ctx); err != nil {
			require.NoError(err)
		}

		test.DrainWakeupChannel(ch)
		dnsMonitor.CheckHostnames()
		if clients := client.GetClients(); assert.Len(clients, 1) {
			assert.Equal(targetWithIp1, clients[0].Target())
			assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
		}

		lookup.Set("testgrpc", []net.IP{ip1, ip2})
		test.DrainWakeupChannel(ch)
		dnsMonitor.CheckHostnames()
		waitForEvent(ctx, t, ch)

		if clients := client.GetClients(); assert.Len(clients, 2) {
			assert.Equal(targetWithIp1, clients[0].Target())
			assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
			assert.Equal(targetWithIp2, clients[1].Target())
			assert.True(clients[1].ip.Equal(ip2), "Expected IP %s, got %s", ip2, clients[1].ip)
		}

		lookup.Set("testgrpc", []net.IP{ip2})
		test.DrainWakeupChannel(ch)
		dnsMonitor.CheckHostnames()
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
	lookup := dnstest.NewMockLookup()
	target := "testgrpc:12345"
	ip1 := net.ParseIP("192.168.0.1")
	targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
	client, dnsMonitor := NewClientsForTest(t, target, lookup)
	ch := client.GetWakeupChannelForTesting()

	testCtx, testCtxCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCtxCancel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, client.WaitForInitialized(ctx))

	assert.Empty(client.GetClients())

	lookup.Set("testgrpc", []net.IP{ip1})
	test.DrainWakeupChannel(ch)
	dnsMonitor.CheckHostnames()
	waitForEvent(testCtx, t, ch)

	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(targetWithIp1, clients[0].Target())
		assert.True(clients[0].ip.Equal(ip1), "Expected IP %s, got %s", ip1, clients[0].ip)
	}
}

func Test_GrpcClients_EtcdInitial(t *testing.T) { // nolint:paralleltest
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		_, addr1 := NewServerForTest(t)
		_, addr2 := NewServerForTest(t)

		embedEtcd := etcdtest.NewServerForTest(t)

		embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
		embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

		client, _ := NewClientsWithEtcdForTest(t, embedEtcd, nil)
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		require.NoError(t, client.WaitForInitialized(ctx))

		clients := client.GetClients()
		assert.Len(t, clients, 2, "Expected two clients, got %+v", clients)
	})
}

func Test_GrpcClients_EtcdUpdate(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	embedEtcd := etcdtest.NewServerForTest(t)
	client, _ := NewClientsWithEtcdForTest(t, embedEtcd, nil)
	ch := client.GetWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	test.DrainWakeupChannel(ch)
	_, addr1 := NewServerForTest(t)
	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	_, addr2 := NewServerForTest(t)
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 2) {
		assert.Equal(addr1, clients[0].Target())
		assert.Equal(addr2, clients[1].Target())
	}

	test.DrainWakeupChannel(ch)
	embedEtcd.DeleteValue("/grpctargets/one")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr2, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	_, addr3 := NewServerForTest(t)
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr3+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr3, clients[0].Target())
	}
}

func Test_GrpcClients_EtcdIgnoreSelf(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	assert := assert.New(t)
	embedEtcd := etcdtest.NewServerForTest(t)
	client, _ := NewClientsWithEtcdForTest(t, embedEtcd, nil)
	ch := client.GetWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	test.DrainWakeupChannel(ch)
	_, addr1 := NewServerForTest(t)
	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	server2, addr2 := NewServerForTest(t)
	server2.serverId = ServerId
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	client.WaitForSelfCheck()
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	embedEtcd.DeleteValue("/grpctargets/two")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}
}
