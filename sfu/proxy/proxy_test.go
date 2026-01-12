/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
package proxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	dnstest "github.com/strukturag/nextcloud-spreed-signaling/dns/test"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	metricstest "github.com/strukturag/nextcloud-spreed-signaling/metrics/test"
	"github.com/strukturag/nextcloud-spreed-signaling/proxy"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/proxy/testserver"
)

const (
	testTimeout = 10 * time.Second
)

func TestMcuProxyStats(t *testing.T) {
	t.Parallel()
	metricstest.CollectAndLint(t, proxyMcuStats...)
}

func newProxyConnectionWithCountry(country geoip.Country) *proxyConnection {
	conn := &proxyConnection{}
	conn.country.Store(country)
	return conn
}

func Test_sortConnectionsForCountry(t *testing.T) {
	t.Parallel()
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[geoip.Country][][]*proxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
	}

	for country, test := range testcases {
		t.Run(string(country), func(t *testing.T) {
			t.Parallel()
			sorted := sortConnectionsForCountry(test[0], country, nil)
			for idx, conn := range sorted {
				assert.Equal(t, test[1][idx], conn, "Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
			}
		})
	}
}

func Test_sortConnectionsForCountryWithOverride(t *testing.T) {
	t.Parallel()
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[geoip.Country][][]*proxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AR": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
		// No match but override (OC -> AS / NA)
		"AU": {
			{conn_us, conn_jp},
			{conn_us, conn_jp},
		},
		// No match but override (AF -> EU)
		"ZA": {
			{conn_de, conn_at},
			{conn_de, conn_at},
		},
	}

	continentMap := ContinentsMap{
		// Use European connections for Africa.
		"AF": {"EU"},
		// Use Asian and North American connections for Oceania.
		"OC": {"AS", "NA"},
	}
	for country, test := range testcases {
		t.Run(string(country), func(t *testing.T) {
			t.Parallel()
			sorted := sortConnectionsForCountry(test[0], country, continentMap)
			for idx, conn := range sorted {
				assert.Equal(t, test[1][idx], conn, "Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
			}
		})
	}
}

func newMcuProxyForTestWithOptions(t *testing.T, options testserver.ProxyTestOptions, idx int, lookup *dnstest.MockLookup) (*proxySFU, *goconf.ConfigFile) {
	t.Helper()
	require := require.New(t)
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
	if len(options.Servers) == 0 {
		options.Servers = []testserver.ProxyTestServer{
			testserver.NewProxyServerForTest(t, "DE"),
		}
	}
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

	mcu, err := NewProxySFU(ctx, cfg, etcdClient, grpcClients, dnsMonitor)
	require.NoError(err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(mcu.Start(ctx))

	proxy := mcu.(*proxySFU)

	require.NoError(proxy.WaitForConnections(ctx))

	for len(waitingMap) > 0 {
		require.NoError(ctx.Err())

		for u := range waitingMap {
			proxy.connectionsMu.RLock()
			connections := proxy.connections
			proxy.connectionsMu.RUnlock()
			for _, c := range connections {
				if c.rawUrl == u && c.IsConnected() && c.SessionId() != "" {
					delete(waitingMap, u)
					break
				}
			}
		}

		time.Sleep(time.Millisecond)
	}

	return proxy, cfg
}

func newMcuProxyForTestWithServers(t *testing.T, servers []testserver.ProxyTestServer, idx int, lookup *dnstest.MockLookup) *proxySFU {
	t.Helper()

	proxy, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: servers,
	}, idx, lookup)
	return proxy
}

func newMcuProxyForTest(t *testing.T, idx int, lookup *dnstest.MockLookup) *proxySFU {
	t.Helper()
	server := testserver.NewProxyServerForTest(t, "DE")

	return newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{server}, idx, lookup)
}

func Test_ProxyAddRemoveConnections(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	server1 := testserver.NewProxyServerForTest(t, "DE")
	mcu, config := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{
			server1,
		},
	}, 0, nil)

	server2 := testserver.NewProxyServerForTest(t, "DE")
	server1.Servers = append(server1.Servers, server2)
	server2.Servers = server1.Servers
	server2.Tokens = server1.Tokens
	urls1 := []string{
		server1.URL(),
		server2.URL(),
	}
	config.AddOption("mcu", "url", strings.Join(urls1, " "))
	mcu.Reload(config)

	mcu.connectionsMu.RLock()
	assert.Len(mcu.connections, 2)
	mcu.connectionsMu.RUnlock()

	// Wait until connection is established.
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	for waitCtx.Err() == nil {
		mcu.connectionsMu.RLock()
		notAllConnected := slices.ContainsFunc(mcu.connections, func(conn *proxyConnection) bool {
			return !conn.IsConnected()
		})
		mcu.connectionsMu.RUnlock()
		if notAllConnected {
			time.Sleep(time.Millisecond)
			continue
		}

		break
	}
	assert.NoError(waitCtx.Err(), "error while waiting for connection to be established")

	urls2 := []string{
		server2.URL(),
	}
	config.AddOption("mcu", "url", strings.Join(urls2, " "))
	mcu.Reload(config)

	// Removing the connections takes a short while (asynchronously, closed when unused).
	waitCtx, cancel = context.WithTimeout(ctx, time.Second)
	defer cancel()

	for waitCtx.Err() == nil {
		mcu.connectionsMu.RLock()
		if len(mcu.connections) != 1 {
			mcu.connectionsMu.RUnlock()
			time.Sleep(time.Millisecond)
			continue
		}

		assert.Len(mcu.connections, 1)
		assert.Equal(server2.URL(), mcu.connections[0].rawUrl)
		mcu.connectionsMu.RUnlock()
		break
	}
	assert.NoError(waitCtx.Err(), "error while waiting for connection to be removed")
}

func Test_ProxyAddRemoveConnectionsDnsDiscovery(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	lookup := dnstest.NewMockLookup()

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server1.Start()
	h, port, err := net.SplitHostPort(server1.Listener().Addr().String())
	require.NoError(err)
	ip1 := net.ParseIP(h)
	require.NotNil(ip1, "failed for %s", h)

	require.Contains(server1.URL(), ip1.String())
	server1.SetURL(strings.ReplaceAll(server1.URL(), ip1.String(), "proxydomain.invalid"))
	u1, err := url.Parse(server1.URL())
	require.NoError(err)
	lookup.Set(u1.Hostname(), []net.IP{
		ip1,
	})

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{
			server1,
		},
	}, 0, lookup)

	if connections := mcu.getConnections(); assert.Len(connections, 1) && assert.NotNil(connections[0].ip) {
		assert.True(ip1.Equal(connections[0].ip), "ip addresses differ: expected %s, got %s", ip1.String(), connections[0].ip.String())
	}

	dnsMonitor := mcu.config.(*configStatic).dnsMonitor
	require.NotNil(dnsMonitor)

	server2 := testserver.NewProxyServerForTest(t, "DE")
	l, err := net.Listen("tcp", "127.0.0.2:"+port)
	require.NoError(err)
	assert.NoError(server2.Listener().Close())
	server2.SetListener(l)
	server2.Start()

	h, _, err = net.SplitHostPort(server2.Listener().Addr().String())
	require.NoError(err)
	ip2 := net.ParseIP(h)
	require.NotNil(ip2, "failed for %s", h)
	require.Contains(server2.URL(), ip2.String())
	server2.SetURL(strings.ReplaceAll(server2.URL(), ip2.String(), "proxydomain.invalid"))

	server1.Servers = append(server1.Servers, server2)
	server2.Servers = server1.Servers
	server2.Tokens = server1.Tokens

	lookup.Set(u1.Hostname(), []net.IP{
		ip1,
		ip2,
	})
	dnsMonitor.CheckHostnames()

	// Wait until connection is established.
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	for waitCtx.Err() == nil {
		mcu.connectionsMu.RLock()
		if len(mcu.connections) != 2 {
			mcu.connectionsMu.RUnlock()
			time.Sleep(time.Millisecond)
			continue
		}

		notAllConnected := slices.ContainsFunc(mcu.connections, func(conn *proxyConnection) bool {
			return !conn.IsConnected()
		})
		mcu.connectionsMu.RUnlock()
		if notAllConnected {
			time.Sleep(time.Millisecond)
			continue
		}

		break
	}
	assert.NoError(waitCtx.Err(), "error while waiting for connection to be established")

	lookup.Set(u1.Hostname(), []net.IP{
		ip2,
	})
	dnsMonitor.CheckHostnames()

	// Removing the connections takes a short while (asynchronously, closed when unused).
	waitCtx, cancel = context.WithTimeout(ctx, time.Second)
	defer cancel()

	for waitCtx.Err() == nil {
		mcu.connectionsMu.RLock()
		if len(mcu.connections) != 1 {
			mcu.connectionsMu.RUnlock()
			time.Sleep(time.Millisecond)
			continue
		}

		assert.Len(mcu.connections, 1)
		assert.Equal(server1.URL(), mcu.connections[0].rawUrl)
		if assert.NotNil(mcu.connections[0].ip) {
			assert.True(ip2.Equal(mcu.connections[0].ip), "ip addresses differ: expected %s, got %s", ip2.String(), mcu.connections[0].ip.String())
		}
		mcu.connectionsMu.RUnlock()
		break
	}
	assert.NoError(waitCtx.Err(), "error while waiting for connection to be removed")
}

func Test_ProxyPublisherSubscriber(t *testing.T) {
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")

	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyPublisherCodecs(t *testing.T) {
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
		AudioCodec: "opus,g722",
		VideoCodec: "vp9,vp8,av1",
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())
}

func Test_ProxyWaitForPublisher(t *testing.T) {
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")
	done := make(chan struct{})
	go func() {
		defer close(done)
		sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
		if !assert.NoError(t, err) {
			return
		}

		defer sub.Close(context.Background())
	}()

	// Give subscriber goroutine some time to start
	time.Sleep(100 * time.Millisecond)

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	select {
	case <-done:
	case <-ctx.Done():
		assert.NoError(t, ctx.Err())
	}
	defer pub.Close(context.Background())
}

func Test_ProxyPublisherBandwidth(t *testing.T) {
	t.Parallel()
	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		server1,
		server2,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pub1Id := api.PublicSessionId("the-publisher-1")
	pub1Sid := "1234567890"
	pub1Listener := mock.NewListener(pub1Id + "-public")
	pub1Initiator := mock.NewInitiator("DE")
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	if pub1.(*proxyPublisher).conn.rawUrl == server1.URL() {
		server1.UpdateBandwidth(100, 0)
	} else {
		server2.UpdateBandwidth(100, 0)
	}

	// Wait until proxy has been updated
	for assert.NoError(t, ctx.Err()) {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := true
		for _, c := range connections {
			if c.Bandwidth() != nil {
				missing = false
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	pub2Id := api.PublicSessionId("the-publisher-2")
	pub2id := "1234567890"
	pub2Listener := mock.NewListener(pub2Id + "-public")
	pub2Initiator := mock.NewInitiator("DE")
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.NotEqual(t, pub1.(*proxyPublisher).conn.rawUrl, pub2.(*proxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherBandwidthOverload(t *testing.T) {
	t.Parallel()
	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		server1,
		server2,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pub1Id := api.PublicSessionId("the-publisher-1")
	pub1Sid := "1234567890"
	pub1Listener := mock.NewListener(pub1Id + "-public")
	pub1Initiator := mock.NewInitiator("DE")
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	// If all servers are bandwidth loaded, select the one with the least usage.
	if pub1.(*proxyPublisher).conn.rawUrl == server1.URL() {
		server1.UpdateBandwidth(100, 0)
		server2.UpdateBandwidth(102, 0)
	} else {
		server1.UpdateBandwidth(102, 0)
		server2.UpdateBandwidth(100, 0)
	}

	// Wait until proxy has been updated
	for assert.NoError(t, ctx.Err()) {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := false
		for _, c := range connections {
			if c.Bandwidth() == nil {
				missing = true
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	pub2Id := api.PublicSessionId("the-publisher-2")
	pub2id := "1234567890"
	pub2Listener := mock.NewListener(pub2Id + "-public")
	pub2Initiator := mock.NewInitiator("DE")
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.Equal(t, pub1.(*proxyPublisher).conn.rawUrl, pub2.(*proxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherLoad(t *testing.T) {
	t.Parallel()
	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		server1,
		server2,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pub1Id := api.PublicSessionId("the-publisher-1")
	pub1Sid := "1234567890"
	pub1Listener := mock.NewListener(pub1Id + "-public")
	pub1Initiator := mock.NewInitiator("DE")
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	// Make sure connections are re-sorted.
	mcu.nextSort.Store(0)
	time.Sleep(100 * time.Millisecond)

	pub2Id := api.PublicSessionId("the-publisher-2")
	pub2id := "1234567890"
	pub2Listener := mock.NewListener(pub2Id + "-public")
	pub2Initiator := mock.NewInitiator("DE")
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.NotEqual(t, pub1.(*proxyPublisher).conn.rawUrl, pub2.(*proxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherCountry(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubDEId := api.PublicSessionId("the-publisher-de")
	pubDESid := "1234567890"
	pubDEListener := mock.NewListener(pubDEId + "-public")
	pubDEInitiator := mock.NewInitiator("DE")
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubDEInitiator)
	require.NoError(t, err)

	defer pubDE.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pubDE.(*proxyPublisher).conn.rawUrl)

	pubUSId := api.PublicSessionId("the-publisher-us")
	pubUSSid := "1234567890"
	pubUSListener := mock.NewListener(pubUSId + "-public")
	pubUSInitiator := mock.NewInitiator("US")
	pubUS, err := mcu.NewPublisher(ctx, pubUSListener, pubUSId, pubUSSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubUSInitiator)
	require.NoError(t, err)

	defer pubUS.Close(context.Background())

	assert.Equal(t, serverUS.URL(), pubUS.(*proxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherContinent(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubDEId := api.PublicSessionId("the-publisher-de")
	pubDESid := "1234567890"
	pubDEListener := mock.NewListener(pubDEId + "-public")
	pubDEInitiator := mock.NewInitiator("DE")
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubDEInitiator)
	require.NoError(t, err)

	defer pubDE.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pubDE.(*proxyPublisher).conn.rawUrl)

	pubFRId := api.PublicSessionId("the-publisher-fr")
	pubFRSid := "1234567890"
	pubFRListener := mock.NewListener(pubFRId + "-public")
	pubFRInitiator := mock.NewInitiator("FR")
	pubFR, err := mcu.NewPublisher(ctx, pubFRListener, pubFRId, pubFRSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubFRInitiator)
	require.NoError(t, err)

	defer pubFR.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pubFR.(*proxyPublisher).conn.rawUrl)
}

func Test_ProxySubscriberCountry(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pub.(*proxyPublisher).conn.rawUrl)

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("US")
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverUS.URL(), sub.(*proxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberContinent(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pub.(*proxyPublisher).conn.rawUrl)

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("FR")
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), sub.(*proxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberBandwidth(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pub.(*proxyPublisher).conn.rawUrl)

	serverDE.UpdateBandwidth(0, 100)

	// Wait until proxy has been updated
	for assert.NoError(t, ctx.Err()) {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := true
		for _, c := range connections {
			if c.Bandwidth() != nil {
				missing = false
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("US")
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverUS.URL(), sub.(*proxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberBandwidthOverload(t *testing.T) {
	t.Parallel()
	serverDE := testserver.NewProxyServerForTest(t, "DE")
	serverUS := testserver.NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []testserver.ProxyTestServer{
		serverDE,
		serverUS,
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), pub.(*proxyPublisher).conn.rawUrl)

	serverDE.UpdateBandwidth(0, 100)
	serverUS.UpdateBandwidth(0, 102)

	// Wait until proxy has been updated
	for assert.NoError(t, ctx.Err()) {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := false
		for _, c := range connections {
			if c.Bandwidth() == nil {
				missing = true
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("US")
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverDE.URL(), sub.(*proxySubscriber).conn.rawUrl)
}

func Test_ProxyPublisherTimeout(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	server := testserver.NewProxyServerForTest(t, "DE")
	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{server},
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	settings := mcu.settings.(*proxySettings)
	settings.SetTimeout(testserver.TimeoutTestTimeout)

	// Creating the publisher will timeout locally.
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	if pub != nil {
		defer pub.Close(context.Background())
	}
	assert.ErrorContains(err, "no MCU connection available")

	// Wait for publisher to be created on the proxy side.
	require.NoError(server.WaitForWakeup(ctx), "publisher not created")

	// The local side will remove the (unused) publisher from the proxy.
	require.NoError(server.WaitForWakeup(ctx), "unused publisher not deleted")
}

func Test_ProxySubscriberTimeout(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	server := testserver.NewProxyServerForTest(t, "DE")
	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{server},
	}, 0, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(err)
	defer pub.Close(context.Background())

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")

	settings := mcu.settings.(*proxySettings)
	settings.SetTimeout(testserver.TimeoutTestTimeout)

	// Creating the subscriber will timeout locally.
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	if sub != nil {
		defer sub.Close(context.Background())
	}
	assert.ErrorIs(err, context.DeadlineExceeded)

	// Wait for subscriber to be created on the proxy side.
	require.NoError(server.WaitForWakeup(ctx), "subscriber not created")

	// The local side will remove the (unused) subscriber from the proxy.
	require.NoError(server.WaitForWakeup(ctx), "unused subscriber not deleted")
}

func Test_ProxyReconnectAfter(t *testing.T) {
	t.Parallel()
	reasons := []string{
		"session_resumed",
		"session_expired",
		"session_closed",
		"unknown_reason",
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			server := testserver.NewProxyServerForTest(t, "DE")
			mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
				Servers: []testserver.ProxyTestServer{server},
			}, 0, nil)

			connections := mcu.getSortedConnections(nil)
			require.Len(connections, 1)
			sessionId := connections[0].SessionId()

			ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
			defer cancel()

			client := server.GetSingleClient()
			require.NotNil(client)

			client.SendMessage(&proxy.ServerMessage{
				Type: "bye",
				Bye: &proxy.ByeServerMessage{
					Reason: reason,
				},
			})

			// The "bye" will close the connection and reset the session id.
			assert.NoError(mcu.WaitForDisconnected(ctx))

			// The client will automatically reconnect.
			time.Sleep(10 * time.Millisecond)
			assert.NoError(mcu.WaitForConnections(ctx))

			if connections := mcu.getSortedConnections(nil); assert.Len(connections, 1) {
				assert.NotEqual(sessionId, connections[0].SessionId())
			}
		})
	}
}

func Test_ProxyReconnectAfterShutdown(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	server := testserver.NewProxyServerForTest(t, "DE")
	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{server},
	}, 0, nil)

	connections := mcu.getSortedConnections(nil)
	require.Len(connections, 1)
	sessionId := connections[0].SessionId()

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client := server.GetSingleClient()
	require.NotNil(client)

	client.SendMessage(&proxy.ServerMessage{
		Type: "event",
		Event: &proxy.EventServerMessage{
			Type: "shutdown-scheduled",
		},
	})

	// Force reconnect.
	client.Close()
	assert.NoError(mcu.WaitForDisconnected(ctx))

	// The client will automatically reconnect and resume the session.
	time.Sleep(10 * time.Millisecond)
	assert.NoError(mcu.WaitForConnections(ctx))

	if connections := mcu.getSortedConnections(nil); assert.Len(connections, 1) {
		assert.Equal(sessionId, connections[0].SessionId())
	}
}

func Test_ProxyResume(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	server := testserver.NewProxyServerForTest(t, "DE")
	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{server},
	}, 0, nil)

	connections := mcu.getSortedConnections(nil)
	require.Len(connections, 1)
	sessionId := connections[0].SessionId()

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client := server.GetSingleClient()
	require.NotNil(client)

	// Force reconnect.
	client.Close()
	assert.NoError(mcu.WaitForDisconnected(ctx))

	// The client will automatically reconnect.
	time.Sleep(10 * time.Millisecond)
	assert.NoError(mcu.WaitForConnections(ctx))

	if connections := mcu.getSortedConnections(nil); assert.Len(connections, 1) {
		assert.Equal(sessionId, connections[0].SessionId())
	}
}

func Test_ProxyResumeFail(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	server := testserver.NewProxyServerForTest(t, "DE")
	mcu, _ := newMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Servers: []testserver.ProxyTestServer{server},
	}, 0, nil)

	connections := mcu.getSortedConnections(nil)
	require.Len(connections, 1)
	sessionId := connections[0].SessionId()

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client := server.GetSingleClient()
	require.NotNil(client)
	server.ClearClients()

	// Force reconnect.
	client.Close()
	assert.NoError(mcu.WaitForDisconnected(ctx))

	// The client will automatically reconnect.
	time.Sleep(10 * time.Millisecond)
	assert.NoError(mcu.WaitForConnections(ctx))

	if connections := mcu.getSortedConnections(nil); assert.Len(connections, 1) {
		assert.NotEqual(sessionId, connections[0].SessionId())
	}
}
