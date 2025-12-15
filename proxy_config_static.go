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
	"errors"
	"maps"
	"net"
	"net/url"
	"sync"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/dns"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type ipList struct {
	hostname string

	entry *dns.MonitorEntry
	ips   []net.IP
}

type proxyConfigStatic struct {
	logger log.Logger
	mu     sync.Mutex
	proxy  McuProxy

	dnsMonitor *dns.Monitor
	// +checklocks:mu
	dnsDiscovery bool

	// +checklocks:mu
	connectionsMap map[string]*ipList
}

func NewProxyConfigStatic(logger log.Logger, config *goconf.ConfigFile, proxy McuProxy, dnsMonitor *dns.Monitor) (ProxyConfig, error) {
	result := &proxyConfigStatic{
		logger:         logger,
		proxy:          proxy,
		dnsMonitor:     dnsMonitor,
		connectionsMap: make(map[string]*ipList),
	}
	if err := result.configure(config, false); err != nil {
		return nil, err
	}
	if len(result.connectionsMap) == 0 {
		return nil, errors.New("no MCU proxy connections configured")
	}
	return result, nil
}

func (p *proxyConfigStatic) configure(cfg *goconf.ConfigFile, fromReload bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	dnsDiscovery, _ := cfg.GetBool("mcu", "dnsdiscovery")
	if dnsDiscovery != p.dnsDiscovery {
		if !dnsDiscovery {
			for _, ips := range p.connectionsMap {
				if ips.entry != nil {
					p.dnsMonitor.Remove(ips.entry)
					ips.entry = nil
				}
			}
		}
		p.dnsDiscovery = dnsDiscovery
	}

	remove := maps.Clone(p.connectionsMap)

	mcuUrl, _ := config.GetStringOptionWithEnv(cfg, "mcu", "url")
	for u := range internal.SplitEntries(mcuUrl, " ") {
		if existing, found := remove[u]; found {
			// Proxy connection still exists in new configuration
			delete(remove, u)
			p.proxy.KeepConnection(u, existing.ips...)
			continue
		}

		parsed, err := url.Parse(u)
		if err != nil {
			if !fromReload {
				return err
			}

			p.logger.Printf("Could not parse URL %s: %s", u, err)
			continue
		}

		if host, _, err := net.SplitHostPort(parsed.Host); err == nil {
			parsed.Host = host
		}

		if dnsDiscovery {
			p.connectionsMap[u] = &ipList{ // +checklocksignore: Not supported for iter loops yet, see https://github.com/google/gvisor/issues/12176
				hostname: parsed.Host,
			}
			continue
		}

		if fromReload {
			if err := p.proxy.AddConnection(fromReload, u); err != nil {
				if !fromReload {
					return err
				}

				p.logger.Printf("Could not create proxy connection to %s: %s", u, err)
				continue
			}
		}

		p.connectionsMap[u] = &ipList{ // +checklocksignore: Not supported for iter loops yet, see https://github.com/google/gvisor/issues/12176
			hostname: parsed.Host,
		}
	}

	for u, entry := range remove {
		p.proxy.RemoveConnection(u, entry.ips...)
		delete(p.connectionsMap, u)
	}

	return nil
}

func (p *proxyConfigStatic) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dnsDiscovery {
		for u, ips := range p.connectionsMap {
			if ips.entry != nil {
				continue
			}

			entry, err := p.dnsMonitor.Add(u, p.onLookup)
			if err != nil {
				return err
			}

			ips.entry = entry
		}
	} else {
		for u, ipList := range p.connectionsMap {
			if err := p.proxy.AddConnection(false, u, ipList.ips...); err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *proxyConfigStatic) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dnsDiscovery {
		for _, ips := range p.connectionsMap {
			if ips.entry == nil {
				continue
			}

			p.dnsMonitor.Remove(ips.entry)
			ips.entry = nil
		}
	}
}

func (p *proxyConfigStatic) Reload(config *goconf.ConfigFile) error {
	return p.configure(config, true)
}

func (p *proxyConfigStatic) onLookup(entry *dns.MonitorEntry, all []net.IP, added []net.IP, keep []net.IP, removed []net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()

	u := entry.URL()
	for _, ip := range keep {
		p.proxy.KeepConnection(u, ip)
	}

	if len(added) > 0 {
		if err := p.proxy.AddConnection(true, u, added...); err != nil {
			p.logger.Printf("Could not add proxy connection to %s with %+v: %s", u, added, err)
		}
	}

	if len(removed) > 0 {
		p.proxy.RemoveConnection(u, removed...)
	}

	if ipList, found := p.connectionsMap[u]; found {
		ipList.ips = all
	}
}
