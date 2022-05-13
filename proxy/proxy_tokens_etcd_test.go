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
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/lease"
)

var (
	etcdListenUrl = "http://localhost:8080"
)

func isErrorAddressAlreadyInUse(err error) bool {
	var eOsSyscall *os.SyscallError
	if !errors.As(err, &eOsSyscall) {
		return false
	}
	var errErrno syscall.Errno // doesn't need a "*" (ptr) because it's already a ptr (uintptr)
	if !errors.As(eOsSyscall, &errErrno) {
		return false
	}
	if errErrno == syscall.EADDRINUSE {
		return true
	}
	const WSAEADDRINUSE = 10048
	if runtime.GOOS == "windows" && errErrno == WSAEADDRINUSE {
		return true
	}
	return false
}

func newEtcdForTesting(t *testing.T) *embed.Etcd {
	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	os.Chmod(cfg.Dir, 0700) // nolint
	cfg.LogLevel = "warn"

	u, err := url.Parse(etcdListenUrl)
	if err != nil {
		t.Fatal(err)
	}

	// Find a free port to bind the server to.
	var etcd *embed.Etcd
	for port := 50000; port < 50100; port++ {
		u.Host = net.JoinHostPort("localhost", strconv.Itoa(port))
		cfg.LCUrls = []url.URL{*u}
		etcd, err = embed.StartEtcd(cfg)
		if isErrorAddressAlreadyInUse(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		break
	}
	if etcd == nil {
		t.Fatal("could not find free port")
	}

	t.Cleanup(func() {
		etcd.Close()
	})
	// Wait for server to be ready.
	<-etcd.Server.ReadyNotify()

	return etcd
}

func newTokensEtcdForTesting(t *testing.T) (*tokensEtcd, *embed.Etcd) {
	etcd := newEtcdForTesting(t)

	cfg := goconf.NewConfigFile()
	cfg.AddOption("tokens", "endpoints", etcd.Config().LCUrls[0].String())
	cfg.AddOption("tokens", "keyformat", "/%s, /testing/%s/key")

	tokens, err := NewProxyTokensEtcd(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tokens.Close()
	})

	return tokens.(*tokensEtcd), etcd
}

func storeKey(t *testing.T, etcd *embed.Etcd, key string, pubkey crypto.PublicKey) {
	var data []byte
	var err error
	switch pubkey := pubkey.(type) {
	case rsa.PublicKey:
		data, err = x509.MarshalPKIXPublicKey(&pubkey)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown key type %T in %+v", pubkey, pubkey)
	}

	data = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: data,
	})

	if kv := etcd.Server.KV(); kv != nil {
		kv.Put([]byte(key), data, lease.NoLease)
		kv.Commit()
	}
}

func generateAndSaveKey(t *testing.T, etcd *embed.Etcd, name string) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	storeKey(t, etcd, name, key.PublicKey)
	return key
}

func TestProxyTokensEtcd(t *testing.T) {
	tokens, etcd := newTokensEtcdForTesting(t)

	key1 := generateAndSaveKey(t, etcd, "/foo")
	key2 := generateAndSaveKey(t, etcd, "/testing/bar/key")

	if token, err := tokens.Get("foo"); err != nil {
		t.Error(err)
	} else if token == nil {
		t.Error("could not get token")
	} else if !key1.PublicKey.Equal(token.key) {
		t.Error("token keys mismatch")
	}

	if token, err := tokens.Get("bar"); err != nil {
		t.Error(err)
	} else if token == nil {
		t.Error("could not get token")
	} else if !key2.PublicKey.Equal(token.key) {
		t.Error("token keys mismatch")
	}
}
