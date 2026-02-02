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
package security

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
)

type withReloadCounter interface {
	GetReloadCounter() uint64
}

func waitForReload(ctx context.Context, t *testing.T, r withReloadCounter, expected uint64) bool {
	t.Helper()

	for r.GetReloadCounter() < expected {
		if !assert.NoError(t, ctx.Err()) {
			return false
		}

		time.Sleep(time.Millisecond)
	}
	return true
}

func TestCertificateReloader(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)

	org1 := "Testing certificate"
	cert1 := internal.GenerateSelfSignedCertificateForTesting(t, org1, key)

	tmpdir := t.TempDir()
	certFile := path.Join(tmpdir, "cert.pem")
	privkeyFile := path.Join(tmpdir, "privkey.pem")
	pubkeyFile := path.Join(tmpdir, "pubkey.pem")

	require.NoError(internal.WritePrivateKey(key, privkeyFile))
	require.NoError(internal.WritePublicKey(&key.PublicKey, pubkeyFile))
	require.NoError(internal.WriteCertificate(cert1, certFile))

	logger := logtest.NewLoggerForTest(t)
	reloader, err := NewCertificateReloader(logger, certFile, privkeyFile)
	require.NoError(err)

	defer reloader.Close()

	if cert, err := reloader.GetCertificate(nil); assert.NoError(err) {
		assert.True(cert1.Equal(cert.Leaf))
		assert.True(key.Equal(cert.PrivateKey))
	}
	if cert, err := reloader.GetClientCertificate(nil); assert.NoError(err) {
		assert.True(cert1.Equal(cert.Leaf))
		assert.True(key.Equal(cert.PrivateKey))
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	org2 := "Updated certificate"
	cert2 := internal.GenerateSelfSignedCertificateForTesting(t, org2, key)
	internal.ReplaceCertificate(t, certFile, cert2)

	waitForReload(ctx, t, reloader, 1)

	if cert, err := reloader.GetCertificate(nil); assert.NoError(err) {
		assert.True(cert2.Equal(cert.Leaf))
		assert.True(key.Equal(cert.PrivateKey))
	}
	if cert, err := reloader.GetClientCertificate(nil); assert.NoError(err) {
		assert.True(cert2.Equal(cert.Leaf))
		assert.True(key.Equal(cert.PrivateKey))
	}
}

func TestCertPoolReloader(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)

	org1 := "Testing certificate"
	cert1 := internal.GenerateSelfSignedCertificateForTesting(t, org1, key)

	tmpdir := t.TempDir()
	certFile := path.Join(tmpdir, "cert.pem")
	privkeyFile := path.Join(tmpdir, "privkey.pem")
	pubkeyFile := path.Join(tmpdir, "pubkey.pem")

	require.NoError(internal.WritePrivateKey(key, privkeyFile))
	require.NoError(internal.WritePublicKey(&key.PublicKey, pubkeyFile))
	require.NoError(internal.WriteCertificate(cert1, certFile))

	logger := logtest.NewLoggerForTest(t)
	reloader, err := NewCertPoolReloader(logger, certFile)
	require.NoError(err)

	defer reloader.Close()

	pool1 := reloader.GetCertPool()
	assert.NotNil(pool1)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	org2 := "Updated certificate"
	cert2 := internal.GenerateSelfSignedCertificateForTesting(t, org2, key)
	internal.ReplaceCertificate(t, certFile, cert2)

	waitForReload(ctx, t, reloader, 1)

	pool2 := reloader.GetCertPool()
	assert.NotNil(pool2)

	assert.False(pool1.Equal(pool2))
}
