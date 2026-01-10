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
package etcd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/client/pkg/v3/srv"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/connectivity"

	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

var (
	initialWaitDelay = time.Second
	maxWaitDelay     = 8 * time.Second
)

type etcdClient struct {
	logger        log.Logger
	compatSection string

	mu     sync.Mutex
	client atomic.Value
	// +checklocks:mu
	listeners map[ClientListener]bool
}

func NewClient(logger log.Logger, config *goconf.ConfigFile, compatSection string) (Client, error) {
	result := &etcdClient{
		logger:        logger,
		compatSection: compatSection,
	}
	if err := result.load(config, false); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *etcdClient) GetServerInfoEtcd() *BackendServerInfoEtcd {
	client := c.getEtcdClient()
	if client == nil {
		return nil
	}

	result := &BackendServerInfoEtcd{
		Endpoints: client.Endpoints(),
	}

	conn := client.ActiveConnection()
	if conn != nil {
		result.Active = conn.Target()
		result.Connected = internal.MakePtr(conn.GetState() == connectivity.Ready)
	}

	return result
}

func (c *etcdClient) getConfigStringWithFallback(config *goconf.ConfigFile, option string) string {
	value, _ := config.GetString("etcd", option)
	if value == "" && c.compatSection != "" {
		value, _ = config.GetString(c.compatSection, option)
		if value != "" {
			c.logger.Printf("WARNING: Configuring etcd option \"%s\" in section \"%s\" is deprecated, use section \"etcd\" instead", option, c.compatSection)
		}
	}

	return value
}

func (c *etcdClient) load(config *goconf.ConfigFile, ignoreErrors bool) error {
	var endpoints []string
	if endpointsString := c.getConfigStringWithFallback(config, "endpoints"); endpointsString != "" {
		endpoints = slices.Collect(internal.SplitEntries(endpointsString, ","))
	} else if discoverySrv := c.getConfigStringWithFallback(config, "discoverysrv"); discoverySrv != "" {
		discoveryService := c.getConfigStringWithFallback(config, "discoveryservice")
		clients, err := srv.GetClient("etcd-client", discoverySrv, discoveryService)
		if err != nil {
			if !ignoreErrors {
				return fmt.Errorf("could not discover etcd endpoints for %s: %w", discoverySrv, err)
			}
		} else {
			endpoints = clients.Endpoints
		}
	}

	if len(endpoints) == 0 {
		if !ignoreErrors {
			return nil
		}

		c.logger.Printf("No etcd endpoints configured, not changing client")
	} else {
		cfg := clientv3.Config{
			Endpoints: endpoints,

			// set timeout per request to fail fast when the target endpoint is unavailable
			DialTimeout: time.Second,
		}

		if logLevel, _ := config.GetString("etcd", "loglevel"); logLevel != "" {
			var l zapcore.Level
			if err := l.Set(logLevel); err != nil {
				return fmt.Errorf("unsupported etcd log level %s: %w", logLevel, err)
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
					return fmt.Errorf("could not setup etcd TLS configuration: %w", err)
				}

				c.logger.Printf("Could not setup TLS configuration, will be disabled (%s)", err)
			} else {
				cfg.TLS = tlsConfig
			}
		}

		client, err := clientv3.New(cfg)
		if err != nil {
			if !ignoreErrors {
				return err
			}

			c.logger.Printf("Could not create new client from etd endpoints %+v: %s", endpoints, err)
		} else {
			prev := c.getEtcdClient()
			if prev != nil {
				prev.Close()
			}
			c.client.Store(client)
			c.logger.Printf("Using etcd endpoints %+v", endpoints)
			c.notifyListeners()
		}
	}

	return nil
}

func (c *etcdClient) Close() error {
	client := c.getEtcdClient()
	if client != nil {
		return client.Close()
	}

	return nil
}

func (c *etcdClient) IsConfigured() bool {
	return c.getEtcdClient() != nil
}

func (c *etcdClient) getEtcdClient() *clientv3.Client {
	client := c.client.Load()
	if client == nil {
		return nil
	}

	return client.(*clientv3.Client)
}

func (c *etcdClient) syncClient(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return c.getEtcdClient().Sync(ctx)
}

func (c *etcdClient) notifyListeners() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for listener := range c.listeners {
		listener.EtcdClientCreated(c)
	}
}

func (c *etcdClient) AddListener(listener ClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.listeners == nil {
		c.listeners = make(map[ClientListener]bool)
	}
	c.listeners[listener] = true
	if client := c.getEtcdClient(); client != nil {
		go listener.EtcdClientCreated(c)
	}
}

func (c *etcdClient) RemoveListener(listener ClientListener) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.listeners, listener)
}

func (c *etcdClient) WaitForConnection(ctx context.Context) error {
	backoff, err := async.NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
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
				c.logger.Printf("Timeout waiting for etcd client to connect to the cluster, retry in %s", backoff.NextWait())
			} else {
				c.logger.Printf("Could not sync etcd client with the cluster, retry in %s: %s", backoff.NextWait(), err)
			}

			backoff.Wait(ctx)
			continue
		}

		c.logger.Printf("Client synced, using endpoints %+v", c.getEtcdClient().Endpoints())
		return nil
	}
}

func (c *etcdClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return c.getEtcdClient().Get(ctx, key, opts...)
}

func (c *etcdClient) Watch(ctx context.Context, key string, nextRevision int64, watcher ClientWatcher, opts ...clientv3.OpOption) (int64, error) {
	c.logger.Printf("Wait for leader and start watching on %s (rev=%d)", key, nextRevision)
	opts = append(opts, clientv3.WithRev(nextRevision), clientv3.WithPrevKV())
	ch := c.getEtcdClient().Watch(clientv3.WithRequireLeader(ctx), key, opts...)
	c.logger.Printf("Watch created for %s", key)
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
				c.logger.Printf("Unsupported watch event %s %q -> %q", ev.Type, ev.Kv.Key, ev.Kv.Value)
			}
		}
	}

	return nextRevision, nil
}
