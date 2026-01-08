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
package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	rpc "github.com/strukturag/nextcloud-spreed-signaling/grpc"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

type CertificateReloadWaiter interface {
	WaitForCertificateReload(ctx context.Context, counter uint64) error
}

func (s *GrpcServer) WaitForCertificateReload(ctx context.Context, counter uint64) error {
	c, ok := s.creds.(CertificateReloadWaiter)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertificateReload(ctx, counter)
}

type CertPoolReloadWaiter interface {
	WaitForCertPoolReload(ctx context.Context, counter uint64) error
}

func (s *GrpcServer) WaitForCertPoolReload(ctx context.Context, counter uint64) error {
	c, ok := s.creds.(CertPoolReloadWaiter)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertPoolReload(ctx, counter)
}

func NewGrpcServerForTestWithConfig(t *testing.T, config *goconf.ConfigFile) (server *GrpcServer, addr string) {
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	for port := 50000; port < 50100; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = NewGrpcServer(ctx, config, "0.0.0")
		if test.IsErrorAddressAlreadyInUse(err) {
			continue
		}

		require.NoError(t, err)
		break
	}

	require.NotNil(t, server, "could not find free port")

	// Don't match with own server id by default.
	server.serverId = "dont-match"

	go func() {
		assert.NoError(t, server.Run(), "could not start GRPC server")
	}()

	t.Cleanup(func() {
		server.Close()
	})
	return server, addr
}

func NewGrpcServerForTest(t *testing.T) (server *GrpcServer, addr string) {
	config := goconf.NewConfigFile()
	return NewGrpcServerForTestWithConfig(t, config)
}

func Test_GrpcServer_ReloadCerts(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)

	org1 := "Testing certificate"
	cert1 := internal.GenerateSelfSignedCertificateForTesting(t, org1, key)

	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	certFile := path.Join(dir, "cert.pem")
	require.NoError(internal.WritePrivateKey(key, privkeyFile))
	require.NoError(internal.WritePublicKey(&key.PublicKey, pubkeyFile))
	require.NoError(internal.WriteCertificate(cert1, certFile))

	config := goconf.NewConfigFile()
	config.AddOption("grpc", "servercertificate", certFile)
	config.AddOption("grpc", "serverkey", privkeyFile)

	server, addr := NewGrpcServerForTestWithConfig(t, config)

	cp1 := x509.NewCertPool()
	cp1.AddCert(cert1)

	cfg1 := &tls.Config{
		RootCAs: cp1,
	}
	conn1, err := tls.Dial("tcp", addr, cfg1)
	require.NoError(err)
	defer conn1.Close() // nolint
	state1 := conn1.ConnectionState()
	if certs := state1.PeerCertificates; assert.NotEmpty(certs) {
		if assert.NotEmpty(certs[0].Subject.Organization) {
			assert.Equal(org1, certs[0].Subject.Organization[0])
		}
	}

	org2 := "Updated certificate"
	cert2 := internal.GenerateSelfSignedCertificateForTesting(t, org2, key)
	internal.ReplaceCertificate(t, certFile, cert2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(server.WaitForCertificateReload(ctx, 0))

	cp2 := x509.NewCertPool()
	cp2.AddCert(cert2)

	cfg2 := &tls.Config{
		RootCAs: cp2,
	}
	conn2, err := tls.Dial("tcp", addr, cfg2)
	require.NoError(err)
	defer conn2.Close() // nolint
	state2 := conn2.ConnectionState()
	if certs := state2.PeerCertificates; assert.NotEmpty(certs) {
		if assert.NotEmpty(certs[0].Subject.Organization) {
			assert.Equal(org2, certs[0].Subject.Organization[0])
		}
	}
}

func Test_GrpcServer_ReloadCA(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	require := require.New(t)
	serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	clientKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)

	serverCert := internal.GenerateSelfSignedCertificateForTesting(t, "Server cert", serverKey)
	org1 := "Testing client"
	clientCert1 := internal.GenerateSelfSignedCertificateForTesting(t, org1, clientKey)

	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	certFile := path.Join(dir, "cert.pem")
	caFile := path.Join(dir, "ca.pem")
	require.NoError(internal.WritePrivateKey(serverKey, privkeyFile))
	require.NoError(internal.WritePublicKey(&serverKey.PublicKey, pubkeyFile))
	require.NoError(internal.WriteCertificate(serverCert, certFile))
	require.NoError(internal.WriteCertificate(clientCert1, caFile))

	config := goconf.NewConfigFile()
	config.AddOption("grpc", "servercertificate", certFile)
	config.AddOption("grpc", "serverkey", privkeyFile)
	config.AddOption("grpc", "clientca", caFile)

	server, addr := NewGrpcServerForTestWithConfig(t, config)

	pool := x509.NewCertPool()
	pool.AddCert(serverCert)

	pair1, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCert1.Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	}))
	require.NoError(err)

	cfg1 := &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair1},
	}
	client1, err := rpc.NewClient(logger, addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg1)))
	require.NoError(err)
	defer client1.Close() // nolint

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second)
	defer cancel1()

	_, _, err = client1.GetServerId(ctx1)
	require.NoError(err)

	org2 := "Updated client"
	clientCert2 := internal.GenerateSelfSignedCertificateForTesting(t, org2, clientKey)
	internal.ReplaceCertificate(t, caFile, clientCert2)

	require.NoError(server.WaitForCertPoolReload(ctx1, 0))

	pair2, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCert2.Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	}))
	require.NoError(err)

	cfg2 := &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair2},
	}
	client2, err := rpc.NewClient(logger, addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg2)))
	require.NoError(err)
	defer client2.Close() // nolint

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()

	// This will fail if the CA certificate has not been reloaded by the server.
	_, _, err = client2.GetServerId(ctx2)
	require.NoError(err)
}

func Test_GrpcClients_Encryption(t *testing.T) { // nolint:paralleltest
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		require := require.New(t)
		serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)
		clientKey, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(err)

		serverCert := internal.GenerateSelfSignedCertificateForTesting(t, "Server cert", serverKey)
		clientCert := internal.GenerateSelfSignedCertificateForTesting(t, "Testing client", clientKey)

		dir := t.TempDir()
		serverPrivkeyFile := path.Join(dir, "server-privkey.pem")
		serverPubkeyFile := path.Join(dir, "server-pubkey.pem")
		serverCertFile := path.Join(dir, "server-cert.pem")
		require.NoError(internal.WritePrivateKey(serverKey, serverPrivkeyFile))
		require.NoError(internal.WritePublicKey(&serverKey.PublicKey, serverPubkeyFile))
		require.NoError(internal.WriteCertificate(serverCert, serverCertFile))
		clientPrivkeyFile := path.Join(dir, "client-privkey.pem")
		clientPubkeyFile := path.Join(dir, "client-pubkey.pem")
		clientCertFile := path.Join(dir, "client-cert.pem")
		require.NoError(internal.WritePrivateKey(clientKey, clientPrivkeyFile))
		require.NoError(internal.WritePublicKey(&clientKey.PublicKey, clientPubkeyFile))
		require.NoError(internal.WriteCertificate(clientCert, clientCertFile))

		serverConfig := goconf.NewConfigFile()
		serverConfig.AddOption("grpc", "servercertificate", serverCertFile)
		serverConfig.AddOption("grpc", "serverkey", serverPrivkeyFile)
		serverConfig.AddOption("grpc", "clientca", clientCertFile)
		_, addr := NewGrpcServerForTestWithConfig(t, serverConfig)

		clientConfig := goconf.NewConfigFile()
		clientConfig.AddOption("grpc", "targets", addr)
		clientConfig.AddOption("grpc", "clientcertificate", clientCertFile)
		clientConfig.AddOption("grpc", "clientkey", clientPrivkeyFile)
		clientConfig.AddOption("grpc", "serverca", serverCertFile)
		clients, _ := grpctest.NewClientsForTestWithConfig(t, clientConfig, nil, nil)

		ctx, cancel1 := context.WithTimeout(context.Background(), time.Second)
		defer cancel1()

		require.NoError(clients.WaitForInitialized(ctx))

		for _, client := range clients.GetClients() {
			_, _, err := client.GetServerId(ctx)
			require.NoError(err)
		}
	})
}
