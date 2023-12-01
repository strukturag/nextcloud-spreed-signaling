package signaling

import (
	"context"
	"net"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
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
	t        *testing.T
	expected []proxyConfigEvent
	mu       sync.Mutex
	waiters  []chan struct{}
}

func newMcuProxyForConfig(t *testing.T) *mcuProxyForConfig {
	proxy := &mcuProxyForConfig{
		t: t,
	}
	t.Cleanup(func() {
		if len(proxy.expected) > 0 {
			t.Errorf("expected events %+v were not triggered", proxy.expected)
		}
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

func (p *mcuProxyForConfig) WaitForEvents(ctx context.Context) {
	p.t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.expected) == 0 {
		return
	}

	waiter := make(chan struct{})
	p.waiters = append(p.waiters, waiter)
	p.mu.Unlock()
	defer p.mu.Lock()
	select {
	case <-ctx.Done():
		p.t.Error(ctx.Err())
	case <-waiter:
	}
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

	if len(p.expected) == 0 {
		p.t.Errorf("no event expected, got %+v from %s:%d", event, caller.File, caller.Line)
		return
	}

	defer func() {
		if len(p.expected) == 0 {
			p.mu.Lock()
			waiters := p.waiters
			p.waiters = nil
			p.mu.Unlock()

			for _, ch := range waiters {
				ch <- struct{}{}
			}
		}
	}()

	p.mu.Lock()
	defer p.mu.Unlock()
	expected := p.expected[0]
	p.expected = p.expected[1:]
	if !reflect.DeepEqual(expected, *event) {
		p.t.Errorf("expected %+v, got %+v from %s:%d", expected, event, caller.File, caller.Line)
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
