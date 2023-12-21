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
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
)

type ipList struct {
	hostname string

	ips []net.IP
}

type proxyConfigStatic struct {
	mu    sync.Mutex
	proxy McuProxy

	dnsDiscovery atomic.Bool
	stopping     chan struct{}
	stopped      chan struct{}

	connectionsMap map[string]*ipList
}

func NewProxyConfigStatic(config *goconf.ConfigFile, proxy McuProxy) (ProxyConfig, error) {
	result := &proxyConfigStatic{
		proxy: proxy,

		stopping: make(chan struct{}, 1),
		stopped:  make(chan struct{}, 1),

		connectionsMap: make(map[string]*ipList),
	}
	if err := result.configure(config, false); err != nil {
		return nil, err
	}
	if len(result.connectionsMap) == 0 {
		return nil, errors.New("No MCU proxy connections configured")
	}
	return result, nil
}

func (p *proxyConfigStatic) configure(config *goconf.ConfigFile, fromReload bool) error {
	dnsDiscovery, _ := config.GetBool("mcu", "dnsdiscovery")
	if p.dnsDiscovery.CompareAndSwap(!dnsDiscovery, dnsDiscovery) && fromReload {
		if !dnsDiscovery {
			p.stopping <- struct{}{}
			<-p.stopped
		} else {
			go p.monitorProxyIPs()
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	remove := make(map[string]*ipList)
	for u, ips := range p.connectionsMap {
		remove[u] = ips
	}

	mcuUrl, _ := config.GetString("mcu", "url")
	for _, u := range strings.Split(mcuUrl, " ") {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}

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

			log.Printf("Could not parse URL %s: %s", u, err)
			continue
		}

		if host, _, err := net.SplitHostPort(parsed.Host); err == nil {
			parsed.Host = host
		}

		var ips []net.IP
		if dnsDiscovery {
			ips, err = lookupProxyIP(parsed.Host)
			if err != nil {
				// Will be retried later.
				log.Printf("Could not lookup %s: %s\n", parsed.Host, err)
				continue
			}
		}

		if fromReload {
			if err := p.proxy.AddConnection(fromReload, u, ips...); err != nil {
				if !fromReload {
					return err
				}

				log.Printf("Could not create proxy connection to %s: %s", u, err)
				continue
			}
		}

		p.connectionsMap[u] = &ipList{
			hostname: parsed.Host,
			ips:      ips,
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

	for u, ipList := range p.connectionsMap {
		if err := p.proxy.AddConnection(false, u, ipList.ips...); err != nil {
			return err
		}
	}

	if p.dnsDiscovery.Load() {
		go p.monitorProxyIPs()
	}
	return nil
}

func (p *proxyConfigStatic) Stop() {
	if p.dnsDiscovery.CompareAndSwap(true, false) {
		p.stopping <- struct{}{}
		<-p.stopped
	}
}

func (p *proxyConfigStatic) Reload(config *goconf.ConfigFile) error {
	return p.configure(config, true)
}

func (p *proxyConfigStatic) monitorProxyIPs() {
	log.Printf("Start monitoring proxy IPs")
	ticker := time.NewTicker(updateDnsInterval)
	for {
		select {
		case <-ticker.C:
			p.updateProxyIPs()
		case <-p.stopping:
			p.stopped <- struct{}{}
			return
		}
	}
}

func (p *proxyConfigStatic) updateProxyIPs() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for u, iplist := range p.connectionsMap {
		if len(iplist.ips) == 0 {
			continue
		}

		if net.ParseIP(iplist.hostname) != nil {
			// No need to lookup endpoints that connect to IP addresses.
			continue
		}

		ips, err := lookupProxyIP(iplist.hostname)
		if err != nil {
			log.Printf("Could not lookup %s: %s", iplist.hostname, err)
			continue
		}

		var newIPs []net.IP
		var removedIPs []net.IP
		for _, oldIP := range iplist.ips {
			found := false
			for idx, newIP := range ips {
				if oldIP.Equal(newIP) {
					ips = append(ips[:idx], ips[idx+1:]...)
					found = true
					p.proxy.KeepConnection(u, oldIP)
					newIPs = append(newIPs, oldIP)
					break
				}
			}

			if !found {
				removedIPs = append(removedIPs, oldIP)
			}
		}

		if len(ips) > 0 {
			newIPs = append(newIPs, ips...)
			if err := p.proxy.AddConnection(true, u, ips...); err != nil {
				log.Printf("Could not add proxy connection to %s with %+v: %s", u, ips, err)
			}
		}
		iplist.ips = newIPs

		if len(removedIPs) > 0 {
			p.proxy.RemoveConnection(u, removedIPs...)
		}
	}
}
