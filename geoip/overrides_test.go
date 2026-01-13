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
package geoip

import (
	"maps"
	"net"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
)

func mustSucceed1[T any, A1 any](t *testing.T, f func(a1 A1) (T, bool), a1 A1) T {
	t.Helper()
	result, ok := f(a1)
	if !ok {
		t.FailNow()
	}
	return result
}

func TestOverridesEmpty(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	config := goconf.NewConfigFile()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	overrides, err := LoadOverrides(ctx, config, true)
	require.NoError(err)
	assert.Empty(overrides)
}

func TestOverrides(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	config := goconf.NewConfigFile()
	config.AddOption("geoip-overrides", "10.1.0.0/16", "DE")
	config.AddOption("geoip-overrides", "2001:db8::/48", "FR")
	config.AddOption("geoip-overrides", "2001:db9::3", "CH")
	config.AddOption("geoip-overrides", "10.3.4.5", "custom")
	config.AddOption("geoip-overrides", "10.4.5.6", "loopback")
	config.AddOption("geoip-overrides", "192.168.1.0", "")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	overrides, err := LoadOverrides(ctx, config, true)
	require.NoError(err)

	if assert.Len(overrides, 4) {
		assert.EqualValues("DE", mustSucceed1(t, overrides.Lookup, net.ParseIP("10.1.2.3")))
		assert.EqualValues("DE", mustSucceed1(t, overrides.Lookup, net.ParseIP("10.1.3.4")))
		assert.EqualValues("FR", mustSucceed1(t, overrides.Lookup, net.ParseIP("2001:db8::1")))
		assert.EqualValues("FR", mustSucceed1(t, overrides.Lookup, net.ParseIP("2001:db8::2")))
		assert.EqualValues("CH", mustSucceed1(t, overrides.Lookup, net.ParseIP("2001:db9::3")))
		assert.EqualValues("CUSTOM", mustSucceed1(t, overrides.Lookup, net.ParseIP("10.3.4.5")))

		country, ok := overrides.Lookup(net.ParseIP("10.4.5.6"))
		assert.False(ok, "expected no country, got %s", country)

		country, ok = overrides.Lookup(net.ParseIP("192.168.1.0"))
		assert.False(ok, "expected no country, got %s", country)
	}
}

func TestOverridesInvalidIgnoreErrors(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	config := goconf.NewConfigFile()
	config.AddOption("geoip-overrides", "invalid-ip", "DE")
	config.AddOption("geoip-overrides", "300.1.2.3/8", "DE")
	config.AddOption("geoip-overrides", "10.2.0.0/16", "FR")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	overrides, err := LoadOverrides(ctx, config, true)
	require.NoError(err)

	if assert.Len(overrides, 1) {
		assert.EqualValues("FR", mustSucceed1(t, overrides.Lookup, net.ParseIP("10.2.3.4")))
		assert.EqualValues("FR", mustSucceed1(t, overrides.Lookup, net.ParseIP("10.2.4.5")))

		country, ok := overrides.Lookup(net.ParseIP("10.3.4.5"))
		assert.False(ok, "expected no country, got %s", country)

		country, ok = overrides.Lookup(net.ParseIP("192.168.1.0"))
		assert.False(ok, "expected no country, got %s", country)
	}
}

func TestOverridesInvalidIPReturnErrors(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	config := goconf.NewConfigFile()
	config.AddOption("geoip-overrides", "invalid-ip", "DE")
	config.AddOption("geoip-overrides", "10.2.0.0/16", "FR")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	overrides, err := LoadOverrides(ctx, config, false)
	assert.ErrorContains(err, "could not parse IP", err)
	assert.Empty(overrides)
}

func TestOverridesInvalidCIDRReturnErrors(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	config := goconf.NewConfigFile()
	config.AddOption("geoip-overrides", "300.1.2.3/8", "DE")
	config.AddOption("geoip-overrides", "10.2.0.0/16", "FR")

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)

	overrides, err := LoadOverrides(ctx, config, false)
	var e *net.ParseError
	assert.ErrorAs(err, &e)
	assert.Empty(overrides)
}

func TestAtomicOverrides(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	overrides := make(Overrides)
	overrides[&net.IPNet{
		IP:   net.ParseIP("10.1.2.3."),
		Mask: net.CIDRMask(32, 32),
	}] = "DE"

	var value AtomicOverrides
	assert.Nil(value.Load())
	value.Store(make(Overrides))
	assert.Nil(value.Load())
	value.Store(overrides)
	if o := value.Load(); assert.NotEmpty(o) {
		assert.Equal(overrides, o)
	}
	// Updating the overrides doesn't change the stored value.
	overrides2 := maps.Clone(overrides)
	overrides[&net.IPNet{
		IP:   net.ParseIP("10.1.2.3."),
		Mask: net.CIDRMask(32, 32),
	}] = "FR"
	if o := value.Load(); assert.NotEmpty(o) {
		assert.Equal(overrides2, o)
	}
}
