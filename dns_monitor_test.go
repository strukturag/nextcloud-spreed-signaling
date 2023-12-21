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
	"testing"
	"time"
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

	monitor, err := NewDnsMonitor(interval)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		monitor.Stop()
	})

	if err := monitor.Start(); err != nil {
		t.Fatal(err)
	}

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
		r.t.Errorf("expected no event, got %v", received)
		return
	}

	if expected == nil {
		if r.received != nil && !r.received.Equal(received) {
			r.t.Errorf("already received %v, got %v", r.received, received)
		}
		return
	}

	if !expected.Equal(received) {
		r.t.Errorf("expected %v, got %v", expected, received)
	}
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
			r.t.Error(ctx.Err())
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
		r.t.Errorf("didn't get previously expected %v", r.expected)
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
		r.t.Errorf("didn't get previously expected %v", r.expected)
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

	entry1, err := monitor.Add("https://foo", rec1.OnLookup)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Remove(entry1)

	rec1.WaitForExpected(ctx)

	ips2 := []net.IP{
		ip1,
		ip2,
		ip3,
	}
	add2 := []net.IP{ip3}
	keep2 := []net.IP{ip1, ip2}
	lookup.Set("foo", ips2)
	rec1.Expect(ips2, add2, keep2, nil)
	rec1.WaitForExpected(ctx)

	ips3 := []net.IP{
		ip2,
		ip3,
	}
	lookup.Set("foo", ips3)
	keep3 := []net.IP{ip2, ip3}
	remove3 := []net.IP{ip1}
	rec1.Expect(ips3, nil, keep3, remove3)
	rec1.WaitForExpected(ctx)

	rec1.ExpectNone()
	time.Sleep(5 * interval)

	lookup.Set("foo", nil)
	remove4 := []net.IP{ip2, ip3}
	rec1.Expect(nil, nil, nil, remove4)
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

	entry, err := monitor.Add("https://"+ip, rec1.OnLookup)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Remove(entry)

	rec1.WaitForExpected(ctx)

	rec1.ExpectNone()
	time.Sleep(5 * interval)
}
