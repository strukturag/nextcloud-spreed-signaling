/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

var (
	// CertificateCheckInterval defines the interval in which certificate files
	// are checked for modifications.
	CertificateCheckInterval = time.Minute
)

type CertificateReloader struct {
	mu sync.Mutex

	certFile string
	keyFile  string

	certificate  *tls.Certificate
	lastModified time.Time

	nextCheck time.Time
}

func NewCertificateReloader(certFile string, keyFile string) (*CertificateReloader, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("could not load certificate / key: %w", err)
	}

	stat, err := os.Stat(certFile)
	if err != nil {
		return nil, fmt.Errorf("could not stat %s: %w", certFile, err)
	}

	return &CertificateReloader{
		certFile: certFile,
		keyFile:  keyFile,

		certificate:  &pair,
		lastModified: stat.ModTime(),

		nextCheck: time.Now().Add(CertificateCheckInterval),
	}, nil
}

func (r *CertificateReloader) getCertificate() (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if now.Before(r.nextCheck) {
		return r.certificate, nil
	}

	r.nextCheck = now.Add(CertificateCheckInterval)

	stat, err := os.Stat(r.certFile)
	if err != nil {
		log.Printf("could not stat %s: %s", r.certFile, err)
		return r.certificate, nil
	}

	if !stat.ModTime().Equal(r.lastModified) {
		log.Printf("reloading certificate from %s with %s", r.certFile, r.keyFile)
		pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
		if err != nil {
			log.Printf("could not load certificate / key: %s", err)
			return r.certificate, nil
		}

		r.certificate = &pair
		r.lastModified = stat.ModTime()
	}

	return r.certificate, nil
}

func (r *CertificateReloader) GetCertificate(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return r.getCertificate()
}

func (r *CertificateReloader) GetClientCertificate(i *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return r.getCertificate()
}

type CertPoolReloader struct {
	mu sync.Mutex

	certFile string

	pool         *x509.CertPool
	lastModified time.Time

	nextCheck time.Time
}

func loadCertPool(filename string) (*x509.CertPool, error) {
	cert, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		return nil, fmt.Errorf("invalid CA in %s: %w", filename, err)
	}

	return pool, nil
}

func NewCertPoolReloader(certFile string) (*CertPoolReloader, error) {
	pool, err := loadCertPool(certFile)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(certFile)
	if err != nil {
		return nil, fmt.Errorf("could not stat %s: %w", certFile, err)
	}

	return &CertPoolReloader{
		certFile: certFile,

		pool:         pool,
		lastModified: stat.ModTime(),

		nextCheck: time.Now().Add(CertificateCheckInterval),
	}, nil
}

func (r *CertPoolReloader) GetCertPool() *x509.CertPool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if now.Before(r.nextCheck) {
		return r.pool
	}

	r.nextCheck = now.Add(CertificateCheckInterval)

	stat, err := os.Stat(r.certFile)
	if err != nil {
		log.Printf("could not stat %s: %s", r.certFile, err)
		return r.pool
	}

	if !stat.ModTime().Equal(r.lastModified) {
		log.Printf("reloading certificate pool from %s", r.certFile)
		pool, err := loadCertPool(r.certFile)
		if err != nil {
			log.Printf("could not load certificate pool: %s", err)
			return r.pool
		}

		r.pool = pool
		r.lastModified = stat.ModTime()
	}

	return r.pool
}
