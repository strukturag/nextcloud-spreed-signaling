package signaling

import (
	"net"
	"testing"
)

func TestAllowedIps(t *testing.T) {
	a, err := ParseAllowedIps("127.0.0.1, 192.168.0.1, 192.168.1.1/24")
	if err != nil {
		t.Fatal(err)
	}
	if a.Empty() {
		t.Fatal("should not be empty")
	}

	allowed := []string{
		"127.0.0.1",
		"192.168.0.1",
		"192.168.1.1",
		"192.168.1.100",
	}
	notAllowed := []string{
		"192.168.0.2",
		"10.1.2.3",
	}

	for _, addr := range allowed {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Errorf("error parsing %s", addr)
			} else if !a.Allowed(ip) {
				t.Errorf("should allow %s", addr)
			}
		})
	}

	for _, addr := range notAllowed {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Errorf("error parsing %s", addr)
			} else if a.Allowed(ip) {
				t.Errorf("should not allow %s", addr)
			}
		})
	}
}
