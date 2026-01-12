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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/dns"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/proxy"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/proxy/testserver"
)

const (
	testTimeout = 10 * time.Second
)

type ConnectionWaiter interface {
	WaitForConnections(ctx context.Context) error
	WaitForConnectionsEstablished(ctx context.Context, waitMap map[string]bool) error
}

func NewMcuProxyForTestWithOptions(t *testing.T, options testserver.ProxyTestOptions, idx int, lookup *dns.MockLookup) (sfu.SFU, *goconf.ConfigFile) {
	t.Helper()
	require := require.New(t)
	require.NotEmpty(options.Servers)
	if options.Etcd == nil {
		options.Etcd = etcdtest.NewServerForTest(t)
	}
	grpcClients, dnsMonitor := grpctest.NewClientsWithEtcdForTest(t, options.Etcd, lookup)

	tokenKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	require.NoError(internal.WritePrivateKey(tokenKey, privkeyFile))
	require.NoError(internal.WritePublicKey(&tokenKey.PublicKey, pubkeyFile))

	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "urltype", "static")
	if strings.Contains(t.Name(), "DnsDiscovery") {
		cfg.AddOption("mcu", "dnsdiscovery", "true")
	}
	cfg.AddOption("mcu", "proxytimeout", strconv.Itoa(int(testTimeout.Seconds())))
	var urls []string
	waitingMap := make(map[string]bool)
	tokenId := fmt.Sprintf("test-token-%d", idx)
	for _, s := range options.Servers {
		s.SetServers(options.Servers)
		s.SetToken(tokenId, &tokenKey.PublicKey)
		urls = append(urls, s.URL())
		waitingMap[s.URL()] = true
	}
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	cfg.AddOption("mcu", "token_id", tokenId)
	cfg.AddOption("mcu", "token_key", privkeyFile)

	etcdConfig := goconf.NewConfigFile()
	etcdConfig.AddOption("etcd", "endpoints", options.Etcd.URL().String())
	etcdConfig.AddOption("etcd", "loglevel", "error")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	etcdClient, err := etcd.NewClient(logger, etcdConfig, "")
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(t, etcdClient.Close())
	})

	mcu, err := proxy.NewProxySFU(ctx, cfg, etcdClient, grpcClients, dnsMonitor)
	require.NoError(err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(mcu.Start(ctx))

	waiter, ok := mcu.(ConnectionWaiter)
	require.True(ok, "can't wait for connections")

	require.NoError(waiter.WaitForConnections(ctx))
	require.NoError(waiter.WaitForConnectionsEstablished(ctx, waitingMap))

	return mcu, cfg
}
