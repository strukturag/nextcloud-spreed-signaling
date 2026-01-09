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
package grpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"path"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	status "google.golang.org/grpc/status"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

type CertificateReloadWaiter interface {
	WaitForCertificateReload(ctx context.Context, counter uint64) error
}

func (s *Server) WaitForCertificateReload(ctx context.Context, counter uint64) error {
	c, ok := s.creds.(CertificateReloadWaiter)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertificateReload(ctx, counter)
}

type CertPoolReloadWaiter interface {
	WaitForCertPoolReload(ctx context.Context, counter uint64) error
}

func (s *Server) WaitForCertPoolReload(ctx context.Context, counter uint64) error {
	c, ok := s.creds.(CertPoolReloadWaiter)
	if !ok {
		return errors.New("no reloadable credentials found")
	}

	return c.WaitForCertPoolReload(ctx, counter)
}

func NewServerForTestWithConfig(t *testing.T, config *goconf.ConfigFile) (server *Server, addr string) {
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	for port := 50000; port < 50100; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = NewServer(ctx, config, "0.0.0")
		if test.IsErrorAddressAlreadyInUse(err) {
			continue
		}

		require.NoError(t, err)
		break
	}

	require.NotNil(t, server, "could not find free port")

	// Don't match with own server id by default.
	server.SetServerId("dont-match")

	go func() {
		assert.NoError(t, server.Run(), "could not start GRPC server")
	}()

	t.Cleanup(func() {
		server.Close()
	})
	return server, addr
}

func NewServerForTest(t *testing.T) (server *Server, addr string) {
	config := goconf.NewConfigFile()
	return NewServerForTestWithConfig(t, config)
}

func TestServer_ReloadCerts(t *testing.T) {
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

	server, addr := NewServerForTestWithConfig(t, config)

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

func TestServer_ReloadCA(t *testing.T) {
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

	server, addr := NewServerForTestWithConfig(t, config)

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
	client1, err := NewClient(logger, addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg1)))
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
	client2, err := NewClient(logger, addr, nil, grpc.WithTransportCredentials(credentials.NewTLS(cfg2)))
	require.NoError(err)
	defer client2.Close() // nolint

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()

	// This will fail if the CA certificate has not been reloaded by the server.
	_, _, err = client2.GetServerId(ctx2)
	require.NoError(err)
}

func TestClients_Encryption(t *testing.T) { // nolint:paralleltest
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
		_, addr := NewServerForTestWithConfig(t, serverConfig)

		clientConfig := goconf.NewConfigFile()
		clientConfig.AddOption("grpc", "targets", addr)
		clientConfig.AddOption("grpc", "clientcertificate", clientCertFile)
		clientConfig.AddOption("grpc", "clientkey", clientPrivkeyFile)
		clientConfig.AddOption("grpc", "serverca", serverCertFile)
		clients, _ := NewClientsForTestWithConfig(t, clientConfig, nil, nil)

		ctx, cancel1 := context.WithTimeout(context.Background(), time.Second)
		defer cancel1()

		require.NoError(clients.WaitForInitialized(ctx))

		for _, client := range clients.GetClients() {
			_, _, err := client.GetServerId(ctx)
			require.NoError(err)
		}
	})
}

type disconnectInfo struct {
	sessionId     api.PublicSessionId
	roomSessionId api.RoomSessionId
	reason        string
}

type testServerHub struct {
	t       *testing.T
	backend *talk.Backend

	disconnected atomic.Pointer[disconnectInfo]
}

func newTestServerHub(t *testing.T) *testServerHub {
	t.Helper()
	logger := log.NewLoggerForTest(t)

	cfg := goconf.NewConfigFile()
	cfg.AddOption(testBackendId, "secret", "not-so-secret")
	cfg.AddOption(testBackendId, "sessionlimit", "10")
	backend, err := talk.NewBackendFromConfig(logger, testBackendId, cfg, "foo")
	require.NoError(t, err)

	u, err := url.Parse(testBackendUrl)
	require.NoError(t, err)
	backend.AddUrl(u)

	return &testServerHub{
		t:       t,
		backend: backend,
	}
}

const (
	testResumeId            = "test-resume-id"
	testSessionId           = "test-session-id"
	testRoomSessionId       = "test-room-session-id"
	testInternalSessionId   = "test-internal-session-id"
	testVirtualSessionId    = "test-virtual-session-id"
	testInternalInCallFlags = 2
	testVirtualInCallFlags  = 3
	testBackendId           = "backend-1"
	testBackendUrl          = "https://server.domain.invalid"
	testRoomId              = "test-room-id"
	testStreamType          = sfu.StreamTypeVideo
	testProxyUrl            = "https://proxy.domain.invalid"
	testIp                  = "1.2.3.4"
	testConnectToken        = "test-connection-token"
	testPublisherToken      = "test-publisher-token"
	testAddr                = "2.3.4.5"
	testCountry             = geoip.Country("DE")
	testAgent               = "test-agent"
)

var (
	testFeatures = []string{"bar", "foo"}
	testExpires  = time.Now().Add(time.Minute).Truncate(time.Millisecond)
	testMessage  = []byte("hello world!")
)

func (h *testServerHub) GetSessionIdByResumeId(resumeId api.PrivateSessionId) api.PublicSessionId {
	if resumeId == testResumeId {
		return testSessionId
	}

	return ""
}

func (h *testServerHub) GetSessionIdByRoomSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error) {
	if roomSessionId == testRoomSessionId {
		return testSessionId, nil
	}

	return "", ErrNoSuchRoomSession
}

func (h *testServerHub) IsSessionIdInCall(sessionId api.PublicSessionId, roomId string, backendUrl string) (bool, bool) {
	if roomId == testRoomId && backendUrl == testBackendUrl {
		return sessionId == testSessionId, true
	}

	return false, false
}

func (h *testServerHub) DisconnectSessionByRoomSessionId(sessionId api.PublicSessionId, roomSessionId api.RoomSessionId, reason string) {
	h.t.Helper()
	prev := h.disconnected.Swap(&disconnectInfo{
		sessionId:     sessionId,
		roomSessionId: roomSessionId,
		reason:        reason,
	})
	assert.Nil(h.t, prev, "duplicate call")
}

func (h *testServerHub) GetBackend(u *url.URL) *talk.Backend {
	if u == nil {
		// No compat backend.
		return nil
	} else if u.String() == testBackendUrl {
		return h.backend
	}

	return nil
}

func (h *testServerHub) GetInternalSessions(roomId string, backend *talk.Backend) ([]*InternalSessionData, []*VirtualSessionData, bool) {
	if roomId == testRoomId && backend == h.backend {
		return []*InternalSessionData{
				{
					SessionId: testInternalSessionId,
					InCall:    testInternalInCallFlags,
					Features:  testFeatures,
				},
			}, []*VirtualSessionData{
				{
					SessionId: testVirtualSessionId,
					InCall:    testVirtualInCallFlags,
				},
			}, true
	}

	return nil, nil, false
}

func (h *testServerHub) GetTransientEntries(roomId string, backend *talk.Backend) (api.TransientDataEntries, bool) {
	if roomId == testRoomId && backend == h.backend {
		return api.TransientDataEntries{
			"foo": api.NewTransientDataEntryWithExpires("bar", testExpires),
			"bar": api.NewTransientDataEntry(123, 0),
		}, true
	}

	return nil, false
}

func (h *testServerHub) GetPublisherIdForSessionId(ctx context.Context, sessionId api.PublicSessionId, streamType sfu.StreamType) (*GetPublisherIdReply, error) {
	if sessionId == testSessionId {
		if streamType != testStreamType {
			return nil, status.Error(codes.NotFound, "no such publisher")
		}

		return &GetPublisherIdReply{
			PublisherId:    testSessionId,
			ProxyUrl:       testProxyUrl,
			Ip:             testIp,
			ConnectToken:   testConnectToken,
			PublisherToken: testPublisherToken,
		}, nil
	}

	return nil, status.Error(codes.NotFound, "no such session")
}

func getMetadata(t *testing.T, md metadata.MD, key string) string {
	t.Helper()
	if values := md.Get(key); len(values) > 0 {
		return values[0]
	}

	return ""
}

func (h *testServerHub) ProxySession(request RpcSessions_ProxySessionServer) error {
	h.t.Helper()
	if md, found := metadata.FromIncomingContext(request.Context()); assert.True(h.t, found) {
		if getMetadata(h.t, md, "sessionId") != testSessionId {
			return status.Error(codes.InvalidArgument, "unknown session id")
		}

		assert.Equal(h.t, testSessionId, getMetadata(h.t, md, "sessionId"))
		assert.Equal(h.t, testAddr, getMetadata(h.t, md, "remoteAddr"))
		assert.EqualValues(h.t, testCountry, getMetadata(h.t, md, "country"))
		assert.Equal(h.t, testAgent, getMetadata(h.t, md, "userAgent"))
	}

	assert.NoError(h.t, request.Send(&ServerSessionMessage{
		Message: testMessage,
	}))

	return nil
}

func TestServer_GetSessionIdByResumeId(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		reply, err := client.LookupResumeId(ctx, "")
		assert.ErrorIs(err, ErrNoSuchResumeId, "expected unknown resume id, got %s", reply.GetSessionId())

		reply, err = client.LookupResumeId(ctx, testResumeId+"1")
		assert.ErrorIs(err, ErrNoSuchResumeId, "expected unknown resume id, got %s", reply.GetSessionId())

		if reply, err := client.LookupResumeId(ctx, testResumeId); assert.NoError(err) {
			assert.Equal(testSessionId, reply.SessionId)
		}
	}
}

func TestServer_LookupSessionId(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		sessionId, err := client.LookupSessionId(ctx, "", "")
		assert.ErrorIs(err, ErrNoSuchRoomSession, "expected unknown room session id, got %s", sessionId)

		sessionId, err = client.LookupSessionId(ctx, testRoomSessionId+"1", "")
		assert.ErrorIs(err, ErrNoSuchRoomSession, "expected unknown room session id, got %s", sessionId)

		if sessionId, err := client.LookupSessionId(ctx, testRoomSessionId, "test-reason"); assert.NoError(err) {
			assert.EqualValues(testSessionId, sessionId)
		}
	}

	if disconnected := hub.disconnected.Load(); assert.NotNil(disconnected, "session was not disconnected") {
		assert.EqualValues(testSessionId, disconnected.sessionId)
		assert.EqualValues(testRoomSessionId, disconnected.roomSessionId)
		assert.Equal("test-reason", disconnected.reason)
	}
}

func TestServer_IsSessionInCall(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		if inCall, err := client.IsSessionInCall(ctx, testSessionId, testRoomId+"1", testBackendUrl); assert.NoError(err) {
			assert.False(inCall)
		}
		if inCall, err := client.IsSessionInCall(ctx, testSessionId, testRoomId, testBackendUrl+"1"); assert.NoError(err) {
			assert.False(inCall)
		}

		if inCall, err := client.IsSessionInCall(ctx, testSessionId+"1", testRoomId, testBackendUrl); assert.NoError(err) {
			assert.False(inCall, "should not be in call")
		}
		if inCall, err := client.IsSessionInCall(ctx, testSessionId, testRoomId, testBackendUrl); assert.NoError(err) {
			assert.True(inCall, "should be in call")
		}
	}
}

func TestServer_GetInternalSessions(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		if internal, virtual, err := client.GetInternalSessions(ctx, testRoomId+"1", []string{testBackendUrl}); assert.NoError(err) {
			assert.Empty(internal)
			assert.Empty(virtual)
		}
		if internal, virtual, err := client.GetInternalSessions(ctx, testRoomId, nil); assert.NoError(err) {
			assert.Empty(internal)
			assert.Empty(virtual)
		}
		if internal, virtual, err := client.GetInternalSessions(ctx, testRoomId, []string{testBackendUrl}); assert.NoError(err) {
			if assert.Len(internal, 1) && assert.NotNil(internal[testInternalSessionId], "did not find %s in %+v", testInternalSessionId, internal) {
				assert.Equal(testInternalSessionId, internal[testInternalSessionId].SessionId)
				assert.EqualValues(testInternalInCallFlags, internal[testInternalSessionId].InCall)
				assert.Equal(testFeatures, internal[testInternalSessionId].Features)
			}
			if assert.Len(virtual, 1) && assert.NotNil(virtual[testVirtualSessionId], "did not find %s in %+v", testVirtualSessionId, virtual) {
				assert.Equal(testVirtualSessionId, virtual[testVirtualSessionId].SessionId)
				assert.EqualValues(testVirtualInCallFlags, virtual[testVirtualSessionId].InCall)
			}
		}
	}
}

func TestServer_GetPublisherId(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		if publisherId, proxyUrl, ip, connToken, publisherToken, err := client.GetPublisherId(ctx, testSessionId, sfu.StreamTypeVideo); assert.NoError(err) {
			assert.EqualValues(testSessionId, publisherId)
			assert.Equal(testProxyUrl, proxyUrl)
			assert.True(net.ParseIP(testIp).Equal(ip), "expected IP %s, got %s", testIp, ip.String())
			assert.Equal(testConnectToken, connToken)
			assert.Equal(testPublisherToken, publisherToken)
		}
	}
}

func TestServer_GetTransientData(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		if entries, err := client.GetTransientData(ctx, testRoomId+"1", hub.backend); assert.NoError(err) {
			assert.Empty(entries)
		}
		if entries, err := client.GetTransientData(ctx, testRoomId, hub.backend); assert.NoError(err) && assert.Len(entries, 2) {
			if e := entries["foo"]; assert.NotNil(e, "did not find foo in %+v", entries) {
				assert.Equal("bar", e.Value)
				assert.Equal(testExpires, e.Expires)
			}

			if e := entries["bar"]; assert.NotNil(e, "did not find bar in %+v", entries) {
				assert.EqualValues(123, e.Value)
				assert.True(e.Expires.IsZero(), "should have no expiration, got %s", e.Expires)
			}
		}
	}
}

type testReceiver struct {
	t        *testing.T
	received atomic.Bool
	closed   chan struct{}
}

func (r *testReceiver) RemoteAddr() string {
	return testAddr
}

func (r *testReceiver) Country() geoip.Country {
	return testCountry
}

func (r *testReceiver) UserAgent() string {
	return testAgent
}

func (r *testReceiver) OnProxyMessage(message *ServerSessionMessage) error {
	assert.Equal(r.t, testMessage, message.Message)
	assert.False(r.t, r.received.Swap(true), "received additional message %v", message)
	return nil
}

func (r *testReceiver) OnProxyClose(err error) {
	if err != nil {
		if s := status.Convert(err); assert.NotNil(r.t, s, "expected status, got %+v", err) {
			assert.Equal(r.t, codes.InvalidArgument, s.Code())
			assert.Equal(r.t, "unknown session id", s.Message())
		}
	}

	close(r.closed)
}

func TestServer_ProxySession(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		receiver := &testReceiver{
			t:      t,
			closed: make(chan struct{}),
		}
		if proxy, err := client.ProxySession(ctx, testSessionId, receiver); assert.NoError(err) {
			t.Cleanup(func() {
				assert.NoError(proxy.Close())
			})

			assert.NotNil(proxy)
			<-receiver.closed
			assert.True(receiver.received.Load(), "should have received message")
		}
	}
}

func TestServer_ProxySessionError(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		receiver := &testReceiver{
			t:      t,
			closed: make(chan struct{}),
		}
		if proxy, err := client.ProxySession(ctx, testSessionId+"1", receiver); assert.NoError(err) {
			t.Cleanup(func() {
				assert.NoError(proxy.Close())
			})

			assert.NotNil(proxy)
			<-receiver.closed
		}
	}
}

type testSession struct{}

func (s *testSession) PublicId() api.PublicSessionId {
	return testSessionId
}

func (s *testSession) ClientType() api.ClientType {
	return api.HelloClientTypeClient
}

func TestServer_GetSessionCount(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	hub := newTestServerHub(t)

	server, addr := NewServerForTest(t)
	server.SetHub(hub)
	clients, _ := NewClientsForTest(t, addr, nil)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	require.NoError(clients.WaitForInitialized(ctx))

	for _, client := range clients.GetClients() {
		if count, err := client.GetSessionCount(ctx, testBackendUrl+"1"); assert.NoError(err) {
			assert.EqualValues(0, count)
		}
		if count, err := client.GetSessionCount(ctx, testBackendUrl); assert.NoError(err) {
			assert.EqualValues(0, count)
		}
		assert.NoError(hub.backend.AddSession(&testSession{}))
		if count, err := client.GetSessionCount(ctx, testBackendUrl); assert.NoError(err) {
			assert.EqualValues(1, count)
		}
		hub.backend.RemoveSession(&testSession{})
		if count, err := client.GetSessionCount(ctx, testBackendUrl); assert.NoError(err) {
			assert.EqualValues(0, count)
		}
	}
}
