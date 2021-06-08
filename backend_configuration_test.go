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
	"reflect"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func testUrls(t *testing.T, config *BackendConfiguration, valid_urls []string, invalid_urls []string) {
	for _, u := range valid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			parsed, err := url.ParseRequestURI(u)
			if err != nil {
				t.Errorf("The url %s should be valid, got %s", u, err)
				return
			}
			if !config.IsUrlAllowed(parsed) {
				t.Errorf("The url %s should be allowed", u)
			}
			if secret := config.GetSecret(parsed); !bytes.Equal(secret, testBackendSecret) {
				t.Errorf("Expected secret %s for url %s, got %s", string(testBackendSecret), u, string(secret))
			}
		})
	}
	for _, u := range invalid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			parsed, _ := url.ParseRequestURI(u)
			if config.IsUrlAllowed(parsed) {
				t.Errorf("The url %s should not be allowed", u)
			}
		})
	}
}

func testBackends(t *testing.T, config *BackendConfiguration, valid_urls [][]string, invalid_urls []string) {
	for _, entry := range valid_urls {
		entry := entry
		t.Run(entry[0], func(t *testing.T) {
			u := entry[0]
			parsed, err := url.ParseRequestURI(u)
			if err != nil {
				t.Errorf("The url %s should be valid, got %s", u, err)
				return
			}
			if !config.IsUrlAllowed(parsed) {
				t.Errorf("The url %s should be allowed", u)
			}
			s := entry[1]
			if secret := config.GetSecret(parsed); !bytes.Equal(secret, []byte(s)) {
				t.Errorf("Expected secret %s for url %s, got %s", string(s), u, string(secret))
			}
		})
	}
	for _, u := range invalid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			parsed, _ := url.ParseRequestURI(u)
			if config.IsUrlAllowed(parsed) {
				t.Errorf("The url %s should not be allowed", u)
			}
		})
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
		{"https://domain.invalid/foo", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/foo/", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid:443/foo/", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/foo/folder", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/bar", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid/bar/", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid:443/bar/", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid/bar/folder/", string(testBackendSecret) + "-bar"},
		{"https://otherdomain.invalid/", string(testBackendSecret) + "-lala"},
		{"https://otherdomain.invalid/folder/", string(testBackendSecret) + "-lala"},
	}
	invalid_urls := []string{
		"https://domain.invalid",
		"https://domain.invalid/",
		"https://domain.invalid:8443/foo/",
		"https://www.domain.invalid/foo/",
		"https://domain.invalid/baz/",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "backends", "foo, bar, lala, missing")
	config.AddOption("foo", "url", "https://domain.invalid/foo")
	config.AddOption("foo", "secret", string(testBackendSecret)+"-foo")
	config.AddOption("bar", "url", "https://domain.invalid:443/bar/")
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
		"https://domain.invalid:443",
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

type ParseBackendIdsTestcase struct {
	s   string
	ids []string
}

func TestParseBackendIds(t *testing.T) {
	testcases := []ParseBackendIdsTestcase{
		{"", nil},
		{"backend1", []string{"backend1"}},
		{" backend1 ", []string{"backend1"}},
		{"backend1,", []string{"backend1"}},
		{"backend1,backend1", []string{"backend1"}},
		{"backend1, backend2", []string{"backend1", "backend2"}},
		{"backend1,backend2, backend1", []string{"backend1", "backend2"}},
	}

	for _, test := range testcases {
		ids := getConfiguredBackendIDs(test.s)
		if !reflect.DeepEqual(ids, test.ids) {
			t.Errorf("List of ids differs, expected %+v, got %+v", test.ids, ids)
		}
	}
}

func TestBackendReloadNoChange(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}
	checkStatsValue(t, statsBackendsCurrent, current+2)

	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+4)
	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadChangeExistingURL(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain3.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend1", "sessionlimit", "10")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+4)
	original_config.RemoveOption("backend1", "url")
	original_config.AddOption("backend1", "url", "http://domain3.invalid")
	original_config.AddOption("backend1", "sessionlimit", "10")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadChangeSecret(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend3")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+4)
	original_config.RemoveOption("backend1", "secret")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend3")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadAddBackend(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+1)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	new_config.AddOption("backend2", "sessionlimit", "10")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	original_config.AddOption("backend2", "sessionlimit", "10")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadRemoveHost(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1")
	original_config.RemoveSection("backend2")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+2)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadRemoveBackendFromSharedHost(t *testing.T) {
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid/foo/")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain1.invalid/bar/")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(original_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid/foo/")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	n_cfg, err := NewBackendConfiguration(new_config)
	if err != nil {
		t.Fatal(err)
	}

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1")
	original_config.RemoveSection("backend2")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+2)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		t.Error("BackendConfiguration should be equal after Reload")
	}
}
