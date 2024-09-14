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
	"context"
	"net/url"
	"reflect"
	"sort"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testUrls(t *testing.T, config *BackendConfiguration, valid_urls []string, invalid_urls []string) {
	for _, u := range valid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			assert := assert.New(t)
			parsed, err := url.ParseRequestURI(u)
			if !assert.NoError(err, "The url %s should be valid", u) {
				return
			}
			assert.True(config.IsUrlAllowed(parsed), "The url %s should be allowed", u)
			secret := config.GetSecret(parsed)
			assert.Equal(string(testBackendSecret), string(secret), "Expected secret %s for url %s, got %s", string(testBackendSecret), u, string(secret))
		})
	}
	for _, u := range invalid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			assert := assert.New(t)
			parsed, _ := url.ParseRequestURI(u)
			assert.False(config.IsUrlAllowed(parsed), "The url %s should not be allowed", u)
		})
	}
}

func testBackends(t *testing.T, config *BackendConfiguration, valid_urls [][]string, invalid_urls []string) {
	for _, entry := range valid_urls {
		entry := entry
		t.Run(entry[0], func(t *testing.T) {
			assert := assert.New(t)
			u := entry[0]
			parsed, err := url.ParseRequestURI(u)
			if !assert.NoError(err, "The url %s should be valid", u) {
				return
			}
			assert.True(config.IsUrlAllowed(parsed), "The url %s should be allowed", u)
			s := entry[1]
			secret := config.GetSecret(parsed)
			assert.Equal(s, string(secret), "Expected secret %s for url %s, got %s", s, u, string(secret))
		})
	}
	for _, u := range invalid_urls {
		u := u
		t.Run(u, func(t *testing.T) {
			assert := assert.New(t)
			parsed, _ := url.ParseRequestURI(u)
			assert.False(config.IsUrlAllowed(parsed), "The url %s should not be allowed", u)
		})
	}
}

func TestIsUrlAllowed_Compat(t *testing.T) {
	log := GetLoggerForTest(t)
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
	config.AddOption("backend", "allowhttp", "true")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(log, config, nil)
	require.NoError(t, err)
	testUrls(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_CompatForceHttps(t *testing.T) {
	log := GetLoggerForTest(t)
	// Old-style configuration, force HTTPS
	valid_urls := []string{
		"https://domain.invalid",
	}
	invalid_urls := []string{
		"http://domain.invalid",
		"http://otherdomain.invalid",
		"https://otherdomain.invalid",
		"domain.invalid",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", "domain.invalid")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(log, config, nil)
	require.NoError(t, err)
	testUrls(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed(t *testing.T) {
	log := GetLoggerForTest(t)
	valid_urls := [][]string{
		{"https://domain.invalid/foo", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/foo/", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid:443/foo/", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/foo/folder", string(testBackendSecret) + "-foo"},
		{"https://domain.invalid/bar", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid/bar/", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid:443/bar/", string(testBackendSecret) + "-bar"},
		{"https://domain.invalid/bar/folder/", string(testBackendSecret) + "-bar"},
		{"http://domain.invalid/baz", string(testBackendSecret) + "-baz"},
		{"http://domain.invalid/baz/", string(testBackendSecret) + "-baz"},
		{"http://domain.invalid:80/baz/", string(testBackendSecret) + "-baz"},
		{"http://domain.invalid/baz/folder/", string(testBackendSecret) + "-baz"},
		{"https://otherdomain.invalid/", string(testBackendSecret) + "-lala"},
		{"https://otherdomain.invalid/folder/", string(testBackendSecret) + "-lala"},
	}
	invalid_urls := []string{
		"http://domain.invalid",
		"http://domain.invalid/",
		"https://domain.invalid",
		"https://domain.invalid/",
		"http://domain.invalid/foo",
		"http://domain.invalid/foo/",
		"https://domain.invalid:8443/foo/",
		"https://www.domain.invalid/foo/",
		"https://domain.invalid/baz/",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "backends", "foo, bar, baz, lala, missing")
	config.AddOption("foo", "url", "https://domain.invalid/foo")
	config.AddOption("foo", "secret", string(testBackendSecret)+"-foo")
	config.AddOption("bar", "url", "https://domain.invalid:443/bar/")
	config.AddOption("bar", "secret", string(testBackendSecret)+"-bar")
	config.AddOption("baz", "url", "http://domain.invalid/baz")
	config.AddOption("baz", "secret", string(testBackendSecret)+"-baz")
	config.AddOption("lala", "url", "https://otherdomain.invalid/")
	config.AddOption("lala", "secret", string(testBackendSecret)+"-lala")
	cfg, err := NewBackendConfiguration(log, config, nil)
	require.NoError(t, err)
	testBackends(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_EmptyAllowlist(t *testing.T) {
	log := GetLoggerForTest(t)
	valid_urls := []string{}
	invalid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
		"domain.invalid",
	}
	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", "")
	config.AddOption("backend", "secret", string(testBackendSecret))
	cfg, err := NewBackendConfiguration(log, config, nil)
	require.NoError(t, err)
	testUrls(t, cfg, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_AllowAll(t *testing.T) {
	log := GetLoggerForTest(t)
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
	cfg, err := NewBackendConfiguration(log, config, nil)
	require.NoError(t, err)
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

	assert := assert.New(t)
	for _, test := range testcases {
		ids := getConfiguredBackendIDs(test.s)
		assert.Equal(test.ids, ids, "List of ids differs for \"%s\"", test.s)
	}
}

func TestBackendReloadNoChange(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)
	checkStatsValue(t, statsBackendsCurrent, current+2)

	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+4)
	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadChangeExistingURL(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain3.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend1", "sessionlimit", "10")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+4)
	original_config.RemoveOption("backend1", "url")
	original_config.AddOption("backend1", "url", "http://domain3.invalid")
	original_config.AddOption("backend1", "sessionlimit", "10")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadChangeSecret(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend3")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+4)
	original_config.RemoveOption("backend1", "secret")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend3")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadAddBackend(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+1)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1, backend2")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	new_config.AddOption("backend2", "url", "http://domain2.invalid")
	new_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	new_config.AddOption("backend2", "sessionlimit", "10")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	original_config.AddOption("backend2", "sessionlimit", "10")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+4)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadRemoveHost(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain2.invalid")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1")
	original_config.RemoveSection("backend2")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+2)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func TestBackendReloadRemoveBackendFromSharedHost(t *testing.T) {
	log := GetLoggerForTest(t)
	require := require.New(t)
	current := testutil.ToFloat64(statsBackendsCurrent)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "allowall", "false")
	original_config.AddOption("backend1", "url", "http://domain1.invalid/foo/")
	original_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	original_config.AddOption("backend2", "url", "http://domain1.invalid/bar/")
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	o_cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+2)
	new_config := goconf.NewConfigFile()
	new_config.AddOption("backend", "backends", "backend1")
	new_config.AddOption("backend", "allowall", "false")
	new_config.AddOption("backend1", "url", "http://domain1.invalid/foo/")
	new_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	n_cfg, err := NewBackendConfiguration(log, new_config, nil)
	require.NoError(err)

	checkStatsValue(t, statsBackendsCurrent, current+3)
	original_config.RemoveOption("backend", "backends")
	original_config.AddOption("backend", "backends", "backend1")
	original_config.RemoveSection("backend2")

	o_cfg.Reload(original_config)
	checkStatsValue(t, statsBackendsCurrent, current+2)
	if !reflect.DeepEqual(n_cfg, o_cfg) {
		assert.Fail(t, "BackendConfiguration should be equal after Reload")
	}
}

func sortBackends(backends []*Backend) []*Backend {
	result := make([]*Backend, len(backends))
	copy(result, backends)

	sort.Slice(result, func(i, j int) bool {
		return result[i].Id() < result[j].Id()
	})
	return result
}

func mustParse(s string) *url.URL {
	p, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}

func TestBackendConfiguration_Etcd(t *testing.T) {
	t.Parallel()
	log := GetLoggerForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	etcd, client := NewEtcdClientForTest(t)

	url1 := "https://domain1.invalid/foo"
	initialSecret1 := string(testBackendSecret) + "-backend1-initial"
	secret1 := string(testBackendSecret) + "-backend1"

	SetEtcdValue(etcd, "/backends/1_one", []byte("{\"url\":\""+url1+"\",\"secret\":\""+initialSecret1+"\"}"))

	config := goconf.NewConfigFile()
	config.AddOption("backend", "backendtype", "etcd")
	config.AddOption("backend", "backendprefix", "/backends")

	cfg, err := NewBackendConfiguration(log, config, client)
	require.NoError(err)
	defer cfg.Close()

	storage := cfg.storage.(*backendStorageEtcd)
	ch := storage.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(storage.WaitForInitialized(ctx))

	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 1) &&
		assert.Equal(url1, backends[0].url) &&
		assert.Equal(initialSecret1, string(backends[0].secret)) {
		if backend := cfg.GetBackend(mustParse(url1)); backend != backends[0] {
			assert.Fail("Expected backend %+v, got %+v", backends[0], backend)
		}
	}

	drainWakeupChannel(ch)
	SetEtcdValue(etcd, "/backends/1_one", []byte("{\"url\":\""+url1+"\",\"secret\":\""+secret1+"\"}"))
	<-ch
	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 1) &&
		assert.Equal(url1, backends[0].url) &&
		assert.Equal(secret1, string(backends[0].secret)) {
		if backend := cfg.GetBackend(mustParse(url1)); backend != backends[0] {
			assert.Fail("Expected backend %+v, got %+v", backends[0], backend)
		}
	}

	url2 := "https://domain1.invalid/bar"
	secret2 := string(testBackendSecret) + "-backend2"

	drainWakeupChannel(ch)
	SetEtcdValue(etcd, "/backends/2_two", []byte("{\"url\":\""+url2+"\",\"secret\":\""+secret2+"\"}"))
	<-ch
	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 2) &&
		assert.Equal(url1, backends[0].url) &&
		assert.Equal(secret1, string(backends[0].secret)) &&
		assert.Equal(url2, backends[1].url) &&
		assert.Equal(secret2, string(backends[1].secret)) {
		if backend := cfg.GetBackend(mustParse(url1)); backend != backends[0] {
			assert.Fail("Expected backend %+v, got %+v", backends[0], backend)
		} else if backend := cfg.GetBackend(mustParse(url2)); backend != backends[1] {
			assert.Fail("Expected backend %+v, got %+v", backends[1], backend)
		}
	}

	url3 := "https://domain2.invalid/foo"
	secret3 := string(testBackendSecret) + "-backend3"

	drainWakeupChannel(ch)
	SetEtcdValue(etcd, "/backends/3_three", []byte("{\"url\":\""+url3+"\",\"secret\":\""+secret3+"\"}"))
	<-ch
	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 3) &&
		assert.Equal(url1, backends[0].url) &&
		assert.Equal(secret1, string(backends[0].secret)) &&
		assert.Equal(url2, backends[1].url) &&
		assert.Equal(secret2, string(backends[1].secret)) &&
		assert.Equal(url3, backends[2].url) &&
		assert.Equal(secret3, string(backends[2].secret)) {
		if backend := cfg.GetBackend(mustParse(url1)); backend != backends[0] {
			assert.Fail("Expected backend %+v, got %+v", backends[0], backend)
		} else if backend := cfg.GetBackend(mustParse(url2)); backend != backends[1] {
			assert.Fail("Expected backend %+v, got %+v", backends[1], backend)
		} else if backend := cfg.GetBackend(mustParse(url3)); backend != backends[2] {
			assert.Fail("Expected backend %+v, got %+v", backends[2], backend)
		}
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/backends/1_one")
	<-ch
	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 2) {
		assert.Equal(url2, backends[0].url)
		assert.Equal(secret2, string(backends[0].secret))
		assert.Equal(url3, backends[1].url)
		assert.Equal(secret3, string(backends[1].secret))
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/backends/2_two")
	<-ch
	if backends := sortBackends(cfg.GetBackends()); assert.Len(backends, 1) {
		assert.Equal(url3, backends[0].url)
		assert.Equal(secret3, string(backends[0].secret))
	}

	if _, found := storage.backends["domain1.invalid"]; found {
		assert.Fail("Should have removed host information for %s", "domain1.invalid")
	}
}

func TestBackendCommonSecret(t *testing.T) {
	t.Parallel()
	log := GetLoggerForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	u1, err := url.Parse("http://domain1.invalid")
	require.NoError(err)
	u2, err := url.Parse("http://domain2.invalid")
	require.NoError(err)
	original_config := goconf.NewConfigFile()
	original_config.AddOption("backend", "backends", "backend1, backend2")
	original_config.AddOption("backend", "secret", string(testBackendSecret))
	original_config.AddOption("backend1", "url", u1.String())
	original_config.AddOption("backend2", "url", u2.String())
	original_config.AddOption("backend2", "secret", string(testBackendSecret)+"-backend2")
	cfg, err := NewBackendConfiguration(log, original_config, nil)
	require.NoError(err)

	if b1 := cfg.GetBackend(u1); assert.NotNil(b1) {
		assert.Equal(string(testBackendSecret), string(b1.Secret()))
	}
	if b2 := cfg.GetBackend(u2); assert.NotNil(b2) {
		assert.Equal(string(testBackendSecret)+"-backend2", string(b2.Secret()))
	}

	updated_config := goconf.NewConfigFile()
	updated_config.AddOption("backend", "backends", "backend1, backend2")
	updated_config.AddOption("backend", "secret", string(testBackendSecret))
	updated_config.AddOption("backend1", "url", u1.String())
	updated_config.AddOption("backend1", "secret", string(testBackendSecret)+"-backend1")
	updated_config.AddOption("backend2", "url", u2.String())
	cfg.Reload(updated_config)

	if b1 := cfg.GetBackend(u1); assert.NotNil(b1) {
		assert.Equal(string(testBackendSecret)+"-backend1", string(b1.Secret()))
	}
	if b2 := cfg.GetBackend(u2); assert.NotNil(b2) {
		assert.Equal(string(testBackendSecret), string(b2.Secret()))
	}
}
