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
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type reloadableCredentials struct {
	config *tls.Config

	loader *CertificateReloader
	pool   *CertPoolReloader
}

func (c *reloadableCredentials) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	// use local cfg to avoid clobbering ServerName if using multiple endpoints
	cfg := c.config.Clone()
	if c.loader != nil {
		cfg.GetClientCertificate = c.loader.GetClientCertificate
	}
	if c.pool != nil {
		cfg.RootCAs = c.pool.GetCertPool()
	}
	if cfg.ServerName == "" {
		serverName, _, err := net.SplitHostPort(authority)
		if err != nil {
			// If the authority had no host port or if the authority cannot be parsed, use it as-is.
			serverName = authority
		}
		cfg.ServerName = serverName
	}
	conn := tls.Client(rawConn, cfg)
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- conn.Handshake()
		close(errChannel)
	}()
	select {
	case err := <-errChannel:
		if err != nil {
			conn.Close()
			return nil, nil, err
		}
	case <-ctx.Done():
		conn.Close()
		return nil, nil, ctx.Err()
	}
	tlsInfo := credentials.TLSInfo{
		State: conn.ConnectionState(),
		CommonAuthInfo: credentials.CommonAuthInfo{
			SecurityLevel: credentials.PrivacyAndIntegrity,
		},
	}
	return WrapSyscallConn(rawConn, conn), tlsInfo, nil
}

func (c *reloadableCredentials) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	cfg := c.config.Clone()
	if c.loader != nil {
		cfg.GetCertificate = c.loader.GetCertificate
	}
	if c.pool != nil {
		cfg.ClientCAs = c.pool.GetCertPool()
	}

	conn := tls.Server(rawConn, cfg)
	if err := conn.Handshake(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	tlsInfo := credentials.TLSInfo{
		State: conn.ConnectionState(),
		CommonAuthInfo: credentials.CommonAuthInfo{
			SecurityLevel: credentials.PrivacyAndIntegrity,
		},
	}
	return WrapSyscallConn(rawConn, conn), tlsInfo, nil
}

func (c *reloadableCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{
		SecurityProtocol: "tls",
		SecurityVersion:  "1.2",
		ServerName:       c.config.ServerName,
	}
}

func (c *reloadableCredentials) Clone() credentials.TransportCredentials {
	return &reloadableCredentials{
		config: c.config.Clone(),
		pool:   c.pool,
	}
}

func (c *reloadableCredentials) OverrideServerName(serverName string) error {
	c.config.ServerName = serverName
	return nil
}

func NewReloadableCredentials(config *goconf.ConfigFile, server bool) (credentials.TransportCredentials, error) {
	var prefix string
	var caPrefix string
	if server {
		prefix = "server"
		caPrefix = "client"
	} else {
		prefix = "client"
		caPrefix = "server"
	}
	certificateFile, _ := config.GetString("grpc", prefix+"certificate")
	keyFile, _ := config.GetString("grpc", prefix+"key")
	caFile, _ := config.GetString("grpc", caPrefix+"ca")
	cfg := &tls.Config{
		NextProtos: []string{"h2"},
	}
	var loader *CertificateReloader
	var err error
	if certificateFile != "" && keyFile != "" {
		loader, err = NewCertificateReloader(certificateFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("invalid GRPC %s certificate / key in %s / %s: %w", prefix, certificateFile, keyFile, err)
		}
	}

	var pool *CertPoolReloader
	if caFile != "" {
		pool, err = NewCertPoolReloader(caFile)
		if err != nil {
			return nil, err
		}

		if server {
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	if loader == nil && pool == nil {
		if server {
			log.Printf("WARNING: No GRPC server certificate and/or key configured, running unencrypted")
		} else {
			log.Printf("WARNING: No GRPC CA configured, expecting unencrypted connections")
		}
		return insecure.NewCredentials(), nil
	}

	creds := &reloadableCredentials{
		config: cfg,
		loader: loader,
		pool:   pool,
	}
	return creds, nil
}
