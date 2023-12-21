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
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	lookupDnsMonitorIP = net.LookupIP
)

const (
	defaultDnsMonitorInterval = time.Second
)

type DnsMonitorCallback = func(entry *DnsMonitorEntry, all []net.IP, add []net.IP, keep []net.IP, remove []net.IP)

type DnsMonitorEntry struct {
	entry    *dnsMonitorEntry
	url      string
	callback DnsMonitorCallback
}

func (e *DnsMonitorEntry) URL() string {
	return e.url
}

type dnsMonitorEntry struct {
	hostname string
	hostIP   net.IP

	ips     []net.IP
	entries map[*DnsMonitorEntry]bool
}

func (e *dnsMonitorEntry) setIPs(ips []net.IP, fromIP bool) {
	empty := len(e.ips) == 0
	if empty {
		// Simple case: initial lookup.
		if len(ips) > 0 {
			e.ips = ips
			e.runCallbacks(ips, ips, nil, nil)
		}
		return
	} else if fromIP {
		// No more updates possible for IP addresses.
		return
	} else if len(ips) == 0 {
		// Simple case: no records received from lookup.
		if !empty {
			removed := e.ips
			e.ips = nil
			e.runCallbacks(nil, nil, nil, removed)
		}
		return
	}

	var newIPs []net.IP
	var addedIPs []net.IP
	var removedIPs []net.IP
	var keepIPs []net.IP
	for _, oldIP := range e.ips {
		found := false
		for idx, newIP := range ips {
			if oldIP.Equal(newIP) {
				ips = append(ips[:idx], ips[idx+1:]...)
				found = true
				keepIPs = append(keepIPs, oldIP)
				newIPs = append(newIPs, oldIP)
				break
			}
		}

		if !found {
			removedIPs = append(removedIPs, oldIP)
		}
	}

	if len(ips) > 0 {
		addedIPs = append(addedIPs, ips...)
		newIPs = append(newIPs, ips...)
	}
	e.ips = newIPs

	if len(addedIPs) > 0 || len(removedIPs) > 0 {
		e.runCallbacks(newIPs, addedIPs, keepIPs, removedIPs)
	}
}

func (e *dnsMonitorEntry) runCallbacks(all []net.IP, add []net.IP, keep []net.IP, remove []net.IP) {
	for entry := range e.entries {
		entry.callback(entry, all, add, keep, remove)
	}
}

type DnsMonitor struct {
	interval time.Duration

	stopCtx  context.Context
	stopFunc func()

	mu        sync.RWMutex
	hostnames map[string]*dnsMonitorEntry
}

func NewDnsMonitor(interval time.Duration) (*DnsMonitor, error) {
	if interval < 0 {
		interval = defaultDnsMonitorInterval
	}

	stopCtx, stopFunc := context.WithCancel(context.Background())
	monitor := &DnsMonitor{
		interval: interval,

		stopCtx:  stopCtx,
		stopFunc: stopFunc,

		hostnames: make(map[string]*dnsMonitorEntry),
	}
	return monitor, nil
}

func (m *DnsMonitor) Start() error {
	go m.run()
	return nil
}

func (m *DnsMonitor) Stop() {
	m.stopFunc()
}

func (m *DnsMonitor) Add(target string, callback DnsMonitorCallback) (*DnsMonitorEntry, error) {
	var hostname string
	if strings.Contains(target, "://") {
		// Full URL passed.
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		hostname = parsed.Host
	} else {
		// Hostname with optional port passed.
		hostname = target
		if h, _, err := net.SplitHostPort(target); err == nil {
			hostname = h
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := &DnsMonitorEntry{
		url:      target,
		callback: callback,
	}

	entry, found := m.hostnames[hostname]
	if !found {
		entry = &dnsMonitorEntry{
			hostname: hostname,
			hostIP:   net.ParseIP(hostname),
			entries:  make(map[*DnsMonitorEntry]bool),
		}
		m.hostnames[hostname] = entry
	}
	e.entry = entry
	entry.entries[e] = true
	return e, nil
}

func (m *DnsMonitor) Remove(entry *DnsMonitorEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.entry == nil {
		return
	}

	e, found := m.hostnames[entry.entry.hostname]
	if !found {
		return
	}

	entry.entry = nil
	delete(e.entries, entry)
}

func (m *DnsMonitor) run() {
	ticker := time.NewTicker(m.interval)
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.checkHostnames()
		}
	}
}

func (m *DnsMonitor) checkHostnames() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.hostnames {
		m.checkHostname(entry)
	}
}

func (m *DnsMonitor) checkHostname(entry *dnsMonitorEntry) {
	if len(entry.hostIP) > 0 {
		entry.setIPs([]net.IP{entry.hostIP}, true)
		return
	}

	ips, err := lookupDnsMonitorIP(entry.hostname)
	if err != nil {
		log.Printf("Could not lookup %s: %s", entry.hostname, err)
		return
	}

	entry.setIPs(ips, false)
}
