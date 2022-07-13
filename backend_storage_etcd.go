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
	"fmt"
	"log"
	"net/url"
	"sync"
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
	initializedWg        sync.WaitGroup
	wakeupChanForTesting chan bool
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
	result := &backendStorageEtcd{
		backendStorageCommon: backendStorageCommon{
			backends: make(map[string][]*Backend),
		},
		etcdClient: etcdClient,
		keyPrefix:  keyPrefix,
		keyInfos:   make(map[string]*BackendInformationEtcd),

		initializedCtx:  initializedCtx,
		initializedFunc: initializedFunc,
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

func (s *backendStorageEtcd) SetWakeupForTesting(ch chan bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.wakeupChanForTesting = ch
}

func (s *backendStorageEtcd) wakeupForTesting() {
	if s.wakeupChanForTesting == nil {
		return
	}

	select {
	case s.wakeupChanForTesting <- true:
	default:
	}
}

func (s *backendStorageEtcd) EtcdClientCreated(client *EtcdClient) {
	s.initializedWg.Add(1)
	go func() {
		if err := client.Watch(context.Background(), s.keyPrefix, s, clientv3.WithPrefix()); err != nil {
			log.Printf("Error processing watch for %s: %s", s.keyPrefix, err)
		}
	}()

	go func() {
		client.WaitForConnection()

		waitDelay := initialWaitDelay
		for {
			response, err := s.getBackends(client, s.keyPrefix)
			if err != nil {
				if err == context.DeadlineExceeded {
					log.Printf("Timeout getting initial list of backends, retry in %s", waitDelay)
				} else {
					log.Printf("Could not get initial list of backends, retry in %s: %s", waitDelay, err)
				}

				time.Sleep(waitDelay)
				waitDelay = waitDelay * 2
				if waitDelay > maxWaitDelay {
					waitDelay = maxWaitDelay
				}
				continue
			}

			for _, ev := range response.Kvs {
				s.EtcdKeyUpdated(client, string(ev.Key), ev.Value)
			}
			s.initializedWg.Wait()
			s.initializedFunc()
			return
		}
	}()
}

func (s *backendStorageEtcd) EtcdWatchCreated(client *EtcdClient, key string) {
	s.initializedWg.Done()
}

func (s *backendStorageEtcd) getBackends(client *EtcdClient, keyPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	return client.Get(ctx, keyPrefix, clientv3.WithPrefix())
}

func (s *backendStorageEtcd) EtcdKeyUpdated(client *EtcdClient, key string, data []byte) {
	var info BackendInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		log.Printf("Could not decode backend information %s: %s", string(data), err)
		return
	}
	if err := info.CheckValid(); err != nil {
		log.Printf("Received invalid backend information %s: %s", string(data), err)
		return
	}

	backend := &Backend{
		id:        key,
		url:       info.Url,
		parsedUrl: info.parsedUrl,
		secret:    []byte(info.Secret),

		allowHttp: info.parsedUrl.Scheme == "http",

		maxStreamBitrate: info.MaxStreamBitrate,
		maxScreenBitrate: info.MaxScreenBitrate,
		sessionLimit:     info.SessionLimit,
	}

	host := info.parsedUrl.Host

	s.mu.Lock()
	defer s.mu.Unlock()

	s.keyInfos[key] = &info
	entries, found := s.backends[host]
	if !found {
		// Simple case, first backend for this host
		log.Printf("Added backend %s (from %s)", info.Url, key)
		s.backends[host] = []*Backend{backend}
		statsBackendsCurrent.Inc()
		s.wakeupForTesting()
		return
	}

	// Was the backend changed?
	replaced := false
	for idx, entry := range entries {
		if entry.id == key {
			log.Printf("Updated backend %s (from %s)", info.Url, key)
			entries[idx] = backend
			replaced = true
			break
		}
	}

	if !replaced {
		// New backend, add to list.
		log.Printf("Added backend %s (from %s)", info.Url, key)
		s.backends[host] = append(entries, backend)
		statsBackendsCurrent.Inc()
	}
	s.wakeupForTesting()
}

func (s *backendStorageEtcd) EtcdKeyDeleted(client *EtcdClient, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, found := s.keyInfos[key]
	if !found {
		return
	}

	delete(s.keyInfos, key)
	host := info.parsedUrl.Host
	entries, found := s.backends[host]
	if !found {
		return
	}

	log.Printf("Removing backend %s (from %s)", info.Url, key)
	newEntries := make([]*Backend, 0, len(entries)-1)
	for _, entry := range entries {
		if entry.id == key {
			statsBackendsCurrent.Dec()
			continue
		}

		newEntries = append(newEntries, entry)
	}
	if len(newEntries) > 0 {
		s.backends[host] = newEntries
	} else {
		delete(s.backends, host)
	}
	s.wakeupForTesting()
}

func (s *backendStorageEtcd) Close() {
	s.etcdClient.RemoveListener(s)
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
