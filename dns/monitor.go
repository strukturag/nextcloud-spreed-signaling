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
package dns

import (
	"context"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	defaultMonitorInterval = time.Second
)

type MonitorCallback = func(entry *MonitorEntry, all []net.IP, add []net.IP, keep []net.IP, remove []net.IP)

type MonitorEntry struct {
	entry    atomic.Pointer[monitorEntry]
	url      string
	callback MonitorCallback
}

func (e *MonitorEntry) URL() string {
	return e.url
}

type monitorEntry struct {
	hostname string
	hostIP   net.IP

	mu sync.Mutex
	// +checklocks:mu
	ips []net.IP
	// +checklocks:mu
	entries map[*MonitorEntry]bool
}

func (e *monitorEntry) clearRemoved() bool {
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

func (e *monitorEntry) setIPs(ips []net.IP, fromIP bool) {
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

func (e *monitorEntry) addEntry(entry *MonitorEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.entries[entry] = true
}

func (e *monitorEntry) removeEntry(entry *MonitorEntry) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.entries, entry)
	return len(e.entries) == 0
}

// +checklocks:e.mu
func (e *monitorEntry) runCallbacks(all []net.IP, add []net.IP, keep []net.IP, remove []net.IP) {
	for entry := range e.entries {
		entry.callback(entry, all, add, keep, remove)
	}
}

type MonitorLookupFunc func(hostname string) ([]net.IP, error)

type Monitor struct {
	logger     log.Logger
	interval   time.Duration
	lookupFunc MonitorLookupFunc

	stopCtx  context.Context
	stopFunc func()
	stopped  chan struct{}

	mu        sync.RWMutex
	cond      *sync.Cond
	hostnames map[string]*monitorEntry

	tickerWaiting atomic.Bool
	hasRemoved    atomic.Bool
}

func NewMonitor(logger log.Logger, interval time.Duration, lookupFunc MonitorLookupFunc) (*Monitor, error) {
	if interval < 0 {
		interval = defaultMonitorInterval
	}
	if lookupFunc == nil {
		lookupFunc = net.LookupIP
	}

	stopCtx, stopFunc := context.WithCancel(context.Background())
	monitor := &Monitor{
		logger:     logger,
		interval:   interval,
		lookupFunc: lookupFunc,

		stopCtx:  stopCtx,
		stopFunc: stopFunc,
		stopped:  make(chan struct{}),

		hostnames: make(map[string]*monitorEntry),
	}
	monitor.cond = sync.NewCond(&monitor.mu)
	return monitor, nil
}

func (m *Monitor) Start() error {
	go m.run()
	return nil
}

func (m *Monitor) Stop() {
	m.stopFunc()
	m.cond.Signal()
	<-m.stopped
}

func (m *Monitor) Add(target string, callback MonitorCallback) (*MonitorEntry, error) {
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

	e := &MonitorEntry{
		url:      target,
		callback: callback,
	}

	entry, found := m.hostnames[hostname]
	if !found {
		entry = &monitorEntry{
			hostname: hostname,
			hostIP:   net.ParseIP(hostname),
			entries:  make(map[*MonitorEntry]bool),
		}
		m.hostnames[hostname] = entry
	}
	e.entry.Store(entry)
	entry.addEntry(e)
	m.cond.Signal()
	return e, nil
}

func (m *Monitor) Remove(entry *MonitorEntry) {
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

func (m *Monitor) clearRemoved() {
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

func (m *Monitor) waitForEntries() (waited bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for len(m.hostnames) == 0 && m.stopCtx.Err() == nil {
		m.cond.Wait()
		waited = true
	}
	return
}

func (m *Monitor) run() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	defer close(m.stopped)

	for {
		if m.waitForEntries() {
			ticker.Reset(m.interval)
			if m.stopCtx.Err() == nil {
				// Initial check when a new entry was added. More checks will be
				// triggered by the Ticker.
				m.CheckHostnames()
				continue
			}
		}

		m.tickerWaiting.Store(true)
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.CheckHostnames()
		}
	}
}

func (m *Monitor) CheckHostnames() {
	m.clearRemoved()

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.hostnames {
		m.checkHostname(entry)
	}
}

func (m *Monitor) checkHostname(entry *monitorEntry) {
	if len(entry.hostIP) > 0 {
		entry.setIPs([]net.IP{entry.hostIP}, true)
		return
	}

	ips, err := m.lookupFunc(entry.hostname)
	if err != nil {
		m.logger.Printf("Could not lookup %s: %s", entry.hostname, err)
		return
	}

	entry.setIPs(ips, false)
}

func (m *Monitor) WaitForTicker(ctx context.Context) error {
	for !m.tickerWaiting.Load() {
		time.Sleep(time.Millisecond)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
