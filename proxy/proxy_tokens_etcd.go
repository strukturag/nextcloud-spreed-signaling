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
	client *signaling.EtcdClient

	tokenFormats atomic.Value
	tokenCache   *signaling.LruCache
}

func NewProxyTokensEtcd(config *goconf.ConfigFile) (ProxyTokens, error) {
	client, err := signaling.NewEtcdClient(config, "tokens")
	if err != nil {
		return nil, err
	}

	if !client.IsConfigured() {
		return nil, fmt.Errorf("No etcd endpoints configured")
	}

	result := &tokensEtcd{
		client:     client,
		tokenCache: signaling.NewLruCache(tokenCacheSize),
	}
	if err := result.load(config, false); err != nil {
		return nil, err
	}

	return result, nil
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

	resp, err := t.client.Get(ctx, key)
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
	tokenFormat, _ := config.GetString("tokens", "keyformat")

	formats := strings.Split(tokenFormat, ",")
	var tokenFormats []string
	for _, f := range formats {
		f = strings.TrimSpace(f)
		if f != "" {
			tokenFormats = append(tokenFormats, f)
		}
	}
	if len(tokenFormats) == 0 {
		tokenFormats = []string{"/%s"}
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
	if err := t.client.Close(); err != nil {
		log.Printf("Error while closing etcd client: %s", err)
	}
}
