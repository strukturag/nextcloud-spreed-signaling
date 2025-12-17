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
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type backendStorageEtcd struct {
	backendStorageCommon

	logger     log.Logger
	etcdClient etcd.Client
	keyPrefix  string
	keyInfos   map[string]*etcd.BackendInformationEtcd

	initializing         atomic.Bool
	initializedCtx       context.Context
	initializedFunc      context.CancelFunc
	wakeupChanForTesting chan struct{}
	runningDone          sync.WaitGroup

	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewBackendStorageEtcd(logger log.Logger, config *goconf.ConfigFile, etcdClient etcd.Client, stats BackendStorageStats) (BackendStorage, error) {
	if etcdClient == nil || !etcdClient.IsConfigured() {
		return nil, errors.New("no etcd endpoints configured")
	}

	keyPrefix, _ := config.GetString("backend", "backendprefix")
	if keyPrefix == "" {
		return nil, errors.New("no backend prefix configured")
	}

	initializedCtx, initializedFunc := context.WithCancel(context.Background())
	closeCtx, closeFunc := context.WithCancel(context.Background())
	result := &backendStorageEtcd{
		backendStorageCommon: backendStorageCommon{
			backends: make(map[string][]*talk.Backend),
			stats:    stats,
		},
		logger:     logger,
		etcdClient: etcdClient,
		keyPrefix:  keyPrefix,
		keyInfos:   make(map[string]*etcd.BackendInformationEtcd),

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

func (s *backendStorageEtcd) EtcdClientCreated(client etcd.Client) {
	s.initializing.Store(true)
	if s.closeCtx.Err() != nil {
		// Stopped before etcd client was connected.
		s.initializedFunc()
		return
	}

	s.runningDone.Add(1)
	go func() {
		defer s.runningDone.Done()
		defer s.initializedFunc()
		if err := client.WaitForConnection(s.closeCtx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			panic(err)
		}

		backoff, err := async.NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
		if err != nil {
			panic(err)
		}
		for s.closeCtx.Err() == nil {
			response, err := s.getBackends(s.closeCtx, client, s.keyPrefix)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				} else if errors.Is(err, context.DeadlineExceeded) {
					s.logger.Printf("Timeout getting initial list of backends, retry in %s", backoff.NextWait())
				} else {
					s.logger.Printf("Could not get initial list of backends, retry in %s: %s", backoff.NextWait(), err)
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
					s.logger.Printf("Error processing watch for %s (%s), retry in %s", s.keyPrefix, err, backoff.NextWait())
					backoff.Wait(s.closeCtx)
					continue
				}

				if nextRevision != prevRevision {
					backoff.Reset()
					prevRevision = nextRevision
				} else {
					s.logger.Printf("Processing watch for %s interrupted, retry in %s", s.keyPrefix, backoff.NextWait())
					backoff.Wait(s.closeCtx)
				}
			}
			return
		}
	}()
}

func (s *backendStorageEtcd) EtcdWatchCreated(client etcd.Client, key string) {
}

func (s *backendStorageEtcd) getBackends(ctx context.Context, client etcd.Client, keyPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return client.Get(ctx, keyPrefix, clientv3.WithPrefix())
}

func (s *backendStorageEtcd) EtcdKeyUpdated(client etcd.Client, key string, data []byte, prevValue []byte) {
	var info etcd.BackendInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		s.logger.Printf("Could not decode backend information %s: %s", string(data), err)
		return
	}
	if err := info.CheckValid(); err != nil {
		s.logger.Printf("Received invalid backend information %s: %s", string(data), err)
		return
	}

	backend := talk.NewBackendFromEtcd(key, &info)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.keyInfos[key] = &info
	added := false
	for idx, u := range info.ParsedUrls {
		host := u.Host
		entries, found := s.backends[host]
		if !found {
			// Simple case, first backend for this host
			s.logger.Printf("Added backend %s (from %s)", info.Urls[idx], key)
			s.backends[host] = []*talk.Backend{backend}
			added = true
			continue
		}

		// Was the backend changed?
		replaced := false
		for idx, entry := range entries {
			if entry.Id() == key {
				s.logger.Printf("Updated backend %s (from %s)", info.Urls[idx], key)
				entries[idx] = backend
				replaced = true
				break
			}
		}

		if !replaced {
			// New backend, add to list.
			s.logger.Printf("Added backend %s (from %s)", info.Urls[idx], key)
			s.backends[host] = append(entries, backend)
			added = true
		}
	}
	backend.UpdateStats()
	if added {
		s.stats.IncBackends()
	}
	s.wakeupForTesting()
}

func (s *backendStorageEtcd) EtcdKeyDeleted(client etcd.Client, key string, prevValue []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, found := s.keyInfos[key]
	if !found {
		return
	}

	delete(s.keyInfos, key)
	var deleted map[string][]*talk.Backend
	seen := make(map[string]bool)
	for idx, u := range info.ParsedUrls {
		host := u.Host
		entries, found := s.backends[host]
		if !found {
			if d, ok := deleted[host]; ok {
				if slices.ContainsFunc(d, func(b *talk.Backend) bool {
					return slices.Contains(b.Urls(), u.String())
				}) {
					s.logger.Printf("Removing backend %s (from %s)", info.Urls[idx], key)
				}
			}
			continue
		}

		s.logger.Printf("Removing backend %s (from %s)", info.Urls[idx], key)
		newEntries := make([]*talk.Backend, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.Id() == key {
				if len(info.ParsedUrls) > 1 {
					if deleted == nil {
						deleted = make(map[string][]*talk.Backend)
					}
					deleted[host] = append(deleted[host], entry)
				}
				if !seen[entry.Id()] {
					seen[entry.Id()] = true
					entry.UpdateStats()
					s.stats.DecBackends()
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
	firstStop := s.closeCtx.Err() == nil
	s.closeFunc()
	s.etcdClient.RemoveListener(s)
	if firstStop {
		if s.initializing.Load() {
			<-s.initializedCtx.Done()
		}
		s.runningDone.Wait()
	}
}

func (s *backendStorageEtcd) Reload(config *goconf.ConfigFile) {
	// Backend updates are processed through etcd.
}

func (s *backendStorageEtcd) GetCompatBackend() *talk.Backend {
	return nil
}

func (s *backendStorageEtcd) GetBackend(u *url.URL) *talk.Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getBackendLocked(u)
}
