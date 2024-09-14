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
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

const (
	KeypairSizeForTest = 2048
	TokenIdForTest     = "foo"
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

func newProxyServerForTest(t *testing.T) (*ProxyServer, *rsa.PrivateKey, *httptest.Server) {
	require := require.New(t)
	tempdir := t.TempDir()
	var proxy *ProxyServer
	t.Cleanup(func() {
		if proxy != nil {
			proxy.Stop()
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

	log := zaptest.NewLogger(t)
	proxy, err = NewProxyServer(log, r, "0.0", config)
	require.NoError(err)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	return proxy, key, server
}

func TestTokenValid(t *testing.T) {
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
		defer session.Close()
	}
}

func TestTokenNotSigned(t *testing.T) {
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
			defer session.Close()
		}
	}
}

func TestTokenUnknown(t *testing.T) {
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
			defer session.Close()
		}
	}
}

func TestTokenInFuture(t *testing.T) {
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
			defer session.Close()
		}
	}
}

func TestTokenExpired(t *testing.T) {
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
			defer session.Close()
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
