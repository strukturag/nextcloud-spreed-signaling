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
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/proxy"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

const (
	KeypairSizeForTest = 2048
	TokenIdForTest     = "foo"

	testTimeout = 10 * time.Second
)

func getWebsocketUrl(url string) string {
	if url, found := strings.CutPrefix(url, "http://"); found {
		return "ws://" + url + "/proxy"
	} else if url, found := strings.CutPrefix(url, "https://"); found {
		return "wss://" + url + "/proxy"
	} else {
		panic("Unsupported URL: " + url)
	}
}

func WaitForProxyServer(ctx context.Context, t *testing.T, proxy *ProxyServer) {
	// Wait for any channel messages to be processed.
	time.Sleep(10 * time.Millisecond)
	proxy.Stop()
	for {
		proxy.clientsLock.RLock()
		clients := len(proxy.clients)
		proxy.clientsLock.RUnlock()
		proxy.sessionsLock.RLock()
		sessions := len(proxy.sessions)
		proxy.sessionsLock.RUnlock()
		proxy.remoteConnectionsLock.Lock()
		remoteConnections := len(proxy.remoteConnections)
		proxy.remoteConnectionsLock.Unlock()
		if clients == 0 &&
			sessions == 0 &&
			remoteConnections == 0 {
			break
		}

		select {
		case <-ctx.Done():
			proxy.clientsLock.Lock()
			proxy.remoteConnectionsLock.Lock()
			assert.Fail(t, "Error waiting for proxy to terminate", "clients %+v / sessions %+v / remoteConnections %+v: %+v", clients, sessions, remoteConnections, ctx.Err())
			proxy.remoteConnectionsLock.Unlock()
			proxy.clientsLock.Unlock()
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func newProxyServerForTest(t *testing.T) (*ProxyServer, *rsa.PrivateKey, *httptest.Server) {
	require := require.New(t)
	tempdir := t.TempDir()
	var proxy *ProxyServer
	t.Cleanup(func() {
		if proxy != nil {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			WaitForProxyServer(ctx, t, proxy)
		}
	})

	r := mux.NewRouter()
	key, err := rsa.GenerateKey(rand.Reader, KeypairSizeForTest)
	require.NoError(err)
	priv := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	privkey, err := os.CreateTemp(tempdir, "privkey*.pem")
	require.NoError(err)
	require.NoError(pem.Encode(privkey, priv))
	require.NoError(privkey.Close())

	pubData, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(err)
	pub := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubData,
	}
	pubkey, err := os.CreateTemp(tempdir, "pubkey*.pem")
	require.NoError(err)
	require.NoError(pem.Encode(pubkey, pub))
	require.NoError(pubkey.Close())

	config := goconf.NewConfigFile()
	config.AddOption("tokens", TokenIdForTest, pubkey.Name())

	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	proxy, err = NewProxyServer(ctx, r, "0.0", config)
	require.NoError(err)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	return proxy, key, server
}

func TestTokenValid(t *testing.T) {
	t.Parallel()
	proxyServer, key, _ := newProxyServerForTest(t)

	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &proxy.HelloClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxyServer.NewSession(hello); assert.NoError(t, err) {
		defer proxyServer.DeleteSession(session.Sid())
	}
}

func TestTokenNotSigned(t *testing.T) {
	t.Parallel()
	proxyServer, _, _ := newProxyServerForTest(t)

	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	hello := &proxy.HelloClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxyServer.NewSession(hello); !assert.ErrorIs(t, err, TokenAuthFailed) {
		if session != nil {
			defer proxyServer.DeleteSession(session.Sid())
		}
	}
}

func TestTokenUnknown(t *testing.T) {
	t.Parallel()
	proxyServer, key, _ := newProxyServerForTest(t)

	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest + "2",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &proxy.HelloClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxyServer.NewSession(hello); !assert.ErrorIs(t, err, TokenAuthFailed) {
		if session != nil {
			defer proxyServer.DeleteSession(session.Sid())
		}
	}
}

func TestTokenInFuture(t *testing.T) {
	t.Parallel()
	proxyServer, key, _ := newProxyServerForTest(t)

	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &proxy.HelloClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxyServer.NewSession(hello); !assert.ErrorIs(t, err, TokenNotValidYet) {
		if session != nil {
			defer proxyServer.DeleteSession(session.Sid())
		}
	}
}

func TestTokenExpired(t *testing.T) {
	t.Parallel()
	proxyServer, key, _ := newProxyServerForTest(t)

	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge * 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &proxy.HelloClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxyServer.NewSession(hello); !assert.ErrorIs(t, err, TokenExpired) {
		if session != nil {
			defer proxyServer.DeleteSession(session.Sid())
		}
	}
}

func TestPublicIPs(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	public := []string{
		"8.8.8.8",
		"172.15.1.2",
		"172.32.1.2",
		"192.167.0.1",
		"192.169.0.1",
	}
	private := []string{
		"127.0.0.1",
		"10.1.2.3",
		"172.16.1.2",
		"172.31.1.2",
		"192.168.0.1",
		"192.168.254.254",
	}
	for _, s := range public {
		ip := net.ParseIP(s)
		if assert.NotEmpty(ip, "invalid IP: %s", s) {
			assert.True(IsPublicIP(ip), "should be public IP: %s", s)
		}
	}

	for _, s := range private {
		ip := net.ParseIP(s)
		if assert.NotEmpty(ip, "invalid IP: %s", s) {
			assert.False(IsPublicIP(ip), "should be private IP: %s", s)
		}
	}
}

func TestWebsocketFeatures(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	_, _, server := newProxyServerForTest(t)

	conn, response, err := websocket.DefaultDialer.DialContext(context.Background(), getWebsocketUrl(server.URL), nil)
	require.NoError(t, err)
	defer conn.Close() // nolint

	if server := response.Header.Get("Server"); !strings.HasPrefix(server, "nextcloud-spreed-signaling-proxy/") {
		assert.Fail("expected valid server header", "received \"%s\"", server)
	}
	features := response.Header.Get("X-Spreed-Signaling-Features")
	featuresList := make(map[string]bool)
	for f := range internal.SplitEntries(features, ",") {
		if _, found := featuresList[f]; found {
			assert.Fail("duplicate feature", "id \"%s\" in \"%s\"", f, features)
		}
		featuresList[f] = true
	}
	assert.NotEmpty(featuresList, "expected valid features header, got \"%s\"", features)
	if _, found := featuresList["remote-streams"]; !found {
		assert.Fail("expected feature \"remote-streams\"", "received \"%s\"", features)
	}

	assert.NoError(conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{}))
}

func TestProxyCreateSession(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	_, key, server := newProxyServerForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)
}

type TestMCU struct {
	t *testing.T
}

func (m *TestMCU) GetBandwidthLimits() (api.Bandwidth, api.Bandwidth) {
	return 0, 0
}

func (m *TestMCU) Start(ctx context.Context) error {
	return nil
}

func (m *TestMCU) Stop() {
}

func (m *TestMCU) Reload(config *goconf.ConfigFile) {
}

func (m *TestMCU) SetOnConnected(f func()) {
}

func (m *TestMCU) SetOnDisconnected(f func()) {
}

func (m *TestMCU) GetStats() any {
	return nil
}

func (m *TestMCU) GetServerInfoSfu() *talk.BackendServerInfoSfu {
	return nil
}

func (m *TestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	return nil, errors.New("not implemented")
}

func (m *TestMCU) NewSubscriber(ctx context.Context, listener sfu.Listener, publisher api.PublicSessionId, streamType sfu.StreamType, initiator sfu.Initiator) (sfu.Subscriber, error) {
	return nil, errors.New("not implemented")
}

type TestMCUPublisher struct {
	id         api.PublicSessionId
	sid        string
	streamType sfu.StreamType
}

func (p *TestMCUPublisher) Id() string {
	return string(p.id)
}

func (p *TestMCUPublisher) PublisherId() api.PublicSessionId {
	return p.id
}

func (p *TestMCUPublisher) Sid() string {
	return p.sid
}

func (p *TestMCUPublisher) StreamType() sfu.StreamType {
	return p.streamType
}

func (p *TestMCUPublisher) MaxBitrate() api.Bandwidth {
	return 0
}

func (p *TestMCUPublisher) Close(ctx context.Context) {
}

func (p *TestMCUPublisher) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	callback(errors.New("not implemented"), nil)
}

func (p *TestMCUPublisher) HasMedia(sfu.MediaType) bool {
	return false
}

func (p *TestMCUPublisher) SetMedia(mediaTypes sfu.MediaType) {
}

func (p *TestMCUPublisher) GetStreams(ctx context.Context) ([]sfu.PublisherStream, error) {
	return nil, errors.New("not implemented")
}

func (p *TestMCUPublisher) PublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("not implemented")
}

func (p *TestMCUPublisher) UnpublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("not implemented")
}

type PublisherTestMCU struct {
	TestMCU

	publisher atomic.Pointer[TestPublisherWithBandwidth]
}

type TestPublisherWithBandwidth struct {
	TestMCUPublisher

	t            *testing.T
	bandwidth    *sfu.ClientBandwidthInfo
	bandwidthSet atomic.Bool
}

func (p *TestPublisherWithBandwidth) Bandwidth() *sfu.ClientBandwidthInfo {
	return p.bandwidth
}

func (p *TestPublisherWithBandwidth) SetBandwidth(ctx context.Context, bandwidth api.Bandwidth) error {
	assert.EqualValues(p.t, 20000, bandwidth)
	p.bandwidthSet.Store(true)
	return nil
}

func (m *PublisherTestMCU) GetPublisher() *TestPublisherWithBandwidth {
	return m.publisher.Load()
}

func (m *PublisherTestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	publisher := &TestPublisherWithBandwidth{
		TestMCUPublisher: TestMCUPublisher{
			id:         id,
			sid:        sid,
			streamType: streamType,
		},

		t: m.t,
		bandwidth: &sfu.ClientBandwidthInfo{
			Sent:     api.BandwidthFromBytes(1000),
			Received: api.BandwidthFromBytes(2000),
		},
	}
	if !m.publisher.CompareAndSwap(nil, publisher) {
		return nil, errors.New("only one publisher supported")
	}

	return publisher, nil
}

func NewPublisherTestMCU(t *testing.T) *PublisherTestMCU {
	return &PublisherTestMCU{
		TestMCU: TestMCU{
			t: t,
		},
	}
}

func TestProxyPublisherBandwidth(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	proxyServer.maxIncoming.Store(api.BandwidthFromBytes(10000))
	proxyServer.maxOutgoing.Store(api.BandwidthFromBytes(10000))

	mcu := NewPublisherTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "create-publisher",
			StreamType: sfu.StreamTypeVideo,
		},
	}))

	var publisherId string
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			assert.NotEmpty(message.Command.Id)
			publisherId = message.Command.Id
		}
	}
	require.NotEmpty(publisherId)
	publisher := mcu.GetPublisher()
	require.NotNil(publisher)

	proxyServer.updateLoad()

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		if err := checkMessageType(message, "event"); assert.NoError(err) && assert.Equal("update-load", message.Event.Type) {
			assert.EqualValues(1, message.Event.Load)
			if bw := message.Event.Bandwidth; assert.NotNil(bw) {
				if assert.NotNil(bw.Incoming) {
					assert.InEpsilon(20, *bw.Incoming, 0.0001)
				}
				if assert.NotNil(bw.Outgoing) {
					assert.InEpsilon(10, *bw.Outgoing, 0.0001)
				}
			}
			if assert.Len(message.Event.ClientBandwidths, 1) {
				if bw := message.Event.ClientBandwidths; assert.NotNil(bw[publisherId], "expected %s, got %+v", bw) {
					assert.EqualValues(8000, bw[publisherId].Sent)
					assert.EqualValues(16000, bw[publisherId].Received)
				}
			}
		}
	}

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:      "update-bandwidth",
			ClientId:  publisherId,
			Bandwidth: api.BandwidthFromBits(20000),
		},
	}))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			assert.Equal(publisherId, message.Command.Id)
		}
	}

	assert.True(publisher.bandwidthSet.Load(), "should have set bandwidth")
}

type HangingTestMCU struct {
	TestMCU
	ctx       context.Context
	creating  chan struct{}
	created   chan struct{}
	cancelled atomic.Bool
}

func NewHangingTestMCU(t *testing.T) *HangingTestMCU {
	ctx, closeFunc := context.WithCancel(context.Background())
	t.Cleanup(func() {
		closeFunc()
	})

	return &HangingTestMCU{
		TestMCU: TestMCU{
			t: t,
		},
		ctx:      ctx,
		creating: make(chan struct{}),
		created:  make(chan struct{}),
	}
}

func (m *HangingTestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	ctx2, cancel := context.WithTimeout(m.ctx, testTimeout*2)
	defer cancel()

	m.creating <- struct{}{}
	defer func() {
		m.created <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		m.cancelled.Store(true)
		return nil, ctx.Err()
	case <-ctx2.Done():
		return nil, errors.New("Should have been cancelled before")
	}
}

func (m *HangingTestMCU) NewSubscriber(ctx context.Context, listener sfu.Listener, publisher api.PublicSessionId, streamType sfu.StreamType, initiator sfu.Initiator) (sfu.Subscriber, error) {
	ctx2, cancel := context.WithTimeout(m.ctx, testTimeout*2)
	defer cancel()

	m.creating <- struct{}{}
	defer func() {
		m.created <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		m.cancelled.Store(true)
		return nil, ctx.Err()
	case <-ctx2.Done():
		return nil, errors.New("Should have been cancelled before")
	}
}

func TestProxyCancelOnClose(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewHangingTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "create-publisher",
			StreamType: sfu.StreamTypeVideo,
		},
	}))

	// Simulate expired session while request is still being processed.
	go func() {
		<-mcu.creating
		if session := proxyServer.GetSession(1); assert.NotNil(session) {
			session.Close()
		}
	}()

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		if err := checkMessageType(message, "bye"); assert.NoError(err) {
			assert.Equal("session_closed", message.Bye.Reason)
		}
	}

	if message, err := client.RunUntilMessage(ctx); assert.Error(err) {
		assert.True(websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived), "expected close error, got %+v", err)
	} else {
		t.Errorf("expected error, got %+v", message)
	}

	<-mcu.created
	assert.True(mcu.cancelled.Load())
}

type CodecsTestMCU struct {
	TestMCU
}

func NewCodecsTestMCU(t *testing.T) *CodecsTestMCU {
	return &CodecsTestMCU{
		TestMCU: TestMCU{
			t: t,
		},
	}
}

func (m *CodecsTestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	assert.Equal(m.t, "opus,g722", settings.AudioCodec)
	assert.Equal(m.t, "vp9,vp8,av1", settings.VideoCodec)
	return &TestMCUPublisher{
		id:         id,
		sid:        sid,
		streamType: streamType,
	}, nil
}

func TestProxyCodecs(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewCodecsTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "create-publisher",
			StreamType: sfu.StreamTypeVideo,
			PublisherSettings: &sfu.NewPublisherSettings{
				AudioCodec: "opus,g722",
				VideoCodec: "vp9,vp8,av1",
			},
		},
	}))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			assert.NotEmpty(message.Command.Id)
		}
	}
}

type StreamTestMCU struct {
	TestMCU

	streams []sfu.PublisherStream
}

type StreamsTestPublisher struct {
	TestMCUPublisher

	streams []sfu.PublisherStream
}

func (m *StreamTestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	return &StreamsTestPublisher{
		TestMCUPublisher: TestMCUPublisher{
			id:         id,
			sid:        sid,
			streamType: streamType,
		},

		streams: m.streams,
	}, nil
}

func (p *StreamsTestPublisher) GetStreams(ctx context.Context) ([]sfu.PublisherStream, error) {
	return p.streams, nil
}

func NewStreamTestMCU(t *testing.T, streams []sfu.PublisherStream) *StreamTestMCU {
	return &StreamTestMCU{
		TestMCU: TestMCU{
			t: t,
		},

		streams: streams,
	}
}

func TestProxyStreams(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	streams := []sfu.PublisherStream{
		{
			Mid:    "0",
			Mindex: 0,
			Type:   "audio",
			Codec:  "opus",
		},
		{
			Mid:    "1",
			Mindex: 1,
			Type:   "video",
			Codec:  "vp8",
		},
	}

	mcu := NewStreamTestMCU(t, streams)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "create-publisher",
			StreamType: sfu.StreamTypeVideo,
		},
	}))

	var clientId string
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			if assert.NotEmpty(message.Command.Id) {
				clientId = message.Command.Id
			}
		}
	}

	require.NotEmpty(clientId, "should have received publisher id")

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:     "get-publisher-streams",
			ClientId: clientId,
		},
	}))
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			assert.Equal(clientId, message.Command.Id)
			assert.Equal(streams, message.Command.Streams)
		}
	}
}

type RemoteSubscriberTestMCU struct {
	TestMCU

	publisher  *TestRemotePublisher
	subscriber *TestRemoteSubscriber
}

func NewRemoteSubscriberTestMCU(t *testing.T) *RemoteSubscriberTestMCU {
	return &RemoteSubscriberTestMCU{
		TestMCU: TestMCU{
			t: t,
		},
	}
}

type TestRemotePublisher struct {
	t *testing.T

	streamType sfu.StreamType
	refcnt     atomic.Int32
	closed     context.Context
	closeFunc  context.CancelFunc
	listener   sfu.Listener
	controller sfu.RemotePublisherController
}

func (p *TestRemotePublisher) Id() string {
	return "id"
}

func (p *TestRemotePublisher) PublisherId() api.PublicSessionId {
	return "id"
}

func (p *TestRemotePublisher) Sid() string {
	return "sid"
}

func (p *TestRemotePublisher) StreamType() sfu.StreamType {
	return p.streamType
}

func (p *TestRemotePublisher) MaxBitrate() api.Bandwidth {
	return 0
}

func (p *TestRemotePublisher) Close(ctx context.Context) {
	if count := p.refcnt.Add(-1); assert.GreaterOrEqual(p.t, int(count), 0) && count == 0 {
		p.closeFunc()
		shortCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		// Won't be able to preform remote call to actually stop publishing.
		if err := p.controller.StopPublishing(shortCtx, p); !errors.Is(err, context.DeadlineExceeded) {
			assert.NoError(p.t, err)
		}
		p.listener.PublisherClosed(p)
	}
}

func (p *TestRemotePublisher) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	callback(errors.New("not implemented"), nil)
}

func (p *TestRemotePublisher) Port() int {
	return 1
}

func (p *TestRemotePublisher) RtcpPort() int {
	return 2
}

func (p *TestRemotePublisher) SetMedia(mediaType sfu.MediaType) {
}

func (p *TestRemotePublisher) HasMedia(mediaType sfu.MediaType) bool {
	return false
}

func (m *RemoteSubscriberTestMCU) NewRemotePublisher(ctx context.Context, listener sfu.Listener, controller sfu.RemotePublisherController, streamType sfu.StreamType) (sfu.RemotePublisher, error) {
	require.Nil(m.t, m.publisher)
	assert.EqualValues(m.t, "video", streamType)
	closeCtx, closeFunc := context.WithCancel(context.Background())
	m.publisher = &TestRemotePublisher{
		t: m.t,

		streamType: streamType,
		closed:     closeCtx,
		closeFunc:  closeFunc,
		listener:   listener,
		controller: controller,
	}
	m.publisher.refcnt.Add(1)
	return m.publisher, nil
}

type TestRemoteSubscriber struct {
	t *testing.T

	publisher *TestRemotePublisher
	closed    context.Context
	closeFunc context.CancelFunc
}

func (s *TestRemoteSubscriber) Id() string {
	return "id"
}

func (s *TestRemoteSubscriber) Sid() string {
	return "sid"
}

func (s *TestRemoteSubscriber) StreamType() sfu.StreamType {
	return s.publisher.StreamType()
}

func (s *TestRemoteSubscriber) MaxBitrate() api.Bandwidth {
	return 0
}

func (s *TestRemoteSubscriber) Close(ctx context.Context) {
	s.publisher.Close(ctx)
	s.closeFunc()
}

func (s *TestRemoteSubscriber) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	callback(errors.New("not implemented"), nil)
}

func (s *TestRemoteSubscriber) Publisher() api.PublicSessionId {
	return api.PublicSessionId(s.publisher.Id())
}

func (m *RemoteSubscriberTestMCU) NewRemoteSubscriber(ctx context.Context, listener sfu.Listener, publisher sfu.RemotePublisher) (sfu.RemoteSubscriber, error) {
	require.Nil(m.t, m.subscriber)
	pub, ok := publisher.(*TestRemotePublisher)
	require.True(m.t, ok)
	closeCtx, closeFunc := context.WithCancel(context.Background())
	m.subscriber = &TestRemoteSubscriber{
		t: m.t,

		publisher: pub,
		closed:    closeCtx,
		closeFunc: closeFunc,
	}
	pub.refcnt.Add(1)
	return m.subscriber, nil
}

func TestProxyRemoteSubscriber(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewRemoteSubscriberTestMCU(t)
	proxyServer.mcu = mcu
	// Unused but must be set so remote subscribing works
	proxyServer.tokenId = "token"
	proxyServer.tokenKey = key
	proxyServer.remoteHostname = "test-hostname"

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	publisherId := api.PublicSessionId("the-publisher-id")
	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
			Subject:  string(publisherId),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:        "create-subscriber",
			StreamType:  sfu.StreamTypeVideo,
			PublisherId: publisherId,
			RemoteUrl:   "https://remote-hostname",
			RemoteToken: tokenString,
		},
	}))

	var clientId string
	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
			clientId = message.Command.Id
		}
	}

	assert.True(proxyServer.hasRemotePublishers())

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:     "delete-subscriber",
			ClientId: clientId,
		},
	}))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			assert.Equal(clientId, message.Command.Id)
		}
	}

	if assert.NotNil(mcu.publisher) && assert.NotNil(mcu.subscriber) {
		select {
		case <-mcu.subscriber.closed.Done():
		case <-ctx.Done():
			assert.Fail("subscriber was not closed")
		}
		select {
		case <-mcu.publisher.closed.Done():
		case <-ctx.Done():
			assert.Fail("publisher was not closed")
		}
	}

	assert.False(proxyServer.hasRemotePublishers())
}

func TestProxyCloseRemoteOnSessionClose(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewRemoteSubscriberTestMCU(t)
	proxyServer.mcu = mcu
	// Unused but must be set so remote subscribing works
	proxyServer.tokenId = "token"
	proxyServer.tokenKey = key
	proxyServer.remoteHostname = "test-hostname"

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client := NewProxyTestClient(ctx, t, server.URL)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(key))

	if hello, err := client.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	publisherId := api.PublicSessionId("the-publisher-id")
	claims := &proxy.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
			Subject:  string(publisherId),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(err)

	require.NoError(client.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:        "create-subscriber",
			StreamType:  sfu.StreamTypeVideo,
			PublisherId: publisherId,
			RemoteUrl:   "https://remote-hostname",
			RemoteToken: tokenString,
		},
	}))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	// Closing the session will cause any active remote publishers stop be stopped.
	client.CloseWithBye()

	if assert.NotNil(mcu.publisher) && assert.NotNil(mcu.subscriber) {
		select {
		case <-mcu.subscriber.closed.Done():
		case <-ctx.Done():
			assert.Fail("subscriber was not closed")
		}
		select {
		case <-mcu.publisher.closed.Done():
		case <-ctx.Done():
			assert.Fail("publisher was not closed")
		}
	}
}

type UnpublishRemoteTestMCU struct {
	TestMCU

	publisher atomic.Pointer[UnpublishRemoteTestPublisher]
}

func NewUnpublishRemoteTestMCU(t *testing.T) *UnpublishRemoteTestMCU {
	return &UnpublishRemoteTestMCU{
		TestMCU: TestMCU{
			t: t,
		},
	}
}

type UnpublishRemoteTestPublisher struct {
	TestMCUPublisher

	t *testing.T // +checklocksignore: Only written to from constructor.

	mu sync.RWMutex
	// +checklocks:mu
	remoteId api.PublicSessionId
	// +checklocks:mu
	remoteData *remotePublisherData
}

func (m *UnpublishRemoteTestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	publisher := &UnpublishRemoteTestPublisher{
		TestMCUPublisher: TestMCUPublisher{
			id:         id,
			sid:        sid,
			streamType: streamType,
		},

		t: m.t,
	}
	m.publisher.Store(publisher)
	return publisher, nil
}

func (p *UnpublishRemoteTestPublisher) getRemoteId() api.PublicSessionId {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.remoteId
}

func (p *UnpublishRemoteTestPublisher) getRemoteData() *remotePublisherData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.remoteData
}

func (p *UnpublishRemoteTestPublisher) clearRemote() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remoteId = ""
	p.remoteData = nil
}

func (p *UnpublishRemoteTestPublisher) PublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if assert.Empty(p.t, p.remoteId) {
		p.remoteId = remoteId
		p.remoteData = &remotePublisherData{
			hostname: hostname,
			port:     port,
			rtcpPort: rtcpPort,
		}
	}
	return nil
}

func (p *UnpublishRemoteTestPublisher) UnpublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	assert.Equal(p.t, remoteId, p.remoteId)
	if remoteData := p.remoteData; assert.NotNil(p.t, remoteData) &&
		assert.Equal(p.t, remoteData.hostname, hostname) &&
		assert.Equal(p.t, remoteData.port, port) &&
		assert.Equal(p.t, remoteData.rtcpPort, rtcpPort) {
		p.remoteId = ""
		p.remoteData = nil
	}
	return nil
}

func TestProxyUnpublishRemote(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewUnpublishRemoteTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewProxyTestClient(ctx, t, server.URL)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(key))

	if hello, err := client1.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client1.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	publisherId := api.PublicSessionId("the-publisher-id")
	require.NoError(client1.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:        "create-publisher",
			PublisherId: publisherId,
			Sid:         "1234-abcd",
			StreamType:  sfu.StreamTypeVideo,
			PublisherSettings: &sfu.NewPublisherSettings{
				Bitrate:    1234567,
				MediaTypes: sfu.MediaTypeAudio | sfu.MediaTypeVideo,
			},
		},
	}))

	var clientId string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
			clientId = message.Command.Id
		}
	}

	client2 := NewProxyTestClient(ctx, t, server.URL)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(key))

	hello2, err := client2.RunUntilHello(ctx)
	if assert.NoError(err) {
		assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2)
	}

	_, err = client2.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client2.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "publish-remote",
			StreamType: sfu.StreamTypeVideo,
			ClientId:   clientId,
			Hostname:   "remote-host",
			Port:       10001,
			RtcpPort:   10002,
		},
	}))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Equal(hello2.Hello.SessionId, publisher.getRemoteId())
		if remoteData := publisher.getRemoteData(); assert.NotNil(remoteData) {
			assert.Equal("remote-host", remoteData.hostname)
			assert.Equal(10001, remoteData.port)
			assert.Equal(10002, remoteData.rtcpPort)
		}
	}

	require.NoError(client2.WriteJSON(&proxy.ClientMessage{
		Id:   "4567",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "unpublish-remote",
			StreamType: sfu.StreamTypeVideo,
			ClientId:   clientId,
			Hostname:   "remote-host",
			Port:       10001,
			RtcpPort:   10002,
		},
	}))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("4567", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Empty(publisher.getRemoteId())
		assert.Nil(publisher.getRemoteData())
	}
}

func TestProxyUnpublishRemotePublisherClosed(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewUnpublishRemoteTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewProxyTestClient(ctx, t, server.URL)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(key))

	if hello, err := client1.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client1.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	publisherId := api.PublicSessionId("the-publisher-id")
	require.NoError(client1.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:        "create-publisher",
			PublisherId: publisherId,
			Sid:         "1234-abcd",
			StreamType:  sfu.StreamTypeVideo,
			PublisherSettings: &sfu.NewPublisherSettings{
				Bitrate:    1234567,
				MediaTypes: sfu.MediaTypeAudio | sfu.MediaTypeVideo,
			},
		},
	}))

	var clientId string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
			clientId = message.Command.Id
		}
	}

	client2 := NewProxyTestClient(ctx, t, server.URL)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(key))

	hello2, err := client2.RunUntilHello(ctx)
	if assert.NoError(err) {
		assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2)
	}

	_, err = client2.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client2.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "publish-remote",
			StreamType: sfu.StreamTypeVideo,
			ClientId:   clientId,
			Hostname:   "remote-host",
			Port:       10001,
			RtcpPort:   10002,
		},
	}))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Equal(hello2.Hello.SessionId, publisher.getRemoteId())
		if remoteData := publisher.getRemoteData(); assert.NotNil(remoteData) {
			assert.Equal("remote-host", remoteData.hostname)
			assert.Equal(10001, remoteData.port)
			assert.Equal(10002, remoteData.rtcpPort)
		}
	}

	require.NoError(client1.WriteJSON(&proxy.ClientMessage{
		Id:   "4567",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:     "delete-publisher",
			ClientId: clientId,
		},
	}))

	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("4567", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	// Remote publishing was not stopped explicitly...
	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Equal(hello2.Hello.SessionId, publisher.getRemoteId())
		if remoteData := publisher.getRemoteData(); assert.NotNil(remoteData) {
			assert.Equal("remote-host", remoteData.hostname)
			assert.Equal(10001, remoteData.port)
			assert.Equal(10002, remoteData.rtcpPort)
		}
	}

	// ...but the session no longer contains information on the remote publisher.
	if data, err := proxyServer.cookie.DecodePublic(hello2.Hello.SessionId); assert.NoError(err) {
		session := proxyServer.GetSession(data.Sid)
		if assert.NotNil(session) {
			session.remotePublishersLock.Lock()
			defer session.remotePublishersLock.Unlock()
			assert.Empty(session.remotePublishers)
		}
	}

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		publisher.clearRemote()
	}
}

func TestProxyUnpublishRemoteOnSessionClose(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	proxyServer, key, server := newProxyServerForTest(t)

	mcu := NewUnpublishRemoteTestMCU(t)
	proxyServer.mcu = mcu

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1 := NewProxyTestClient(ctx, t, server.URL)
	defer client1.CloseWithBye()

	require.NoError(client1.SendHello(key))

	if hello, err := client1.RunUntilHello(ctx); assert.NoError(err) {
		assert.NotEmpty(hello.Hello.SessionId, "%+v", hello)
	}

	_, err := client1.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	publisherId := api.PublicSessionId("the-publisher-id")
	require.NoError(client1.WriteJSON(&proxy.ClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:        "create-publisher",
			PublisherId: publisherId,
			Sid:         "1234-abcd",
			StreamType:  sfu.StreamTypeVideo,
			PublisherSettings: &sfu.NewPublisherSettings{
				Bitrate:    1234567,
				MediaTypes: sfu.MediaTypeAudio | sfu.MediaTypeVideo,
			},
		},
	}))

	var clientId string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("2345", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
			clientId = message.Command.Id
		}
	}

	client2 := NewProxyTestClient(ctx, t, server.URL)
	defer client2.CloseWithBye()

	require.NoError(client2.SendHello(key))

	hello2, err := client2.RunUntilHello(ctx)
	if assert.NoError(err) {
		assert.NotEmpty(hello2.Hello.SessionId, "%+v", hello2)
	}

	_, err = client2.RunUntilLoad(ctx, 0)
	assert.NoError(err)

	require.NoError(client2.WriteJSON(&proxy.ClientMessage{
		Id:   "3456",
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:       "publish-remote",
			StreamType: sfu.StreamTypeVideo,
			ClientId:   clientId,
			Hostname:   "remote-host",
			Port:       10001,
			RtcpPort:   10002,
		},
	}))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal("3456", message.Id)
		if err := checkMessageType(message, "command"); assert.NoError(err) {
			require.NotEmpty(message.Command.Id)
		}
	}

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Equal(hello2.Hello.SessionId, publisher.getRemoteId())
		if remoteData := publisher.getRemoteData(); assert.NotNil(remoteData) {
			assert.Equal("remote-host", remoteData.hostname)
			assert.Equal(10001, remoteData.port)
			assert.Equal(10002, remoteData.rtcpPort)
		}
	}

	// Closing the session will cause any active remote publishers stop be stopped.
	client2.CloseWithBye()

	if publisher := mcu.publisher.Load(); assert.NotNil(publisher) {
		assert.Empty(publisher.getRemoteId())
		assert.Nil(publisher.getRemoteData())
	}
}
