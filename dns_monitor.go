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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	entry    atomic.Pointer[dnsMonitorEntry]
	url      string
	callback DnsMonitorCallback
}

func (e *DnsMonitorEntry) URL() string {
	return e.url
}

type dnsMonitorEntry struct {
	hostname string
	hostIP   net.IP

	mu sync.Mutex
	// +checklocks:mu
	ips []net.IP
	// +checklocks:mu
	entries map[*DnsMonitorEntry]bool
}

func (e *dnsMonitorEntry) clearRemoved() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	deleted := false
	for entry := range e.entries {
		if entry.entry.Load() == nil {
			delete(e.entries, entry)
			deleted = true
		}
	}

	return deleted && len(e.entries) == 0
}

func (e *dnsMonitorEntry) setIPs(ips []net.IP, fromIP bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

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
				ips = slices.Delete(ips, idx, idx+1)
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

func (e *dnsMonitorEntry) addEntry(entry *DnsMonitorEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.entries[entry] = true
}

func (e *dnsMonitorEntry) removeEntry(entry *DnsMonitorEntry) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.entries, entry)
	return len(e.entries) == 0
}

// +checklocks:e.mu
func (e *dnsMonitorEntry) runCallbacks(all []net.IP, add []net.IP, keep []net.IP, remove []net.IP) {
	for entry := range e.entries {
		entry.callback(entry, all, add, keep, remove)
	}
}

type DnsMonitor struct {
	interval time.Duration

	stopCtx  context.Context
	stopFunc func()
	stopped  chan struct{}

	mu        sync.RWMutex
	cond      *sync.Cond
	hostnames map[string]*dnsMonitorEntry

	tickerWaiting atomic.Bool
	hasRemoved    atomic.Bool

	// Can be overwritten from tests.
	checkHostnames func()
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
		stopped:  make(chan struct{}),

		hostnames: make(map[string]*dnsMonitorEntry),
	}
	monitor.cond = sync.NewCond(&monitor.mu)
	monitor.checkHostnames = monitor.doCheckHostnames
	return monitor, nil
}

func (m *DnsMonitor) Start() error {
	go m.run()
	return nil
}

func (m *DnsMonitor) Stop() {
	m.stopFunc()
	m.cond.Signal()
	<-m.stopped
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
		// Hostname only passed.
		hostname = target
	}
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = h
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
	e.entry.Store(entry)
	entry.addEntry(e)
	m.cond.Signal()
	return e, nil
}

func (m *DnsMonitor) Remove(entry *DnsMonitorEntry) {
	oldEntry := entry.entry.Swap(nil)
	if oldEntry == nil {
		// Already removed.
		return
	}

	locked := m.mu.TryLock()
	// Spin-lock for simple cases that resolve immediately to avoid deferred removal.
	for i := 0; !locked && i < 1000; i++ {
		time.Sleep(time.Nanosecond)
		locked = m.mu.TryLock()
	}
	if !locked {
		// Currently processing callbacks for this entry, need to defer removal.
		m.hasRemoved.Store(true)
		return
	}
	defer m.mu.Unlock() // +checklocksforce: only executed if the TryLock above succeeded.

	e, found := m.hostnames[oldEntry.hostname]
	if !found {
		return
	}

	if e.removeEntry(entry) {
		delete(m.hostnames, e.hostname)
	}
}

func (m *DnsMonitor) clearRemoved() {
	if !m.hasRemoved.CompareAndSwap(true, false) {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for hostname, entry := range m.hostnames {
		if entry.clearRemoved() {
			delete(m.hostnames, hostname)
		}
	}
}

func (m *DnsMonitor) waitForEntries() (waited bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for len(m.hostnames) == 0 && m.stopCtx.Err() == nil {
		m.cond.Wait()
		waited = true
	}
	return
}

func (m *DnsMonitor) run() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	defer close(m.stopped)

	for {
		if m.waitForEntries() {
			ticker.Reset(m.interval)
			if m.stopCtx.Err() == nil {
				// Initial check when a new entry was added. More checks will be
				// triggered by the Ticker.
				m.checkHostnames()
				continue
			}
		}

		m.tickerWaiting.Store(true)
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.checkHostnames()
		}
	}
}

func (m *DnsMonitor) doCheckHostnames() {
	m.clearRemoved()

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

func (m *DnsMonitor) waitForTicker(ctx context.Context) error {
	for !m.tickerWaiting.Load() {
		time.Sleep(time.Millisecond)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
