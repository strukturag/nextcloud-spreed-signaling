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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt"
	"github.com/gorilla/mux"
	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

const (
	KeypairSizeForTest = 2048
	TokenIdForTest     = "foo"
)

func newProxyServerForTest(t *testing.T) (*ProxyServer, *rsa.PrivateKey) {
	tempdir := t.TempDir()
	var server *ProxyServer
	t.Cleanup(func() {
		if server != nil {
			server.Stop()
		}
	})

	r := mux.NewRouter()
	key, err := rsa.GenerateKey(rand.Reader, KeypairSizeForTest)
	if err != nil {
		t.Fatalf("could not generate key: %s", err)
	}
	priv := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	privkey, err := os.CreateTemp(tempdir, "privkey*.pem")
	if err != nil {
		t.Fatalf("could not create temporary file for private key: %s", err)
	}
	if err := pem.Encode(privkey, priv); err != nil {
		t.Fatalf("could not encode private key: %s", err)
	}

	pubData, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("could not marshal public key: %s", err)
	}
	pub := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubData,
	}
	pubkey, err := os.CreateTemp(tempdir, "pubkey*.pem")
	if err != nil {
		t.Fatalf("could not create temporary file for public key: %s", err)
	}
	if err := pem.Encode(pubkey, pub); err != nil {
		t.Fatalf("could not encode public key: %s", err)
	}

	config := goconf.NewConfigFile()
	config.AddOption("tokens", TokenIdForTest, pubkey.Name())

	if server, err = NewProxyServer(r, "0.0", config); err != nil {
		t.Fatalf("could not create server: %s", err)
	}
	return server, key
}

func TestTokenInFuture(t *testing.T) {
	server, key := newProxyServerForTest(t)

	claims := &signaling.TokenClaims{
		StandardClaims: jwt.StandardClaims{
			IssuedAt: time.Now().Add(time.Hour).Unix(),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("could not create token: %s", err)
	}

	hello := &signaling.HelloProxyClientMessage{
		Version: "1.0",
		Token:   tokenString,
	}
	session, err := server.NewSession(hello)
	if session != nil {
		defer session.Close()
		t.Errorf("should not have created session")
	} else if err != TokenNotValidYet {
		t.Errorf("could have failed with TokenNotValidYet, got %s", err)
	}
}
