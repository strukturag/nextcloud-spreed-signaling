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
	"path"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/internal"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
)

func writeCertificateForTest(t *testing.T, dir string, name string) (string, string, *rsa.PrivateKey) {
	t.Helper()
	require := require.New(t)

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	keyFile := path.Join(dir, name+".key")
	require.NoError(internal.WritePrivateKey(key, keyFile))

	cert := internal.GenerateSelfSignedCertificateForTesting(t, name, key)
	certFile := path.Join(dir, name+".crt")
	require.NoError(internal.WriteCertificate(cert, certFile))
	return certFile, keyFile, key
}

func TestReloadableCertificate(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	logger := logtest.NewLoggerForTest(t)
	dir := t.TempDir()

	certFile1, keyFile1, key1 := writeCertificateForTest(t, dir, "first")
	certFile2, keyFile2, key2 := writeCertificateForTest(t, dir, "second")

	config := goconf.NewConfigFile()
	_, err := newReloadableCertificate(logger, config)
	assert.Error(err)

	config.AddOption("https", "certificate", certFile1)
	config.AddOption("https", "key", keyFile1)
	cert, err := newReloadableCertificate(logger, config)
	require.NoError(err)

	if c, err := cert.GetCertificate(nil); assert.NoError(err) {
		assert.True(key1.Equal(c.PrivateKey))
	}

	config.AddOption("https", "certificate", certFile2)
	config.AddOption("https", "key", keyFile2)
	cert.Reload(config)
	if c, err := cert.GetCertificate(nil); assert.NoError(err) {
		assert.True(key2.Equal(c.PrivateKey))
	}

	// Invalid configurations keep the existing certificate.
	config.AddOption("https", "key", keyFile1)
	cert.Reload(config)
	config.AddOption("https", "certificate", path.Join(dir, "missing.crt"))
	cert.Reload(config)
	config.RemoveOption("https", "key")
	cert.Reload(config)
	if c, err := cert.GetCertificate(nil); assert.NoError(err) {
		assert.True(key2.Equal(c.PrivateKey))
	}
}
