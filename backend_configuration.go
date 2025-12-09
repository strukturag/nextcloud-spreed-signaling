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
package signaling

import (
	"bytes"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	BackendTypeStatic = "static"
	BackendTypeEtcd   = "etcd"

	DefaultBackendType = BackendTypeStatic
)

var (
	SessionLimitExceeded = NewError("session_limit_exceeded", "Too many sessions connected for this backend.")
)

type Backend struct {
	id     string
	urls   []string
	secret []byte

	allowHttp bool

	maxStreamBitrate api.Bandwidth
	maxScreenBitrate api.Bandwidth

	sessionLimit uint64
	sessionsLock sync.Mutex
	// +checklocks:sessionsLock
	sessions map[PublicSessionId]bool

	counted bool
}

func (b *Backend) Id() string {
	return b.id
}

func (b *Backend) Secret() []byte {
	return b.secret
}

func (b *Backend) IsCompat() bool {
	return len(b.urls) == 0
}

func (b *Backend) Equal(other *Backend) bool {
	if b == other {
		return true
	} else if b == nil || other == nil {
		return false
	}

	return b.id == other.id &&
		b.allowHttp == other.allowHttp &&
		b.maxStreamBitrate == other.maxStreamBitrate &&
		b.maxScreenBitrate == other.maxScreenBitrate &&
		b.sessionLimit == other.sessionLimit &&
		bytes.Equal(b.secret, other.secret) &&
		slices.Equal(b.urls, other.urls)
}

func (b *Backend) IsUrlAllowed(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return b.allowHttp
	default:
		return false
	}
}

func (b *Backend) HasUrl(url string) bool {
	if b.IsCompat() {
		// Old-style configuration, only hosts are configured.
		return true
	}

	for _, u := range b.urls {
		if strings.HasPrefix(url, u) {
			return true
		}
	}

	return false
}

func (b *Backend) Urls() []string {
	return b.urls
}

func (b *Backend) Limit() int {
	return int(b.sessionLimit)
}

func (b *Backend) Len() int {
	b.sessionsLock.Lock()
	defer b.sessionsLock.Unlock()
	return len(b.sessions)
}

func (b *Backend) AddSession(session Session) error {
	if session.ClientType() == HelloClientTypeInternal || session.ClientType() == HelloClientTypeVirtual {
		// Internal and virtual sessions are not counting to the limit.
		return nil
	}

	if b.sessionLimit == 0 {
		// Not limited
		return nil
	}

	b.sessionsLock.Lock()
	defer b.sessionsLock.Unlock()
	if b.sessions == nil {
		b.sessions = make(map[PublicSessionId]bool)
	} else if uint64(len(b.sessions)) >= b.sessionLimit {
		statsBackendLimitExceededTotal.WithLabelValues(b.id).Inc()
		return SessionLimitExceeded
	}

	b.sessions[session.PublicId()] = true
	return nil
}

func (b *Backend) RemoveSession(session Session) {
	b.sessionsLock.Lock()
	defer b.sessionsLock.Unlock()

	delete(b.sessions, session.PublicId())
}

type BackendStorage interface {
	Close()
	Reload(config *goconf.ConfigFile)

	GetCompatBackend() *Backend
	GetBackend(u *url.URL) *Backend
	GetBackends() []*Backend
}

type BackendStorageStats interface {
	AddBackends(count int)
	RemoveBackends(count int)
	IncBackends()
	DecBackends()
}

type backendStorageCommon struct {
	mu sync.RWMutex
	// +checklocks:mu
	backends map[string][]*Backend

	stats BackendStorageStats // +checklocksignore: Only written to from constructor
}

func (s *backendStorageCommon) GetBackends() []*Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Backend
	for _, entries := range s.backends {
		result = append(result, entries...)
	}
	slices.SortFunc(result, func(a, b *Backend) int {
		return strings.Compare(a.Id(), b.Id())
	})
	result = slices.CompactFunc(result, func(a, b *Backend) bool {
		return a.Id() == b.Id()
	})
	return result
}

func (s *backendStorageCommon) getBackendLocked(u *url.URL) *Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, found := s.backends[u.Host]
	if !found {
		return nil
	}

	url := u.String()
	if url[len(url)-1] != '/' {
		url += "/"
	}
	for _, entry := range entries {
		if !entry.IsUrlAllowed(u) {
			continue
		}

		if entry.HasUrl(url) {
			return entry
		}
	}

	return nil
}

type BackendConfiguration struct {
	storage BackendStorage
}

type prometheusBackendStats struct{}

func (s *prometheusBackendStats) AddBackends(count int) {
	statsBackendsCurrent.Add(float64(count))
}

func (s *prometheusBackendStats) RemoveBackends(count int) {
	statsBackendsCurrent.Sub(float64(count))
}

func (s *prometheusBackendStats) IncBackends() {
	statsBackendsCurrent.Inc()
}

func (s *prometheusBackendStats) DecBackends() {
	statsBackendsCurrent.Dec()
}

var (
	defaultBackendStats = &prometheusBackendStats{}
)

func NewBackendConfiguration(logger log.Logger, config *goconf.ConfigFile, etcdClient *EtcdClient) (*BackendConfiguration, error) {
	return NewBackendConfigurationWithStats(logger, config, etcdClient, nil)
}

func NewBackendConfigurationWithStats(logger log.Logger, config *goconf.ConfigFile, etcdClient *EtcdClient, stats BackendStorageStats) (*BackendConfiguration, error) {
	backendType, _ := config.GetString("backend", "backendtype")
	if backendType == "" {
		backendType = DefaultBackendType
	}

	if stats == nil {
		RegisterBackendConfigurationStats()
		stats = defaultBackendStats
	}

	var storage BackendStorage
	var err error
	switch backendType {
	case BackendTypeStatic:
		storage, err = NewBackendStorageStatic(logger, config, stats)
	case BackendTypeEtcd:
		storage, err = NewBackendStorageEtcd(logger, config, etcdClient, stats)
	default:
		err = fmt.Errorf("unknown backend type: %s", backendType)
	}
	if err != nil {
		return nil, err
	}

	return &BackendConfiguration{
		storage: storage,
	}, nil
}

func (b *BackendConfiguration) Close() {
	b.storage.Close()
}

func (b *BackendConfiguration) Reload(config *goconf.ConfigFile) {
	b.storage.Reload(config)
}

func (b *BackendConfiguration) GetCompatBackend() *Backend {
	return b.storage.GetCompatBackend()
}

func (b *BackendConfiguration) GetBackend(u *url.URL) *Backend {
	u, _ = internal.CanonicalizeUrl(u)
	return b.storage.GetBackend(u)
}

func (b *BackendConfiguration) GetBackends() []*Backend {
	return b.storage.GetBackends()
}

func (b *BackendConfiguration) IsUrlAllowed(u *url.URL) bool {
	if u == nil {
		// Reject all invalid URLs.
		return false
	}

	backend := b.GetBackend(u)
	return backend != nil
}

func (b *BackendConfiguration) GetSecret(u *url.URL) []byte {
	if u == nil {
		// Reject all invalid URLs.
		return nil
	}

	entry := b.GetBackend(u)
	if entry == nil {
		return nil
	}

	return entry.Secret()
}
