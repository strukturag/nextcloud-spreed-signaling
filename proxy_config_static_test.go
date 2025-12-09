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
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func newProxyConfigStatic(t *testing.T, proxy McuProxy, dns bool, lookup *mockDnsLookup, urls ...string) (ProxyConfig, *DnsMonitor) {
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	if dns {
		cfg.AddOption("mcu", "dnsdiscovery", "true")
	}
	dnsMonitor := newDnsMonitorForTest(t, time.Hour, lookup) // will be updated manually
	logger := log.NewLoggerForTest(t)
	p, err := NewProxyConfigStatic(logger, cfg, proxy, dnsMonitor)
	require.NoError(t, err)
	t.Cleanup(func() {
		p.Stop()
	})
	return p, dnsMonitor
}

func updateProxyConfigStatic(t *testing.T, config ProxyConfig, dns bool, urls ...string) {
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	if dns {
		cfg.AddOption("mcu", "dnsdiscovery", "true")
	}
	require.NoError(t, config.Reload(cfg))
}

func TestProxyConfigStaticSimple(t *testing.T) {
	t.Parallel()
	proxy := newMcuProxyForConfig(t)
	config, _ := newProxyConfigStatic(t, proxy, false, nil, "https://foo/")
	proxy.Expect("add", "https://foo/")
	require.NoError(t, config.Start())

	proxy.Expect("keep", "https://foo/")
	proxy.Expect("add", "https://bar/")
	updateProxyConfigStatic(t, config, false, "https://foo/", "https://bar/")

	proxy.Expect("keep", "https://bar/")
	proxy.Expect("add", "https://baz/")
	proxy.Expect("remove", "https://foo/")
	updateProxyConfigStatic(t, config, false, "https://bar/", "https://baz/")
}

func TestProxyConfigStaticDNS(t *testing.T) {
	t.Parallel()
	lookup := newMockDnsLookupForTest(t)
	proxy := newMcuProxyForConfig(t)
	config, dnsMonitor := newProxyConfigStatic(t, proxy, true, lookup, "https://foo/")
	require.NoError(t, config.Start())

	time.Sleep(time.Millisecond)

	lookup.Set("foo", []net.IP{
		net.ParseIP("192.168.0.1"),
		net.ParseIP("10.1.2.3"),
	})
	proxy.Expect("add", "https://foo/", lookup.Get("foo")...)
	dnsMonitor.checkHostnames()

	lookup.Set("foo", []net.IP{
		net.ParseIP("192.168.0.1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("192.168.1.2"),
	})
	proxy.Expect("keep", "https://foo/", net.ParseIP("192.168.0.1"))
	proxy.Expect("add", "https://foo/", net.ParseIP("192.168.1.1"), net.ParseIP("192.168.1.2"))
	proxy.Expect("remove", "https://foo/", net.ParseIP("10.1.2.3"))
	dnsMonitor.checkHostnames()

	proxy.Expect("add", "https://bar/")
	proxy.Expect("remove", "https://foo/", lookup.Get("foo")...)
	updateProxyConfigStatic(t, config, false, "https://bar/")
}
