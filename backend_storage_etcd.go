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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type backendStorageEtcd struct {
	backendStorageCommon

	etcdClient *EtcdClient
	keyPrefix  string
	keyInfos   map[string]*BackendInformationEtcd

	initializedCtx       context.Context
	initializedFunc      context.CancelFunc
	wakeupChanForTesting chan struct{}

	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewBackendStorageEtcd(config *goconf.ConfigFile, etcdClient *EtcdClient) (BackendStorage, error) {
	if etcdClient == nil || !etcdClient.IsConfigured() {
		return nil, fmt.Errorf("no etcd endpoints configured")
	}

	keyPrefix, _ := config.GetString("backend", "backendprefix")
	if keyPrefix == "" {
		return nil, fmt.Errorf("no backend prefix configured")
	}

	initializedCtx, initializedFunc := context.WithCancel(context.Background())
	closeCtx, closeFunc := context.WithCancel(context.Background())
	result := &backendStorageEtcd{
		backendStorageCommon: backendStorageCommon{
			backends: make(map[string][]*Backend),
		},
		etcdClient: etcdClient,
		keyPrefix:  keyPrefix,
		keyInfos:   make(map[string]*BackendInformationEtcd),

		initializedCtx:  initializedCtx,
		initializedFunc: initializedFunc,
		closeCtx:        closeCtx,
		closeFunc:       closeFunc,
	}

	etcdClient.AddListener(result)
	return result, nil
}

func (s *backendStorageEtcd) WaitForInitialized(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.initializedCtx.Done():
		return nil
	}
}

func (s *backendStorageEtcd) wakeupForTesting() {
	if s.wakeupChanForTesting == nil {
		return
	}

	select {
	case s.wakeupChanForTesting <- struct{}{}:
	default:
	}
}

func (s *backendStorageEtcd) EtcdClientCreated(client *EtcdClient) {
	go func() {
		if err := client.WaitForConnection(s.closeCtx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			panic(err)
		}

		backoff, err := NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
		if err != nil {
			panic(err)
		}
		for s.closeCtx.Err() == nil {
			response, err := s.getBackends(s.closeCtx, client, s.keyPrefix)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				} else if errors.Is(err, context.DeadlineExceeded) {
					log.Printf("Timeout getting initial list of backends, retry in %s", backoff.NextWait())
				} else {
					log.Printf("Could not get initial list of backends, retry in %s: %s", backoff.NextWait(), err)
				}

				backoff.Wait(s.closeCtx)
				continue
			}

			for _, ev := range response.Kvs {
				s.EtcdKeyUpdated(client, string(ev.Key), ev.Value, nil)
			}
			s.initializedFunc()

			nextRevision := response.Header.Revision + 1
			prevRevision := nextRevision
			backoff.Reset()
			for s.closeCtx.Err() == nil {
				var err error
				if nextRevision, err = client.Watch(s.closeCtx, s.keyPrefix, nextRevision, s, clientv3.WithPrefix()); err != nil {
					log.Printf("Error processing watch for %s (%s), retry in %s", s.keyPrefix, err, backoff.NextWait())
					backoff.Wait(s.closeCtx)
					continue
				}

				if nextRevision != prevRevision {
					backoff.Reset()
					prevRevision = nextRevision
				} else {
					log.Printf("Processing watch for %s interrupted, retry in %s", s.keyPrefix, backoff.NextWait())
					backoff.Wait(s.closeCtx)
				}
			}
			return
		}
	}()
}

func (s *backendStorageEtcd) EtcdWatchCreated(client *EtcdClient, key string) {
}

func (s *backendStorageEtcd) getBackends(ctx context.Context, client *EtcdClient, keyPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return client.Get(ctx, keyPrefix, clientv3.WithPrefix())
}

func (s *backendStorageEtcd) EtcdKeyUpdated(client *EtcdClient, key string, data []byte, prevValue []byte) {
	var info BackendInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		log.Printf("Could not decode backend information %s: %s", string(data), err)
		return
	}
	if err := info.CheckValid(); err != nil {
		log.Printf("Received invalid backend information %s: %s", string(data), err)
		return
	}

	allowHttp := slices.ContainsFunc(info.parsedUrls, func(u *url.URL) bool {
		return u.Scheme == "http"
	})

	backend := &Backend{
		id:     key,
		urls:   info.Urls,
		secret: []byte(info.Secret),

		allowHttp: allowHttp,

		maxStreamBitrate: info.MaxStreamBitrate,
		maxScreenBitrate: info.MaxScreenBitrate,
		sessionLimit:     info.SessionLimit,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.keyInfos[key] = &info
	added := false
	for idx, u := range info.parsedUrls {
		host := u.Host
		entries, found := s.backends[host]
		if !found {
			// Simple case, first backend for this host
			log.Printf("Added backend %s (from %s)", info.Urls[idx], key)
			s.backends[host] = []*Backend{backend}
			added = true
			continue
		}

		// Was the backend changed?
		replaced := false
		for idx, entry := range entries {
			if entry.id == key {
				log.Printf("Updated backend %s (from %s)", info.Urls[idx], key)
				entries[idx] = backend
				replaced = true
				break
			}
		}

		if !replaced {
			// New backend, add to list.
			log.Printf("Added backend %s (from %s)", info.Urls[idx], key)
			s.backends[host] = append(entries, backend)
			added = true
		}
	}
	updateBackendStats(backend)
	if added {
		statsBackendsCurrent.Inc()
	}
	s.wakeupForTesting()
}

func (s *backendStorageEtcd) EtcdKeyDeleted(client *EtcdClient, key string, prevValue []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, found := s.keyInfos[key]
	if !found {
		return
	}

	delete(s.keyInfos, key)
	var deleted map[string][]*Backend
	seen := make(map[string]bool)
	for idx, u := range info.parsedUrls {
		host := u.Host
		entries, found := s.backends[host]
		if !found {
			if d, ok := deleted[host]; ok {
				if slices.ContainsFunc(d, func(b *Backend) bool {
					return slices.Contains(b.urls, u.String())
				}) {
					log.Printf("Removing backend %s (from %s)", info.Urls[idx], key)
				}
			}
			continue
		}

		log.Printf("Removing backend %s (from %s)", info.Urls[idx], key)
		newEntries := make([]*Backend, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.id == key {
				if len(info.parsedUrls) > 1 {
					if deleted == nil {
						deleted = make(map[string][]*Backend)
					}
					deleted[host] = append(deleted[host], entry)
				}
				if !seen[entry.Id()] {
					seen[entry.Id()] = true
					updateBackendStats(entry)
					statsBackendsCurrent.Dec()
				}
				continue
			}

			newEntries = append(newEntries, entry)
		}
		if len(newEntries) > 0 {
			s.backends[host] = newEntries
		} else {
			delete(s.backends, host)
		}
	}
	s.wakeupForTesting()
}

func (s *backendStorageEtcd) Close() {
	s.etcdClient.RemoveListener(s)
	s.closeFunc()
}

func (s *backendStorageEtcd) Reload(config *goconf.ConfigFile) {
	// Backend updates are processed through etcd.
}

func (s *backendStorageEtcd) GetCompatBackend() *Backend {
	return nil
}

func (s *backendStorageEtcd) GetBackend(u *url.URL) *Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getBackendLocked(u)
}
