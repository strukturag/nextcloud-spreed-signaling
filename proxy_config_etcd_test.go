/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
	"encoding/json"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"
)

type TestProxyInformationEtcd struct {
	Address string `json:"address"`

	OtherData string `json:"otherdata,omitempty"`
}

func newProxyConfigEtcd(t *testing.T, proxy McuProxy) (*embed.Etcd, ProxyConfig) {
	t.Helper()
	log := GetLoggerForTest(t)
	etcd, client := NewEtcdClientForTest(t)
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "keyprefix", "proxies/")
	p, err := NewProxyConfigEtcd(log, cfg, client, proxy)
	require.NoError(t, err)
	t.Cleanup(func() {
		p.Stop()
	})
	return etcd, p
}

func SetEtcdProxy(t *testing.T, etcd *embed.Etcd, path string, proxy *TestProxyInformationEtcd) {
	t.Helper()
	data, err := json.Marshal(proxy)
	require.NoError(t, err)
	SetEtcdValue(etcd, path, data)
}

func TestProxyConfigEtcd(t *testing.T) {
	t.Parallel()
	proxy := newMcuProxyForConfig(t)
	etcd, config := newProxyConfigEtcd(t, proxy)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	SetEtcdProxy(t, etcd, "proxies/a", &TestProxyInformationEtcd{
		Address: "https://foo/",
	})
	proxy.Expect("add", "https://foo/")
	require.NoError(t, config.Start())
	proxy.WaitForEvents(ctx)

	proxy.Expect("add", "https://bar/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address: "https://bar/",
	})
	proxy.WaitForEvents(ctx)

	proxy.Expect("keep", "https://bar/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address:   "https://bar/",
		OtherData: "ignore-me",
	})
	proxy.WaitForEvents(ctx)

	proxy.Expect("remove", "https://foo/")
	DeleteEtcdValue(etcd, "proxies/a")
	proxy.WaitForEvents(ctx)

	proxy.Expect("remove", "https://bar/")
	proxy.Expect("add", "https://baz/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address: "https://baz/",
	})
	proxy.WaitForEvents(ctx)

	// Adding the same hostname multiple times should not trigger an event.
	SetEtcdProxy(t, etcd, "proxies/c", &TestProxyInformationEtcd{
		Address: "https://baz/",
	})
	time.Sleep(100 * time.Millisecond)
}
