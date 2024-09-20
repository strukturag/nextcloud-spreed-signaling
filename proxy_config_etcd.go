/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type proxyConfigEtcd struct {
	log   *zap.Logger
	mu    sync.Mutex
	proxy McuProxy

	client    *EtcdClient
	keyPrefix string
	keyInfos  map[string]*ProxyInformationEtcd
	urlToKey  map[string]string

	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewProxyConfigEtcd(log *zap.Logger, config *goconf.ConfigFile, etcdClient *EtcdClient, proxy McuProxy) (ProxyConfig, error) {
	if !etcdClient.IsConfigured() {
		return nil, errors.New("No etcd endpoints configured")
	}

	closeCtx, closeFunc := context.WithCancel(context.Background())

	result := &proxyConfigEtcd{
		log:   log,
		proxy: proxy,

		client:   etcdClient,
		keyInfos: make(map[string]*ProxyInformationEtcd),
		urlToKey: make(map[string]string),

		closeCtx:  closeCtx,
		closeFunc: closeFunc,
	}
	if err := result.configure(config, false); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *proxyConfigEtcd) configure(config *goconf.ConfigFile, fromReload bool) error {
	keyPrefix, _ := config.GetString("mcu", "keyprefix")
	if keyPrefix == "" {
		keyPrefix = "/%s"
	}

	p.keyPrefix = keyPrefix
	return nil
}

func (p *proxyConfigEtcd) Start() error {
	p.client.AddListener(p)
	return nil
}

func (p *proxyConfigEtcd) Reload(config *goconf.ConfigFile) error {
	// not implemented
	return nil
}

func (p *proxyConfigEtcd) Stop() {
	p.client.RemoveListener(p)
	p.closeFunc()
}

func (p *proxyConfigEtcd) EtcdClientCreated(client *EtcdClient) {
	go func() {
		if err := client.WaitForConnection(p.closeCtx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			panic(err)
		}

		backoff, err := NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
		if err != nil {
			panic(err)
		}

		var nextRevision int64
		for p.closeCtx.Err() == nil {
			response, err := p.getProxyUrls(p.closeCtx, client, p.keyPrefix)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				} else if errors.Is(err, context.DeadlineExceeded) {
					p.log.Error("Timeout getting initial list of proxy URLs, retry",
						zap.Duration("wait", backoff.NextWait()),
					)
				} else {
					p.log.Error("Could not get initial list of proxy URLs, retry",
						zap.Duration("wait", backoff.NextWait()),
						zap.Error(err),
					)
				}

				backoff.Wait(p.closeCtx)
				continue
			}

			for _, ev := range response.Kvs {
				p.EtcdKeyUpdated(client, string(ev.Key), ev.Value, nil)
			}
			nextRevision = response.Header.Revision + 1
			break
		}

		prevRevision := nextRevision
		backoff.Reset()
		for p.closeCtx.Err() == nil {
			var err error
			if nextRevision, err = client.Watch(p.closeCtx, p.keyPrefix, nextRevision, p, clientv3.WithPrefix()); err != nil {
				p.log.Error("Error processing watch, retry",
					zap.String("prefix", p.keyPrefix),
					zap.Duration("wait", backoff.NextWait()),
					zap.Error(err),
				)
				backoff.Wait(p.closeCtx)
				continue
			}

			if nextRevision != prevRevision {
				backoff.Reset()
				prevRevision = nextRevision
			} else {
				p.log.Warn("Processing watch interrupted, retry",
					zap.String("prefix", p.keyPrefix),
					zap.Duration("wait", backoff.NextWait()),
				)
				backoff.Wait(p.closeCtx)
			}
		}
	}()
}

func (p *proxyConfigEtcd) EtcdWatchCreated(client *EtcdClient, key string) {
}

func (p *proxyConfigEtcd) getProxyUrls(ctx context.Context, client *EtcdClient, keyPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return client.Get(ctx, keyPrefix, clientv3.WithPrefix())
}

func (p *proxyConfigEtcd) EtcdKeyUpdated(client *EtcdClient, key string, data []byte, prevValue []byte) {
	var info ProxyInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		p.log.Error("Could not decode proxy information",
			zap.ByteString("data", data),
			zap.Error(err),
		)
		return
	}
	if err := info.CheckValid(); err != nil {
		p.log.Error("Received invalid proxy information",
			zap.ByteString("data", data),
			zap.Error(err),
		)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	prev, found := p.keyInfos[key]
	if found && info.Address != prev.Address {
		// Address of a proxy has changed.
		p.removeEtcdProxyLocked(key)
		found = false
	}

	if otherKey, otherFound := p.urlToKey[info.Address]; otherFound && otherKey != key {
		p.log.Warn("Address is already registered, ignoring",
			zap.String("url", info.Address),
			zap.String("key", key),
			zap.String("other", otherKey),
		)
		return
	}

	if found {
		p.keyInfos[key] = &info
		p.proxy.KeepConnection(info.Address)
	} else {
		if err := p.proxy.AddConnection(false, info.Address); err != nil {
			p.log.Error("Could not create proxy connection",
				zap.String("url", info.Address),
				zap.Error(err),
			)
			return
		}

		p.log.Info("Added new proxy connection",
			zap.String("url", info.Address),
			zap.String("key", key),
		)
		p.keyInfos[key] = &info
		p.urlToKey[info.Address] = key
	}
}

func (p *proxyConfigEtcd) EtcdKeyDeleted(client *EtcdClient, key string, prevValue []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeEtcdProxyLocked(key)
}

func (p *proxyConfigEtcd) removeEtcdProxyLocked(key string) {
	info, found := p.keyInfos[key]
	if !found {
		return
	}

	delete(p.keyInfos, key)
	delete(p.urlToKey, info.Address)

	p.log.Info("Removing proxy connection",
		zap.String("target", info.Address),
		zap.String("key", key),
	)
	p.proxy.RemoveConnection(info.Address)
}
