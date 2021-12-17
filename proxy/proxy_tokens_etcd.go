/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt"

	"go.etcd.io/etcd/client/pkg/v3/srv"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

const (
	tokenCacheSize = 4096
)

type tokenCacheEntry struct {
	keyValue []byte
	token    *ProxyToken
}

type tokensEtcd struct {
	client atomic.Value

	tokenFormats atomic.Value
	tokenCache   *signaling.LruCache
}

func NewProxyTokensEtcd(config *goconf.ConfigFile) (ProxyTokens, error) {
	result := &tokensEtcd{
		tokenCache: signaling.NewLruCache(tokenCacheSize),
	}
	if err := result.load(config, false); err != nil {
		return nil, err
	}

	return result, nil
}

func (t *tokensEtcd) getClient() *clientv3.Client {
	c := t.client.Load()
	if c == nil {
		return nil
	}

	return c.(*clientv3.Client)
}

func (t *tokensEtcd) getKeys(id string) []string {
	format := t.tokenFormats.Load().([]string)
	var result []string
	for _, f := range format {
		result = append(result, fmt.Sprintf(f, id))
	}
	return result
}

func (t *tokensEtcd) getByKey(id string, key string) (*ProxyToken, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := t.getClient().Get(ctx, key)
	if err != nil {
		return nil, err
	}

	if len(resp.Kvs) == 0 {
		return nil, nil
	} else if len(resp.Kvs) > 1 {
		log.Printf("Received multiple keys for %s, using last", key)
	}

	keyValue := resp.Kvs[len(resp.Kvs)-1].Value
	cached, _ := t.tokenCache.Get(key).(*tokenCacheEntry)
	if cached == nil || !bytes.Equal(cached.keyValue, keyValue) {
		// Parsed public keys are cached to avoid the parse overhead.
		publicKey, err := jwt.ParseRSAPublicKeyFromPEM(keyValue)
		if err != nil {
			return nil, err
		}

		cached = &tokenCacheEntry{
			keyValue: keyValue,
			token: &ProxyToken{
				id:  id,
				key: publicKey,
			},
		}
		t.tokenCache.Set(key, cached)
	}

	return cached.token, nil
}

func (t *tokensEtcd) Get(id string) (*ProxyToken, error) {
	for _, k := range t.getKeys(id) {
		token, err := t.getByKey(id, k)
		if err != nil {
			log.Printf("Could not get public key from %s for %s: %s", k, id, err)
			continue
		} else if token == nil {
			continue
		}

		return token, nil
	}

	return nil, nil
}

func (t *tokensEtcd) load(config *goconf.ConfigFile, ignoreErrors bool) error {
	var endpoints []string
	if endpointsString, _ := config.GetString("tokens", "endpoints"); endpointsString != "" {
		for _, ep := range strings.Split(endpointsString, ",") {
			ep := strings.TrimSpace(ep)
			if ep != "" {
				endpoints = append(endpoints, ep)
			}
		}
	} else if discoverySrv, _ := config.GetString("tokens", "discoverysrv"); discoverySrv != "" {
		discoveryService, _ := config.GetString("tokens", "discoveryservice")
		clients, err := srv.GetClient("etcd-client", discoverySrv, discoveryService)
		if err != nil {
			if !ignoreErrors {
				return fmt.Errorf("Could not discover endpoints for %s: %s", discoverySrv, err)
			}
		} else {
			endpoints = clients.Endpoints
		}
	}

	if len(endpoints) == 0 {
		if !ignoreErrors {
			return fmt.Errorf("No token endpoints configured")
		}

		log.Printf("No token endpoints configured, not changing client")
	} else {
		cfg := clientv3.Config{
			Endpoints: endpoints,

			// set timeout per request to fail fast when the target endpoint is unavailable
			DialTimeout: time.Second,
		}

		clientKey, _ := config.GetString("tokens", "clientkey")
		clientCert, _ := config.GetString("tokens", "clientcert")
		caCert, _ := config.GetString("tokens", "cacert")
		if clientKey != "" && clientCert != "" && caCert != "" {
			tlsInfo := transport.TLSInfo{
				CertFile:      clientCert,
				KeyFile:       clientKey,
				TrustedCAFile: caCert,
			}
			tlsConfig, err := tlsInfo.ClientConfig()
			if err != nil {
				if !ignoreErrors {
					return fmt.Errorf("Could not setup TLS configuration: %s", err)
				}

				log.Printf("Could not setup TLS configuration, will be disabled (%s)", err)
			} else {
				cfg.TLS = tlsConfig
			}
		}

		c, err := clientv3.New(cfg)
		if err != nil {
			if !ignoreErrors {
				return err
			}

			log.Printf("Could not create new client from token endpoints %+v: %s", endpoints, err)
		} else {
			prev := t.getClient()
			if prev != nil {
				prev.Close()
			}
			t.client.Store(c)
			log.Printf("Using token endpoints %+v", endpoints)
		}
	}

	tokenFormat, _ := config.GetString("tokens", "keyformat")
	if tokenFormat == "" {
		tokenFormat = "/%s"
	}

	formats := strings.Split(tokenFormat, ",")
	var tokenFormats []string
	for _, f := range formats {
		f = strings.TrimSpace(f)
		if f != "" {
			tokenFormats = append(tokenFormats, f)
		}
	}

	t.tokenFormats.Store(tokenFormats)
	log.Printf("Using %v as token formats", tokenFormats)
	return nil
}

func (t *tokensEtcd) Reload(config *goconf.ConfigFile) {
	if err := t.load(config, true); err != nil {
		log.Printf("Error reloading etcd tokens: %s", err)
	}
}

func (t *tokensEtcd) Close() {
	if client := t.getClient(); client != nil {
		client.Close()
	}
}
