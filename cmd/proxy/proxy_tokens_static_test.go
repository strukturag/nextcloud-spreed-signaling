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
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
)

func TestStaticTokens(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	filename := path.Join(t.TempDir(), "token.pub")

	key1, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	require.NoError(internal.WritePublicKey(&key1.PublicKey, filename))

	logger := logtest.NewLoggerForTest(t)
	config := goconf.NewConfigFile()
	config.AddOption("tokens", "foo", filename)

	tokens, err := NewProxyTokensStatic(logger, config)
	require.NoError(err)

	defer tokens.Close()

	if token, err := tokens.Get("foo"); assert.NoError(err) {
		assert.Equal("foo", token.id)
		assert.True(key1.PublicKey.Equal(token.key))
	}

	key2, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	require.NoError(internal.WritePublicKey(&key2.PublicKey, filename))

	tokens.Reload(config)

	if token, err := tokens.Get("foo"); assert.NoError(err) {
		assert.Equal("foo", token.id)
		assert.True(key2.PublicKey.Equal(token.key))
	}
}

func testStaticTokensMissing(t *testing.T, reload bool) {
	require := require.New(t)
	assert := assert.New(t)

	filename := path.Join(t.TempDir(), "token.pub")

	logger := logtest.NewLoggerForTest(t)
	config := goconf.NewConfigFile()
	if !reload {
		config.AddOption("tokens", "foo", filename)
	}

	tokens, err := NewProxyTokensStatic(logger, config)
	if !reload {
		assert.ErrorIs(err, os.ErrNotExist)
		return
	}

	require.NoError(err)
	defer tokens.Close()

	config.AddOption("tokens", "foo", filename)
	tokens.Reload(config)
}

func TestStaticTokensMissing(t *testing.T) {
	t.Parallel()

	testStaticTokensMissing(t, false)
}

func TestStaticTokensMissingReload(t *testing.T) {
	t.Parallel()

	testStaticTokensMissing(t, true)
}

func testStaticTokensEmpty(t *testing.T, reload bool) {
	require := require.New(t)
	assert := assert.New(t)

	logger := logtest.NewLoggerForTest(t)
	config := goconf.NewConfigFile()
	if !reload {
		config.AddOption("tokens", "foo", "")
	}

	tokens, err := NewProxyTokensStatic(logger, config)
	if !reload {
		assert.ErrorContains(err, "no filename given")
		return
	}

	require.NoError(err)
	defer tokens.Close()

	config.AddOption("tokens", "foo", "")
	tokens.Reload(config)
}

func TestStaticTokensEmpty(t *testing.T) {
	t.Parallel()

	testStaticTokensEmpty(t, false)
}

func TestStaticTokensEmptyReload(t *testing.T) {
	t.Parallel()

	testStaticTokensEmpty(t, true)
}

func testStaticTokensInvalidData(t *testing.T, reload bool) {
	require := require.New(t)
	assert := assert.New(t)

	filename := path.Join(t.TempDir(), "token.pub")
	require.NoError(os.WriteFile(filename, []byte("invalid-key-data"), 0600))

	logger := logtest.NewLoggerForTest(t)
	config := goconf.NewConfigFile()
	if !reload {
		config.AddOption("tokens", "foo", filename)
	}

	tokens, err := NewProxyTokensStatic(logger, config)
	if !reload {
		assert.ErrorContains(err, "could not parse public key")
		return
	}

	require.NoError(err)
	defer tokens.Close()

	config.AddOption("tokens", "foo", filename)
	tokens.Reload(config)
}

func TestStaticTokensInvalidData(t *testing.T) {
	t.Parallel()

	testStaticTokensInvalidData(t, false)
}

func TestStaticTokensInvalidDataReload(t *testing.T) {
	t.Parallel()

	testStaticTokensInvalidData(t, true)
}
