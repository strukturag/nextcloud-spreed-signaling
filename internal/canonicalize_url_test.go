/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package internal

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalizeUrl(t *testing.T) {
	t.Parallel()
	mustParse := func(s string) *url.URL {
		t.Helper()
		u, err := url.Parse(s)
		require.NoError(t, err)
		return u
	}
	testcases := []struct {
		url      *url.URL
		expected *url.URL
	}{
		{
			url:      mustParse("http://server.domain.tld:80/foo/"),
			expected: mustParse("http://server.domain.tld/foo/"),
		},
		{
			url:      mustParse("http://server.domain.tld:81/foo/"),
			expected: mustParse("http://server.domain.tld:81/foo/"),
		},
		{
			url:      mustParse("https://server.domain.tld:443/foo/"),
			expected: mustParse("https://server.domain.tld/foo/"),
		},
		{
			url:      mustParse("https://server.domain.tld:444/foo/"),
			expected: mustParse("https://server.domain.tld:444/foo/"),
		},
		{
			url:      mustParse("foo://server.domain.tld:443/foo/"),
			expected: mustParse("foo://server.domain.tld:443/foo/"),
		},
	}

	assert := assert.New(t)
	for idx, tc := range testcases {
		expectChanged := tc.url.String() != tc.expected.String()
		canonicalized, changed := CanonicalizeUrl(tc.url)
		assert.Equal(tc.url, canonicalized) // urls will be changed inplace
		if !expectChanged {
			assert.False(changed, "testcase %d should not have changed the url", idx)
			continue
		}

		if assert.True(changed, "testcase %d: should have changed the url", idx) {
			assert.Equal(tc.expected, canonicalized, "testcase %d failed", idx)
		}
	}
}

func TestCanonicalizeUrlString(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		s        string
		expected string
		err      string
	}{
		{
			s:        "http://server.domain.tld:80/foo/",
			expected: "http://server.domain.tld/foo/",
		},
		{
			s:        "http://server.domain.tld:81/foo/",
			expected: "http://server.domain.tld:81/foo/",
		},
		{
			s:        "https://server.domain.tld:443/foo/",
			expected: "https://server.domain.tld/foo/",
		},
		{
			s:        "https://server.domain.tld:444/foo/",
			expected: "https://server.domain.tld:444/foo/",
		},
		{
			s:        "foo://server.domain.tld:443/foo/",
			expected: "foo://server.domain.tld:443/foo/",
		},
		{
			s:   "://server.domain.tld:443/foo/",
			err: "missing protocol",
		},
	}

	assert := assert.New(t)
	for idx, tc := range testcases {
		canonicalized, err := CanonicalizeUrlString(tc.s)
		if tc.err != "" {
			assert.ErrorContains(err, tc.err, "testcase %d failed", idx)
			continue
		}

		if assert.NoError(err, "testcase %d: should not have failed", idx) {
			assert.Equal(tc.expected, canonicalized, "testcase %d failed", idx)
		}
	}
}
