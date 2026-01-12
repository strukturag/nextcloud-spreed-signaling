/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/dns"
	dnstest "github.com/strukturag/nextcloud-spreed-signaling/dns/test"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
)

func NewClientsForTestWithConfig(t *testing.T, config *goconf.ConfigFile, etcdClient etcd.Client, lookup *dnstest.MockLookup) (*grpc.Clients, *dns.Monitor) {
	dnsMonitor := dnstest.NewMonitorForTest(t, time.Hour, lookup) // will be updated manually
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	client, err := grpc.NewClients(ctx, config, etcdClient, dnsMonitor, "0.0.0")
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	return client, dnsMonitor
}

func NewClientsForTest(t *testing.T, addr string, lookup *dnstest.MockLookup) (*grpc.Clients, *dns.Monitor) {
	config := goconf.NewConfigFile()
	config.AddOption("grpc", "targets", addr)
	config.AddOption("grpc", "dnsdiscovery", "true")

	return NewClientsForTestWithConfig(t, config, nil, lookup)
}

func NewClientsWithEtcdForTest(t *testing.T, embedEtcd *etcdtest.Server, lookup *dnstest.MockLookup) (*grpc.Clients, *dns.Monitor) {
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
