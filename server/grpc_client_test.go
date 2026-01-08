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
package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

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
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		_, addr1 := NewGrpcServerForTest(t)
		_, addr2 := NewGrpcServerForTest(t)

		embedEtcd := etcdtest.NewServerForTest(t)

		embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
		embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

		client, _ := grpctest.NewClientsWithEtcdForTest(t, embedEtcd, nil)
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
	embedEtcd := etcdtest.NewServerForTest(t)
	client, _ := grpctest.NewClientsWithEtcdForTest(t, embedEtcd, nil)
	ch := client.GetWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	test.DrainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	_, addr2 := NewGrpcServerForTest(t)
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
	_, addr3 := NewGrpcServerForTest(t)
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr3+"\"}"))
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
	embedEtcd := etcdtest.NewServerForTest(t)
	client, _ := grpctest.NewClientsWithEtcdForTest(t, embedEtcd, nil)
	ch := client.GetWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	assert.Empty(client.GetClients())

	test.DrainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); assert.Len(clients, 1) {
		assert.Equal(addr1, clients[0].Target())
	}

	test.DrainWakeupChannel(ch)
	server2, addr2 := NewGrpcServerForTest(t)
	server2.serverId = grpc.ServerId
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
