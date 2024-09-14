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
	"os"
	"sync/atomic"

	"go.uber.org/zap"
)

type CertificateReloader struct {
	log *zap.Logger

	certFile    string
	certWatcher *FileWatcher

	keyFile    string
	keyWatcher *FileWatcher

	certificate atomic.Pointer[tls.Certificate]

	reloadCounter atomic.Uint64
}

func NewCertificateReloader(log *zap.Logger, certFile string, keyFile string) (*CertificateReloader, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("could not load certificate / key: %w", err)
	}

	reloader := &CertificateReloader{
		log:      log,
		certFile: certFile,
		keyFile:  keyFile,
	}
	reloader.certificate.Store(&pair)
	reloader.certWatcher, err = NewFileWatcher(log, certFile, reloader.reload)
	if err != nil {
		return nil, err
	}
	reloader.keyWatcher, err = NewFileWatcher(log, keyFile, reloader.reload)
	if err != nil {
		reloader.certWatcher.Close() // nolint
		return nil, err
	}

	return reloader, nil
}

func (r *CertificateReloader) Close() {
	r.keyWatcher.Close()
	r.certWatcher.Close()
}

func (r *CertificateReloader) reload(filename string) {
	log := r.log.With(
		zap.String("certificate", r.certFile),
		zap.String("key", r.keyFile),
	)
	log.Debug("reloading certificate")
	pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		log.Error("could not load certificate / key",
			zap.Error(err),
		)
		return
	}

	r.certificate.Store(&pair)
	r.reloadCounter.Add(1)
}

func (r *CertificateReloader) getCertificate() (*tls.Certificate, error) {
	return r.certificate.Load(), nil
}

func (r *CertificateReloader) GetCertificate(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return r.getCertificate()
}

func (r *CertificateReloader) GetClientCertificate(i *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return r.getCertificate()
}

func (r *CertificateReloader) GetReloadCounter() uint64 {
	return r.reloadCounter.Load()
}

type CertPoolReloader struct {
	log         *zap.Logger
	certFile    string
	certWatcher *FileWatcher

	pool atomic.Pointer[x509.CertPool]

	reloadCounter atomic.Uint64
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

func NewCertPoolReloader(log *zap.Logger, certFile string) (*CertPoolReloader, error) {
	pool, err := loadCertPool(certFile)
	if err != nil {
		return nil, err
	}

	reloader := &CertPoolReloader{
		log:      log,
		certFile: certFile,
	}
	reloader.pool.Store(pool)
	reloader.certWatcher, err = NewFileWatcher(log, certFile, reloader.reload)
	if err != nil {
		return nil, err
	}

	return reloader, nil
}

func (r *CertPoolReloader) Close() {
	r.certWatcher.Close()
}

func (r *CertPoolReloader) reload(filename string) {
	log := r.log.With(
		zap.String("filename", r.certFile),
	)
	log.Debug("reloading certificate pool")
	pool, err := loadCertPool(r.certFile)
	if err != nil {
		log.Error("could not load certificate pool",
			zap.Error(err),
		)
		return
	}

	r.pool.Store(pool)
	r.reloadCounter.Add(1)
}

func (r *CertPoolReloader) GetCertPool() *x509.CertPool {
	return r.pool.Load()
}

func (r *CertPoolReloader) GetReloadCounter() uint64 {
	return r.reloadCounter.Load()
}
