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
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

const (
	KeypairSizeForTest = 2048
	TokenIdForTest     = "foo"

	testTimeout = 10 * time.Second
)

func getWebsocketUrl(url string) string {
	if strings.HasPrefix(url, "http://") {
		return "ws://" + url[7:] + "/proxy"
	} else if strings.HasPrefix(url, "https://") {
		return "wss://" + url[8:] + "/proxy"
	} else {
		panic("Unsupported URL: " + url)
	}
}

func WaitForProxyServer(ctx context.Context, t *testing.T, proxy *ProxyServer) {
	// Wait for any channel messages to be processed.
	time.Sleep(10 * time.Millisecond)
	proxy.Stop()
	for {
		proxy.clientsLock.Lock()
		clients := len(proxy.clients)
		sessions := len(proxy.sessions)
		proxy.clientsLock.Unlock()
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
			assert.Fail(t, fmt.Sprintf("Error waiting for clients %+v / sessions %+v / remoteConnections %+v to terminate: %+v", proxy.clients, proxy.sessions, proxy.remoteConnections, ctx.Err()))
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

	proxy, err = NewProxyServer(r, "0.0", config)
	require.NoError(err)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	return proxy, key, server
}

func TestTokenValid(t *testing.T) {
	signaling.CatchLogForTest(t)
	proxy, key, _ := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxy.NewSession(hello); assert.NoError(t, err) {
		defer proxy.DeleteSession(session.Sid())
	}
}

func TestTokenNotSigned(t *testing.T) {
	signaling.CatchLogForTest(t)
	proxy, _, _ := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxy.NewSession(hello); !assert.ErrorIs(t, err, TokenAuthFailed) {
		if session != nil {
			defer proxy.DeleteSession(session.Sid())
		}
	}
}

func TestTokenUnknown(t *testing.T) {
	signaling.CatchLogForTest(t)
	proxy, key, _ := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest + "2",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxy.NewSession(hello); !assert.ErrorIs(t, err, TokenAuthFailed) {
		if session != nil {
			defer proxy.DeleteSession(session.Sid())
		}
	}
}

func TestTokenInFuture(t *testing.T) {
	signaling.CatchLogForTest(t)
	proxy, key, _ := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxy.NewSession(hello); !assert.ErrorIs(t, err, TokenNotValidYet) {
		if session != nil {
			defer proxy.DeleteSession(session.Sid())
		}
	}
}

func TestTokenExpired(t *testing.T) {
	signaling.CatchLogForTest(t)
	proxy, key, _ := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge * 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	require.NoError(t, err)

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	if session, err := proxy.NewSession(hello); !assert.ErrorIs(t, err, TokenExpired) {
		if session != nil {
			defer proxy.DeleteSession(session.Sid())
		}
	}
}

func TestPublicIPs(t *testing.T) {
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
	signaling.CatchLogForTest(t)
	assert := assert.New(t)
	_, _, server := newProxyServerForTest(t)

	conn, response, err := websocket.DefaultDialer.DialContext(context.Background(), getWebsocketUrl(server.URL), nil)
	require.NoError(t, err)
	defer conn.Close() // nolint

	if server := response.Header.Get("Server"); !strings.HasPrefix(server, "nextcloud-spreed-signaling-proxy/") {
		assert.Fail("expected valid server header, got \"%s\"", server)
	}
	features := response.Header.Get("X-Spreed-Signaling-Features")
	featuresList := make(map[string]bool)
	for _, f := range strings.Split(features, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			if _, found := featuresList[f]; found {
				assert.Fail("duplicate feature id \"%s\" in \"%s\"", f, features)
			}
			featuresList[f] = true
		}
	}
	assert.NotEmpty(featuresList, "expected valid features header, got \"%s\"", features)
	if _, found := featuresList["remote-streams"]; !found {
		assert.Fail("expected feature \"remote-streams\", got \"%s\"", features)
	}

	assert.NoError(conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{}))
}

func TestProxyCreateSession(t *testing.T) {
	signaling.CatchLogForTest(t)
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

type HangingTestMCU struct {
	t         *testing.T
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
		t:        t,
		ctx:      ctx,
		creating: make(chan struct{}),
		created:  make(chan struct{}),
	}
}

func (m *HangingTestMCU) Start(ctx context.Context) error {
	return nil
}

func (m *HangingTestMCU) Stop() {
}

func (m *HangingTestMCU) Reload(config *goconf.ConfigFile) {
}

func (m *HangingTestMCU) SetOnConnected(f func()) {
}

func (m *HangingTestMCU) SetOnDisconnected(f func()) {
}

func (m *HangingTestMCU) GetStats() interface{} {
	return nil
}

func (m *HangingTestMCU) NewPublisher(ctx context.Context, listener signaling.McuListener, id string, sid string, streamType signaling.StreamType, bitrate int, mediaTypes signaling.MediaType, initiator signaling.McuInitiator) (signaling.McuPublisher, error) {
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

func (m *HangingTestMCU) NewSubscriber(ctx context.Context, listener signaling.McuListener, publisher string, streamType signaling.StreamType, initiator signaling.McuInitiator) (signaling.McuSubscriber, error) {
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
	signaling.CatchLogForTest(t)
	assert := assert.New(t)
	require := require.New(t)
	proxy, key, server := newProxyServerForTest(t)

	mcu := NewHangingTestMCU(t)
	proxy.mcu = mcu

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

	require.NoError(client.WriteJSON(&signaling.ProxyClientMessage{
		Id:   "2345",
		Type: "command",
		Command: &signaling.CommandProxyClientMessage{
			Type:       "create-publisher",
			StreamType: signaling.StreamTypeVideo,
		},
	}))

	// Simulate expired session while request is still being processed.
	go func() {
		<-mcu.creating
		if session := proxy.GetSession(1); assert.NotNil(session) {
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
