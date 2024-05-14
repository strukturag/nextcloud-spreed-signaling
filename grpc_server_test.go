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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func (s *GrpcServer) WaitForCertificateReload(ctx context.Context) error {
	c, ok := s.creds.(*reloadableCredentials)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertificateReload(ctx)
}

func (s *GrpcServer) WaitForCertPoolReload(ctx context.Context) error {
	c, ok := s.creds.(*reloadableCredentials)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertPoolReload(ctx)
}

func NewGrpcServerForTestWithConfig(t *testing.T, config *goconf.ConfigFile) (server *GrpcServer, addr string) {
	for port := 50000; port < 50100; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = NewGrpcServer(config)
		if isErrorAddressAlreadyInUse(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		break
	}

	if server == nil {
		t.Fatal("could not find free port")
	}

	// Don't match with own server id by default.
	server.serverId = "dont-match"

	go func() {
		if err := server.Run(); err != nil {
			t.Errorf("could not start GRPC server: %s", err)
		}
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
	CatchLogForTest(t)
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	org1 := "Testing certificate"
	cert1 := GenerateSelfSignedCertificateForTesting(t, 1024, org1, key)

	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	certFile := path.Join(dir, "cert.pem")
	WritePrivateKey(key, privkeyFile)          // nolint
	WritePublicKey(&key.PublicKey, pubkeyFile) // nolint
	os.WriteFile(certFile, cert1, 0755)        // nolint

	config := goconf.NewConfigFile()
	config.AddOption("grpc", "servercertificate", certFile)
	config.AddOption("grpc", "serverkey", privkeyFile)

	UpdateCertificateCheckIntervalForTest(t, 0)
	server, addr := NewGrpcServerForTestWithConfig(t, config)

	cp1 := x509.NewCertPool()
	if !cp1.AppendCertsFromPEM(cert1) {
		t.Fatalf("could not add certificate")
	}

	cfg1 := &tls.Config{
		RootCAs: cp1,
	}
	conn1, err := tls.Dial("tcp", addr, cfg1)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close() // nolint
	state1 := conn1.ConnectionState()
	if certs := state1.PeerCertificates; len(certs) == 0 {
		t.Errorf("expected certificates, got %+v", state1)
	} else if len(certs[0].Subject.Organization) == 0 {
		t.Errorf("expected organization, got %s", certs[0].Subject)
	} else if certs[0].Subject.Organization[0] != org1 {
		t.Errorf("expected organization %s, got %s", org1, certs[0].Subject)
	}

	org2 := "Updated certificate"
	cert2 := GenerateSelfSignedCertificateForTesting(t, 1024, org2, key)
	replaceFile(t, certFile, cert2, 0755)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := server.WaitForCertificateReload(ctx); err != nil {
		t.Fatal(err)
	}

	cp2 := x509.NewCertPool()
	if !cp2.AppendCertsFromPEM(cert2) {
		t.Fatalf("could not add certificate")
	}

	cfg2 := &tls.Config{
		RootCAs: cp2,
	}
	conn2, err := tls.Dial("tcp", addr, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close() // nolint
	state2 := conn2.ConnectionState()
	if certs := state2.PeerCertificates; len(certs) == 0 {
		t.Errorf("expected certificates, got %+v", state2)
	} else if len(certs[0].Subject.Organization) == 0 {
		t.Errorf("expected organization, got %s", certs[0].Subject)
	} else if certs[0].Subject.Organization[0] != org2 {
		t.Errorf("expected organization %s, got %s", org2, certs[0].Subject)
	}
}

func Test_GrpcServer_ReloadCA(t *testing.T) {
	CatchLogForTest(t)
	serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	serverCert := GenerateSelfSignedCertificateForTesting(t, 1024, "Server cert", serverKey)
	org1 := "Testing client"
	clientCert1 := GenerateSelfSignedCertificateForTesting(t, 1024, org1, clientKey)

	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	certFile := path.Join(dir, "cert.pem")
	caFile := path.Join(dir, "ca.pem")
	WritePrivateKey(serverKey, privkeyFile)          // nolint
	WritePublicKey(&serverKey.PublicKey, pubkeyFile) // nolint
	os.WriteFile(certFile, serverCert, 0755)         // nolint
	os.WriteFile(caFile, clientCert1, 0755)          // nolint

	config := goconf.NewConfigFile()
	config.AddOption("grpc", "servercertificate", certFile)
	config.AddOption("grpc", "serverkey", privkeyFile)
	config.AddOption("grpc", "clientca", caFile)

	UpdateCertificateCheckIntervalForTest(t, 0)
	server, addr := NewGrpcServerForTestWithConfig(t, config)

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(serverCert) {
		t.Fatalf("could not add certificate")
	}

	pair1, err := tls.X509KeyPair(clientCert1, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	}))
	if err != nil {
		t.Fatal(err)
	}

	cfg1 := &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair1},
	}
	client1, err := NewGrpcClient(addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg1)))
	if err != nil {
		t.Fatal(err)
	}
	defer client1.Close() // nolint

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second)
	defer cancel1()

	if _, err := client1.GetServerId(ctx1); err != nil {
		t.Fatal(err)
	}

	org2 := "Updated client"
	clientCert2 := GenerateSelfSignedCertificateForTesting(t, 1024, org2, clientKey)
	replaceFile(t, caFile, clientCert2, 0755)

	if err := server.WaitForCertPoolReload(ctx1); err != nil {
		t.Fatal(err)
	}

	pair2, err := tls.X509KeyPair(clientCert2, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	}))
	if err != nil {
		t.Fatal(err)
	}

	cfg2 := &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair2},
	}
	client2, err := NewGrpcClient(addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg2)))
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close() // nolint

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()

	// This will fail if the CA certificate has not been reloaded by the server.
	if _, err := client2.GetServerId(ctx2); err != nil {
		t.Fatal(err)
	}
}
