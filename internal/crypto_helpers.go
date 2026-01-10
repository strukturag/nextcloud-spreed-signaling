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
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func GenerateSelfSignedCertificateForTesting(t *testing.T, organization string, key *rsa.PrivateKey) *x509.Certificate {
	t.Helper()
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{organization},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour * 24 * 180),

		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	data, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(data)
	require.NoError(t, err)

	return cert
}

func WritePrivateKey(key *rsa.PrivateKey, filename string) error {
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return os.WriteFile(filename, data, 0600)
}

func WritePublicKey(key *rsa.PublicKey, filename string) error {
	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return err
	}

	data = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: data,
	})

	return os.WriteFile(filename, data, 0755)
}

func WriteCertificate(cert *x509.Certificate, filename string) error {
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	return os.WriteFile(filename, data, 0755)
}

func ReplaceCertificate(t *testing.T, filename string, cert *x509.Certificate) {
	t.Helper()
	require := require.New(t)
	oldStat, err := os.Stat(filename)
	require.NoError(err, "can't stat old file %s", filename)

	data := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	for {
		require.NoError(os.WriteFile(filename, data, 0755), "can't write file %s", filename)

		newStat, err := os.Stat(filename)
		require.NoError(err, "can't stat new file %s", filename)

		// We need different modification times.
		if !newStat.ModTime().Equal(oldStat.ModTime()) {
			break
		}

		time.Sleep(time.Millisecond)
	}
}
