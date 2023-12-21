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

	"github.com/dlintw/goconf"
)

func newProxyConfigStatic(t *testing.T, proxy McuProxy, dns bool, urls ...string) ProxyConfig {
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	if dns {
		cfg.AddOption("mcu", "dnsdiscovery", "true")
	}
	p, err := NewProxyConfigStatic(cfg, proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Stop()
	})
	return p
}

func updateProxyConfigStatic(t *testing.T, config ProxyConfig, dns bool, urls ...string) {
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	if dns {
		cfg.AddOption("mcu", "dnsdiscovery", "true")
	}
	if err := config.Reload(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestProxyConfigStaticSimple(t *testing.T) {
	proxy := newMcuProxyForConfig(t)
	config := newProxyConfigStatic(t, proxy, false, "https://foo/")
	proxy.Expect("add", "https://foo/")
	if err := config.Start(); err != nil {
		t.Fatal(err)
	}

	proxy.Expect("keep", "https://foo/")
	proxy.Expect("add", "https://bar/")
	updateProxyConfigStatic(t, config, false, "https://foo/", "https://bar/")

	proxy.Expect("keep", "https://bar/")
	proxy.Expect("add", "https://baz/")
	proxy.Expect("remove", "https://foo/")
	updateProxyConfigStatic(t, config, false, "https://bar/", "https://baz/")
}

func TestProxyConfigStaticDNS(t *testing.T) {
	old := lookupProxyIP
	t.Cleanup(func() {
		lookupProxyIP = old
	})
	proxyIPs := make(map[string][]net.IP)
	lookupProxyIP = func(hostname string) ([]net.IP, error) {
		ips := append([]net.IP{}, proxyIPs[hostname]...)
		return ips, nil
	}
	proxyIPs["foo"] = []net.IP{
		net.ParseIP("192.168.0.1"),
		net.ParseIP("10.1.2.3"),
	}

	proxy := newMcuProxyForConfig(t)
	config := newProxyConfigStatic(t, proxy, true, "https://foo/").(*proxyConfigStatic)
	proxy.Expect("add", "https://foo/", proxyIPs["foo"]...)
	if err := config.Start(); err != nil {
		t.Fatal(err)
	}

	proxyIPs["foo"] = []net.IP{
		net.ParseIP("192.168.0.1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("192.168.1.2"),
	}
	proxy.Expect("keep", "https://foo/", net.ParseIP("192.168.0.1"))
	proxy.Expect("add", "https://foo/", net.ParseIP("192.168.1.1"), net.ParseIP("192.168.1.2"))
	proxy.Expect("remove", "https://foo/", net.ParseIP("10.1.2.3"))
	config.updateProxyIPs()

	proxy.Expect("add", "https://bar/")
	proxy.Expect("remove", "https://foo/", proxyIPs["foo"]...)
	updateProxyConfigStatic(t, config, false, "https://bar/")
}
