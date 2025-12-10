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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSelfSignedCertificateForTesting(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	bits := 1024
	key, err := rsa.GenerateKey(rand.Reader, bits)
	require.NoError(err)
	cert := GenerateSelfSignedCertificateForTesting(t, "Testing", key)
	require.NotNil(cert)

	if assert.Len(cert.Subject.Organization, 1) {
		assert.Equal("Testing", cert.Subject.Organization[0])
	}
	if assert.Len(cert.DNSNames, 1) {
		assert.Equal("localhost", cert.DNSNames[0])
	}
	if assert.Len(cert.IPAddresses, 1) {
		assert.Equal("127.0.0.1", cert.IPAddresses[0].String())
	}
	if assert.IsType(&rsa.PublicKey{}, cert.PublicKey) {
		pkey := cert.PublicKey.(*rsa.PublicKey)
		assert.Equal(bits/8, pkey.Size())
	}
}

func TestWriteKeys(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)

	dir := t.TempDir()
	privateFilename := path.Join(dir, "testing.key")
	if assert.NoError(WritePrivateKey(key, privateFilename)) {
		if data, err := os.ReadFile(privateFilename); assert.NoError(err) {
			if block, rest := pem.Decode(data); assert.Equal("RSA PRIVATE KEY", block.Type) && assert.Empty(rest) {
				if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); assert.NoError(err) {
					assert.True(key.Equal(parsed), "keys should be equal")
				}
			}
		}
	}

	publicFilename := path.Join(dir, "testing.pem")
	if assert.NoError(WritePublicKey(&key.PublicKey, publicFilename)) {
		if data, err := os.ReadFile(publicFilename); assert.NoError(err) {
			if block, rest := pem.Decode(data); assert.Equal("RSA PUBLIC KEY", block.Type) && assert.Empty(rest) {
				if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); assert.NoError(err) {
					assert.True(key.PublicKey.Equal(parsed), "keys should be equal")
				}
			}
		}
	}
}

func TestReplaceCertificate(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	bits := 1024
	key, err := rsa.GenerateKey(rand.Reader, bits)
	require.NoError(err)
	cert1 := GenerateSelfSignedCertificateForTesting(t, "Testing", key)
	require.NotNil(cert1)

	dir := t.TempDir()
	filename := path.Join(dir, "testing.crt")
	require.NoError(WriteCertificate(cert1, filename))
	stat1, err := os.Stat(filename)
	require.NoError(err)

	cert2 := GenerateSelfSignedCertificateForTesting(t, "Testing", key)
	require.NotNil(cert2)
	ReplaceCertificate(t, filename, cert2)
	stat2, err := os.Stat(filename)
	require.NoError(err)

	assert.NotEqual(stat1.ModTime(), stat2.ModTime())
}
