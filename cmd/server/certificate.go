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
	"crypto/tls"
	"errors"
	"sync/atomic"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
)

// reloadableCertificate holds the certificate of the HTTPS listeners which
// can be replaced on reload.
type reloadableCertificate struct {
	logger log.Logger

	cert atomic.Pointer[tls.Certificate]
}

func loadHttpsCertificate(cfg *goconf.ConfigFile) (*tls.Certificate, error) {
	certFile, _ := cfg.GetString("https", "certificate")
	keyFile, _ := cfg.GetString("https", "key")
	if certFile == "" || keyFile == "" {
		return nil, errors.New("need a certificate and key for the HTTPS listener")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &cert, nil
}

func newReloadableCertificate(logger log.Logger, cfg *goconf.ConfigFile) (*reloadableCertificate, error) {
	cert, err := loadHttpsCertificate(cfg)
	if err != nil {
		return nil, err
	}

	result := &reloadableCertificate{
		logger: logger,
	}
	result.cert.Store(cert)
	return result, nil
}

func (c *reloadableCertificate) Reload(cfg *goconf.ConfigFile) {
	cert, err := loadHttpsCertificate(cfg)
	if err != nil {
		c.logger.Printf("Could not reload HTTPS certificate, keeping existing: %s", err)
		return
	}

	c.cert.Store(cert)
	c.logger.Printf("Reloaded HTTPS certificate")
}

func (c *reloadableCertificate) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.cert.Load(), nil
}
