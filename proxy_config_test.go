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
	"net"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	thisFilename string
)

func init() {
	pc := make([]uintptr, 1)
	count := runtime.Callers(1, pc)
	frames := runtime.CallersFrames(pc[:count])
	frame, _ := frames.Next()
	thisFilename = frame.File
}

type proxyConfigEvent struct {
	action string
	url    string
	ips    []net.IP
}

type mcuProxyForConfig struct {
	t  *testing.T
	mu sync.Mutex
	// +checklocks:mu
	expected []proxyConfigEvent
	// +checklocks:mu
	waiters []chan struct{}
}

func newMcuProxyForConfig(t *testing.T) *mcuProxyForConfig {
	proxy := &mcuProxyForConfig{
		t: t,
	}
	t.Cleanup(func() {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		assert.Empty(t, proxy.expected)
	})
	return proxy
}

func (p *mcuProxyForConfig) Expect(action string, url string, ips ...net.IP) {
	if len(ips) == 0 {
		ips = nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.expected = append(p.expected, proxyConfigEvent{
		action: action,
		url:    url,
		ips:    ips,
	})
}

func (p *mcuProxyForConfig) addWaiter() chan struct{} {
	p.t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.expected) == 0 {
		return nil
	}

	waiter := make(chan struct{})
	p.waiters = append(p.waiters, waiter)
	return waiter
}

func (p *mcuProxyForConfig) WaitForEvents(ctx context.Context) {
	p.t.Helper()

	waiter := p.addWaiter()
	if waiter == nil {
		return
	}

	select {
	case <-ctx.Done():
		assert.NoError(p.t, ctx.Err())
	case <-waiter:
	}
}

func (p *mcuProxyForConfig) getWaitersIfEmpty() []chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.expected) != 0 {
		return nil
	}

	waiters := p.waiters
	p.waiters = nil
	return waiters
}

func (p *mcuProxyForConfig) getExpectedEvent() *proxyConfigEvent {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.expected) == 0 {
		return nil
	}

	expected := p.expected[0]
	p.expected = p.expected[1:]
	return &expected
}

func (p *mcuProxyForConfig) checkEvent(event *proxyConfigEvent) {
	p.t.Helper()
	pc := make([]uintptr, 32)
	count := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:count])
	var caller runtime.Frame
	for {
		frame, more := frames.Next()
		if frame.File != thisFilename && strings.HasSuffix(frame.File, "_test.go") {
			caller = frame
			break
		}
		if !more {
			break
		}
	}

	expected := p.getExpectedEvent()
	if expected == nil {
		assert.Fail(p.t, "no event expected", "received %+v from %s:%d", event, caller.File, caller.Line)
		return
	}

	if !reflect.DeepEqual(expected, event) {
		assert.Fail(p.t, "wrong event", "expected %+v, received %+v from %s:%d", expected, event, caller.File, caller.Line)
	}

	waiters := p.getWaitersIfEmpty()
	if len(waiters) == 0 {
		return
	}

	for _, ch := range waiters {
		ch <- struct{}{}
	}
}

func (p *mcuProxyForConfig) AddConnection(ignoreErrors bool, url string, ips ...net.IP) error {
	p.t.Helper()
	if len(ips) == 0 {
		ips = nil
	}
	p.checkEvent(&proxyConfigEvent{
		action: "add",
		url:    url,
		ips:    ips,
	})
	return nil
}

func (p *mcuProxyForConfig) KeepConnection(url string, ips ...net.IP) {
	p.t.Helper()
	if len(ips) == 0 {
		ips = nil
	}
	p.checkEvent(&proxyConfigEvent{
		action: "keep",
		url:    url,
		ips:    ips,
	})
}

func (p *mcuProxyForConfig) RemoveConnection(url string, ips ...net.IP) {
	p.t.Helper()
	if len(ips) == 0 {
		ips = nil
	}
	p.checkEvent(&proxyConfigEvent{
		action: "remove",
		url:    url,
		ips:    ips,
	})
}
