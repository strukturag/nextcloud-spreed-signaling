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
	"fmt"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDnsLookup struct {
	sync.RWMutex

	ips map[string][]net.IP
}

func newMockDnsLookupForTest(t *testing.T) *mockDnsLookup {
	mock := &mockDnsLookup{
		ips: make(map[string][]net.IP),
	}
	prev := lookupDnsMonitorIP
	t.Cleanup(func() {
		lookupDnsMonitorIP = prev
	})
	lookupDnsMonitorIP = mock.lookup
	return mock
}

func (m *mockDnsLookup) Set(host string, ips []net.IP) {
	m.Lock()
	defer m.Unlock()

	m.ips[host] = ips
}

func (m *mockDnsLookup) Get(host string) []net.IP {
	m.Lock()
	defer m.Unlock()

	return m.ips[host]
}

func (m *mockDnsLookup) lookup(host string) ([]net.IP, error) {
	m.RLock()
	defer m.RUnlock()

	ips, found := m.ips[host]
	if !found {
		return nil, &net.DNSError{
			Err:        fmt.Sprintf("could not resolve %s", host),
			Name:       host,
			IsNotFound: true,
		}
	}

	return append([]net.IP{}, ips...), nil
}

func newDnsMonitorForTest(t *testing.T, interval time.Duration) *DnsMonitor {
	t.Helper()
	require := require.New(t)
	log := GetLoggerForTest(t)

	monitor, err := NewDnsMonitor(log, interval)
	require.NoError(err)

	t.Cleanup(func() {
		monitor.Stop()
	})

	require.NoError(monitor.Start())
	return monitor
}

type dnsMonitorReceiverRecord struct {
	all    []net.IP
	add    []net.IP
	keep   []net.IP
	remove []net.IP
}

func (r *dnsMonitorReceiverRecord) Equal(other *dnsMonitorReceiverRecord) bool {
	return r == other || (reflect.DeepEqual(r.add, other.add) &&
		reflect.DeepEqual(r.keep, other.keep) &&
		reflect.DeepEqual(r.remove, other.remove))
}

func (r *dnsMonitorReceiverRecord) String() string {
	return fmt.Sprintf("all=%v, add=%v, keep=%v, remove=%v", r.all, r.add, r.keep, r.remove)
}

var (
	expectNone = &dnsMonitorReceiverRecord{}
)

type dnsMonitorReceiver struct {
	sync.Mutex

	t        *testing.T
	expected *dnsMonitorReceiverRecord
	received *dnsMonitorReceiverRecord
}

func newDnsMonitorReceiverForTest(t *testing.T) *dnsMonitorReceiver {
	return &dnsMonitorReceiver{
		t: t,
	}
}

func (r *dnsMonitorReceiver) OnLookup(entry *DnsMonitorEntry, all, add, keep, remove []net.IP) {
	r.Lock()
	defer r.Unlock()

	received := &dnsMonitorReceiverRecord{
		all:    all,
		add:    add,
		keep:   keep,
		remove: remove,
	}

	expected := r.expected
	r.expected = nil
	if expected == expectNone {
		assert.Fail(r.t, "expected no event, got %v", received)
		return
	}

	if expected == nil {
		if r.received != nil && !r.received.Equal(received) {
			assert.Fail(r.t, "already received %v, got %v", r.received, received)
		}
		return
	}

	assert.True(r.t, expected.Equal(received), "expected %v, got %v", expected, received)
	r.received = nil
	r.expected = nil
}

func (r *dnsMonitorReceiver) WaitForExpected(ctx context.Context) {
	r.t.Helper()
	r.Lock()
	defer r.Unlock()

	ticker := time.NewTicker(time.Microsecond)
	abort := false
	for r.expected != nil && !abort {
		r.Unlock()
		select {
		case <-ticker.C:
		case <-ctx.Done():
			assert.NoError(r.t, ctx.Err())
			abort = true
		}
		r.Lock()
	}
}

func (r *dnsMonitorReceiver) Expect(all, add, keep, remove []net.IP) {
	r.t.Helper()
	r.Lock()
	defer r.Unlock()

	if r.expected != nil && r.expected != expectNone {
		assert.Fail(r.t, "didn't get previously expected %v", r.expected)
	}

	expected := &dnsMonitorReceiverRecord{
		all:    all,
		add:    add,
		keep:   keep,
		remove: remove,
	}
	if r.received != nil && r.received.Equal(expected) {
		r.received = nil
		return
	}

	r.expected = expected
}

func (r *dnsMonitorReceiver) ExpectNone() {
	r.t.Helper()
	r.Lock()
	defer r.Unlock()

	if r.expected != nil && r.expected != expectNone {
		assert.Fail(r.t, "didn't get previously expected %v", r.expected)
	}

	r.expected = expectNone
}

func TestDnsMonitor(t *testing.T) {
	lookup := newMockDnsLookupForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	interval := time.Millisecond
	monitor := newDnsMonitorForTest(t, interval)

	ip1 := net.ParseIP("192.168.0.1")
	ip2 := net.ParseIP("192.168.1.1")
	ip3 := net.ParseIP("10.1.2.3")
	ips1 := []net.IP{
		ip1,
		ip2,
	}
	lookup.Set("foo", ips1)

	rec1 := newDnsMonitorReceiverForTest(t)
	rec1.Expect(ips1, ips1, nil, nil)

	entry1, err := monitor.Add("https://foo:12345", rec1.OnLookup)
	require.NoError(t, err)
	defer monitor.Remove(entry1)

	rec1.WaitForExpected(ctx)

	ips2 := []net.IP{
		ip1,
		ip2,
		ip3,
	}
	add2 := []net.IP{ip3}
	keep2 := []net.IP{ip1, ip2}
	rec1.Expect(ips2, add2, keep2, nil)
	lookup.Set("foo", ips2)
	rec1.WaitForExpected(ctx)

	ips3 := []net.IP{
		ip2,
		ip3,
	}
	keep3 := []net.IP{ip2, ip3}
	remove3 := []net.IP{ip1}
	rec1.Expect(ips3, nil, keep3, remove3)
	lookup.Set("foo", ips3)
	rec1.WaitForExpected(ctx)

	rec1.ExpectNone()
	time.Sleep(5 * interval)

	remove4 := []net.IP{ip2, ip3}
	rec1.Expect(nil, nil, nil, remove4)
	lookup.Set("foo", nil)
	rec1.WaitForExpected(ctx)

	rec1.ExpectNone()
	time.Sleep(5 * interval)

	// Removing multiple times is supported.
	monitor.Remove(entry1)
	monitor.Remove(entry1)

	// No more events after removing.
	lookup.Set("foo", ips1)
	rec1.ExpectNone()
	time.Sleep(5 * interval)
}

func TestDnsMonitorIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	interval := time.Millisecond
	monitor := newDnsMonitorForTest(t, interval)

	ip := "192.168.0.1"
	ips := []net.IP{
		net.ParseIP(ip),
	}

	rec1 := newDnsMonitorReceiverForTest(t)
	rec1.Expect(ips, ips, nil, nil)

	entry, err := monitor.Add(ip+":12345", rec1.OnLookup)
	require.NoError(t, err)
	defer monitor.Remove(entry)

	rec1.WaitForExpected(ctx)

	rec1.ExpectNone()
	time.Sleep(5 * interval)
}

func TestDnsMonitorNoLookupIfEmpty(t *testing.T) {
	interval := time.Millisecond
	monitor := newDnsMonitorForTest(t, interval)

	var checked atomic.Bool
	monitor.checkHostnames = func() {
		checked.Store(true)
		monitor.doCheckHostnames()
	}

	time.Sleep(10 * interval)
	assert.False(t, checked.Load(), "should not have checked hostnames")
}

type deadlockMonitorReceiver struct {
	t       *testing.T
	monitor *DnsMonitor

	mu sync.RWMutex
	wg sync.WaitGroup

	entry     *DnsMonitorEntry
	started   chan struct{}
	triggered bool
	closed    atomic.Bool
}

func newDeadlockMonitorReceiver(t *testing.T, monitor *DnsMonitor) *deadlockMonitorReceiver {
	return &deadlockMonitorReceiver{
		t:       t,
		monitor: monitor,
		started: make(chan struct{}),
	}
}

func (r *deadlockMonitorReceiver) OnLookup(entry *DnsMonitorEntry, all []net.IP, add []net.IP, keep []net.IP, remove []net.IP) {
	if !assert.False(r.t, r.closed.Load(), "received lookup after closed") {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.triggered {
		return
	}

	r.triggered = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		r.mu.RLock()
		defer r.mu.RUnlock()

		close(r.started)
		time.Sleep(50 * time.Millisecond)
	}()
}

func (r *deadlockMonitorReceiver) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, err := r.monitor.Add("foo", r.OnLookup)
	if !assert.NoError(r.t, err) {
		return
	}

	r.entry = entry
}

func (r *deadlockMonitorReceiver) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.entry != nil {
		r.monitor.Remove(r.entry)
		r.closed.Store(true)
	}
	r.wg.Wait()
}

func TestDnsMonitorDeadlock(t *testing.T) {
	lookup := newMockDnsLookupForTest(t)
	ip1 := net.ParseIP("192.168.0.1")
	ip2 := net.ParseIP("192.168.0.2")
	lookup.Set("foo", []net.IP{ip1})

	interval := time.Millisecond
	monitor := newDnsMonitorForTest(t, interval)

	r := newDeadlockMonitorReceiver(t, monitor)
	r.Start()
	<-r.started
	lookup.Set("foo", []net.IP{ip2})
	r.Close()
	lookup.Set("foo", []net.IP{ip1})
	time.Sleep(10 * interval)
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	assert.Empty(t, monitor.hostnames)
}
