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
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/client/pkg/v3/srv"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type EtcdClientListener interface {
	EtcdClientCreated(client *EtcdClient)
}

type EtcdClientWatcher interface {
	EtcdWatchCreated(client *EtcdClient, key string)
	EtcdKeyUpdated(client *EtcdClient, key string, value []byte, prevValue []byte)
	EtcdKeyDeleted(client *EtcdClient, key string, prevValue []byte)
}

type EtcdClient struct {
	log           *zap.Logger
	compatSection string

	mu        sync.Mutex
	client    atomic.Value
	listeners map[EtcdClientListener]bool
}

func NewEtcdClient(log *zap.Logger, config *goconf.ConfigFile, compatSection string) (*EtcdClient, error) {
	result := &EtcdClient{
		log:           log,
		compatSection: compatSection,
	}
	if err := result.load(config, false); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *EtcdClient) getConfigStringWithFallback(config *goconf.ConfigFile, option string) string {
	value, _ := config.GetString("etcd", option)
	if value == "" && c.compatSection != "" {
		value, _ = config.GetString(c.compatSection, option)
		if value != "" {
			c.log.Warn("Configured etcd option is deprecated, use section \"etcd\" instead",
				zap.String("option", option),
				zap.String("section", c.compatSection),
			)
		}
	}

	return value
}

func (c *EtcdClient) load(config *goconf.ConfigFile, ignoreErrors bool) error {
	var endpoints []string
	if endpointsString := c.getConfigStringWithFallback(config, "endpoints"); endpointsString != "" {
		for _, ep := range strings.Split(endpointsString, ",") {
			ep := strings.TrimSpace(ep)
			if ep != "" {
				endpoints = append(endpoints, ep)
			}
		}
	} else if discoverySrv := c.getConfigStringWithFallback(config, "discoverysrv"); discoverySrv != "" {
		discoveryService := c.getConfigStringWithFallback(config, "discoveryservice")
		clients, err := srv.GetClient("etcd-client", discoverySrv, discoveryService)
		if err != nil {
			if !ignoreErrors {
				return fmt.Errorf("Could not discover etcd endpoints for %s: %w", discoverySrv, err)
			}
		} else {
			endpoints = clients.Endpoints
		}
	}

	if len(endpoints) == 0 {
		if !ignoreErrors {
			return nil
		}

		c.log.Info("No etcd endpoints configured, not changing client")
	} else {
		cfg := clientv3.Config{
			Endpoints: endpoints,

			// set timeout per request to fail fast when the target endpoint is unavailable
			DialTimeout: time.Second,
		}

		if logLevel, _ := config.GetString("etcd", "loglevel"); logLevel != "" {
			var l zapcore.Level
			if err := l.Set(logLevel); err != nil {
				return fmt.Errorf("Unsupported etcd log level %s: %w", logLevel, err)
			}

			logConfig := zap.NewProductionConfig()
			logConfig.Level = zap.NewAtomicLevelAt(l)
			cfg.LogConfig = &logConfig
		}

		clientKey := c.getConfigStringWithFallback(config, "clientkey")
		clientCert := c.getConfigStringWithFallback(config, "clientcert")
		caCert := c.getConfigStringWithFallback(config, "cacert")
		if clientKey != "" && clientCert != "" && caCert != "" {
			tlsInfo := transport.TLSInfo{
				CertFile:      clientCert,
				KeyFile:       clientKey,
				TrustedCAFile: caCert,
			}
			tlsConfig, err := tlsInfo.ClientConfig()
			if err != nil {
				if !ignoreErrors {
					return fmt.Errorf("Could not setup etcd TLS configuration: %w", err)
				}

				c.log.Warn("Could not setup TLS configuration, will be disabled",
					zap.Error(err),
				)
			} else {
				cfg.TLS = tlsConfig
			}
		}

		client, err := clientv3.New(cfg)
		if err != nil {
			if !ignoreErrors {
				return err
			}

			c.log.Error("Could not create new client from etd endpoints",
				zap.Any("endpoints", endpoints),
				zap.Error(err),
			)
		} else {
			prev := c.getEtcdClient()
			if prev != nil {
				prev.Close()
			}
			c.client.Store(client)
			c.log.Info("Using etcd endpoints",
				zap.Any("endpoints", endpoints),
			)
			c.notifyListeners()
		}
	}

	return nil
}

func (c *EtcdClient) Close() error {
	client := c.getEtcdClient()
	if client != nil {
		return client.Close()
	}

	return nil
}

func (c *EtcdClient) IsConfigured() bool {
	return c.getEtcdClient() != nil
}

func (c *EtcdClient) getEtcdClient() *clientv3.Client {
	client := c.client.Load()
	if client == nil {
		return nil
	}

	return client.(*clientv3.Client)
}

func (c *EtcdClient) syncClient(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return c.getEtcdClient().Sync(ctx)
}

func (c *EtcdClient) notifyListeners() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for listener := range c.listeners {
		listener.EtcdClientCreated(c)
	}
}

func (c *EtcdClient) AddListener(listener EtcdClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.listeners == nil {
		c.listeners = make(map[EtcdClientListener]bool)
	}
	c.listeners[listener] = true
	if client := c.getEtcdClient(); client != nil {
		go listener.EtcdClientCreated(c)
	}
}

func (c *EtcdClient) RemoveListener(listener EtcdClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.listeners, listener)
}

func (c *EtcdClient) WaitForConnection(ctx context.Context) error {
	backoff, err := NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
	if err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := c.syncClient(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			} else if errors.Is(err, context.DeadlineExceeded) {
				c.log.Error("Timeout waiting for etcd client to connect to the cluster, retry",
					zap.Duration("wait", backoff.NextWait()),
				)
			} else {
				c.log.Error("Could not sync etcd client with the cluster, retry",
					zap.Duration("wait", backoff.NextWait()),
					zap.Error(err),
				)
			}

			backoff.Wait(ctx)
			continue
		}

		c.log.Info("Client synced, using endpoints",
			zap.Any("endpoints", c.getEtcdClient().Endpoints()),
		)
		return nil
	}
}

func (c *EtcdClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return c.getEtcdClient().Get(ctx, key, opts...)
}

func (c *EtcdClient) Watch(ctx context.Context, key string, nextRevision int64, watcher EtcdClientWatcher, opts ...clientv3.OpOption) (int64, error) {
	c.log.Debug("Wait for leader and start watching",
		zap.String("key", key),
		zap.Int64("rev", nextRevision),
	)
	opts = append(opts, clientv3.WithRev(nextRevision), clientv3.WithPrevKV())
	ch := c.getEtcdClient().Watch(clientv3.WithRequireLeader(ctx), key, opts...)
	c.log.Debug("Watch created",
		zap.String("key", key),
	)
	watcher.EtcdWatchCreated(c, key)
	for response := range ch {
		if err := response.Err(); err != nil {
			return nextRevision, err
		}

		nextRevision = response.Header.Revision + 1
		for _, ev := range response.Events {
			switch ev.Type {
			case clientv3.EventTypePut:
				var prevValue []byte
				if ev.PrevKv != nil {
					prevValue = ev.PrevKv.Value
				}
				watcher.EtcdKeyUpdated(c, string(ev.Kv.Key), ev.Kv.Value, prevValue)
			case clientv3.EventTypeDelete:
				var prevValue []byte
				if ev.PrevKv != nil {
					prevValue = ev.PrevKv.Value
				}
				watcher.EtcdKeyDeleted(c, string(ev.Kv.Key), prevValue)
			default:
				c.log.Error("Unsupported watch event",
					zap.Any("type", ev.Type),
					zap.ByteString("key", ev.Kv.Key),
					zap.ByteString("value", ev.Kv.Value),
				)
			}
		}
	}

	return nextRevision, nil
}
