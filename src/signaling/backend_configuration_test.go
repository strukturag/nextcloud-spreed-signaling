/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"bytes"
	"net/url"
	"testing"

	"github.com/dlintw/goconf"
)

func testUrls(t *testing.T, config *BackendConfiguration, valid_urls []string, invalid_urls []string) {
	for _, u := range valid_urls {
		parsed, err := url.ParseRequestURI(u)
		if err != nil {
			t.Errorf("The url %s should be valid, got %s", u, err)
			continue
		}
		if !config.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should be allowed", u)
		}
		if secret := config.GetSecret(parsed); !bytes.Equal(secret, testBackendSecret) {
			t.Errorf("Expected secret %s for url %s, got %s", string(testBackendSecret), u, string(secret))
		}
	}
	for _, u := range invalid_urls {
		parsed, _ := url.ParseRequestURI(u)
		if config.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should not be allowed", u)
		}
	}
}

func testBackends(t *testing.T, config *BackendConfiguration, valid_urls [][]string, invalid_urls []string) {
	for _, entry := range valid_urls {
		u := entry[0]
		parsed, err := url.ParseRequestURI(u)
		if err != nil {
			t.Errorf("The url %s should be valid, got %s", u, err)
			continue
		}
		if !config.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should be allowed", u)
		}
		s := entry[1]
		if secret := config.GetSecret(parsed); !bytes.Equal(secret, []byte(s)) {
			t.Errorf("Expected secret %s for url %s, got %s", string(s), u, string(secret))
		}
	}
	for _, u := range invalid_urls {
		parsed, _ := url.ParseRequestURI(u)
		if config.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should not be allowed", u)
		}
	}
}

func TestIsUrlAllowed_Compat(t *testing.T) {
	// Old-style configuration
	valid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
	}
	invalid_urls := []string{
		"http://otherdomain.invalid",
		"https://otherdomain.invalid",
		"domain.invalid",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", "domain.invalid")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(config)
	if err != nil {
		t.Fatal(err)
	}
	testUrls(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed(t *testing.T) {
	valid_urls := [][]string{
		[]string{"https://domain.invalid/foo", string(testBackendSecret) + "-foo"},
		[]string{"https://domain.invalid/foo/", string(testBackendSecret) + "-foo"},
		[]string{"https://domain.invalid/foo/folder", string(testBackendSecret) + "-foo"},
		[]string{"https://domain.invalid/bar", string(testBackendSecret) + "-bar"},
		[]string{"https://domain.invalid/bar/", string(testBackendSecret) + "-bar"},
		[]string{"https://domain.invalid/bar/folder/", string(testBackendSecret) + "-bar"},
		[]string{"https://otherdomain.invalid/", string(testBackendSecret) + "-lala"},
		[]string{"https://otherdomain.invalid/folder/", string(testBackendSecret) + "-lala"},
	}
	invalid_urls := []string{
		"https://domain.invalid",
		"https://domain.invalid/",
		"https://www.domain.invalid/foo/",
		"https://domain.invalid/baz/",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "backends", "foo, bar, lala, missing")
	config.AddOption("foo", "url", "https://domain.invalid/foo")
	config.AddOption("foo", "secret", string(testBackendSecret)+"-foo")
	config.AddOption("bar", "url", "https://domain.invalid/bar/")
	config.AddOption("bar", "secret", string(testBackendSecret)+"-bar")
	config.AddOption("lala", "url", "https://otherdomain.invalid/")
	config.AddOption("lala", "secret", string(testBackendSecret)+"-lala")
	cfg, err := NewBackendConfiguration(config)
	if err != nil {
		t.Fatal(err)
	}
	testBackends(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_EmptyAllowlist(t *testing.T) {
	valid_urls := []string{}
	invalid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
		"domain.invalid",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", "")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(config)
	if err != nil {
		t.Fatal(err)
	}
	testUrls(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_AllowAll(t *testing.T) {
	valid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
	}
	invalid_urls := []string{
		"domain.invalid",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowall", "true")
	config.AddOption("backend", "allowed", "")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(config)
	if err != nil {
		t.Fatal(err)
	}
	testUrls(t, cfg, valid_urls, invalid_urls)
}
