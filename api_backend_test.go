/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackendChecksum(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	rnd := newRandomString(32)
	body := []byte{1, 2, 3, 4, 5}
	secret := []byte("shared-secret")

	check1 := CalculateBackendChecksum(rnd, body, secret)
	check2 := CalculateBackendChecksum(rnd, body, secret)
	assert.Equal(check1, check2, "Expected equal checksums")

	assert.True(ValidateBackendChecksumValue(check1, rnd, body, secret), "Checksum should be valid")
	assert.False(ValidateBackendChecksumValue(check1[1:], rnd, body, secret), "Checksum should not be valid")
	assert.False(ValidateBackendChecksumValue(check1[:len(check1)-1], rnd, body, secret), "Checksum should not be valid")

	request := &http.Request{
		Header: make(http.Header),
	}
	request.Header.Set("Spreed-Signaling-Random", rnd)
	request.Header.Set("Spreed-Signaling-Checksum", check1)
	assert.True(ValidateBackendChecksum(request, body, secret), "Checksum could not be validated from request")
}

func TestValidNumbers(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	valid := []string{
		"+12",
		"+12345",
	}
	invalid := []string{
		"+1",
		"12345",
		" +12345",
		" +12345 ",
		"+123-45",
	}
	for _, number := range valid {
		assert.True(isValidNumber(number), "number %s should be valid", number)
	}
	for _, number := range invalid {
		assert.False(isValidNumber(number), "number %s should not be valid", number)
	}
}

func TestValidateBackendInformationEtcd(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	testcases := []struct {
		b             BackendInformationEtcd
		expectedError string
		expectedUrls  []string
	}{
		{
			b:             BackendInformationEtcd{},
			expectedError: "secret missing",
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
			},
			expectedError: "urls missing",
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Url:    "https://foo\n",
			},
			expectedError: "invalid url",
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo\n"},
			},
			expectedError: "invalid url",
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo", "https://foo\n"},
			},
			expectedError: "invalid url",
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Url:    "https://foo:443",
			},
			expectedUrls: []string{"https://foo"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo:443"},
			},
			expectedUrls: []string{"https://foo"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Url:    "https://foo:8443",
			},
			expectedUrls: []string{"https://foo:8443"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo:8443"},
			},
			expectedUrls: []string{"https://foo:8443"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo", "https://bar", "https://foo"},
			},
			expectedUrls: []string{"https://bar", "https://foo"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo", "https://bar", "https://foo:443", "https://zaz"},
			},
			expectedUrls: []string{"https://bar", "https://foo", "https://zaz"},
		},
		{
			b: BackendInformationEtcd{
				Secret: "verysecret",
				Urls:   []string{"https://foo:443", "https://bar", "https://foo", "https://zaz"},
			},
			expectedUrls: []string{"https://bar", "https://foo", "https://zaz"},
		},
	}

	for idx, tc := range testcases {
		if tc.expectedError == "" {
			if assert.NoError(tc.b.CheckValid(), "failed for testcase %d", idx) {
				assert.Equal(tc.expectedUrls, tc.b.Urls, "failed for testcase %d", idx)
				var urls []string
				for _, u := range tc.b.parsedUrls {
					urls = append(urls, u.String())
				}
				assert.Equal(tc.expectedUrls, urls, "failed for testcase %d", idx)
			}
		} else {
			assert.ErrorContains(tc.b.CheckValid(), tc.expectedError, "failed for testcase %d, got %+v", idx, tc.b.parsedUrls)
		}
	}
}
