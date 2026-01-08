/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package client

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/container"
)

func TestGetRealUserIP(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		expected string
		headers  http.Header
		trusted  string
		addr     string
	}{
		{
			"192.168.1.2",
			nil,
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"invalid-ip",
			nil,
			"192.168.0.0/16",
			"invalid-ip",
		},
		{
			"invalid-ip",
			nil,
			"192.168.0.0/16",
			"invalid-ip:12345",
		},
		{
			"10.11.12.13",
			nil,
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"10.11.12.13"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"2002:db8::1"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14:1234, 192.168.30.32:2345"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"10.11.12.13"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"2002:db8::1"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 2001:db8::1"},
			},
			"192.168.0.0/16, 2001:db8::/48",
			"192.168.1.2:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 192.168.30.32"},
			},
			"192.168.0.0/16, 2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		{
			"2002:db8::1",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"2002:db8::1, 2001:db8::2"},
			},
			"2001:db8::/48",
			"[2001:db8::1]:23456",
		},
		// "X-Real-IP" has preference before "X-Forwarded-For"
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"10.11.12.13"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		// Multiple "X-Forwarded-For" headers are merged.
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"1.2.3.4", "11.12.13.14", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"1.2.3.4", "2.3.4.5", "11.12.13.14", "192.168.31.32", "192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		// Headers are ignored if coming from untrusted clients.
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"11.12.13.14"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		// X-Forwarded-For is filtered for trusted proxies.
		{
			"1.2.3.4",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"1.2.3.4",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4, 192.168.2.3"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"10.11.12.13",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 1.2.3.4"},
			},
			"192.168.0.0/16",
			"10.11.12.13:23456",
		},
		// Invalid IPs are ignored.
		{
			"192.168.1.2",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"): []string{"this-is-not-an-ip"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"this-is-not-an-ip"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"11.12.13.14",
			http.Header{
				http.CanonicalHeaderKey("x-real-ip"):       []string{"this-is-not-an-ip"},
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"11.12.13.14, 192.168.30.32, proxy1"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"192.168.1.2",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"this-is-not-an-ip"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
		{
			"192.168.2.3",
			http.Header{
				http.CanonicalHeaderKey("x-forwarded-for"): []string{"this-is-not-an-ip, 192.168.2.3"},
			},
			"192.168.0.0/16",
			"192.168.1.2:23456",
		},
	}

	for _, tc := range testcases {
		trustedProxies, err := container.ParseIPList(tc.trusted)
		if !assert.NoError(t, err, "invalid trusted proxies in %+v", tc) {
			continue
		}
		request := &http.Request{
			RemoteAddr: tc.addr,
			Header:     tc.headers,
		}
		assert.Equal(t, tc.expected, GetRealUserIP(request, trustedProxies), "failed for %+v", tc)
	}
}
