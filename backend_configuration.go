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
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/dlintw/goconf"
	"go.uber.org/zap"
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
	id        string
	url       string
	parsedUrl *url.URL
	secret    []byte
	compat    bool

	allowHttp bool

	maxStreamBitrate int
	maxScreenBitrate int

	sessionLimit uint64
	sessionsLock sync.Mutex
	sessions     map[string]bool
}

func (b *Backend) Id() string {
	return b.id
}

func (b *Backend) Secret() []byte {
	return b.secret
}

func (b *Backend) IsCompat() bool {
	return b.compat
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

func (b *Backend) Url() string {
	return b.url
}

func (b *Backend) ParsedUrl() *url.URL {
	return b.parsedUrl
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
		b.sessions = make(map[string]bool)
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

type backendStorageCommon struct {
	log      *zap.Logger
	mu       sync.RWMutex
	backends map[string][]*Backend
}

func (s *backendStorageCommon) GetBackends() []*Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Backend
	for _, entries := range s.backends {
		result = append(result, entries...)
	}
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

		if entry.url == "" {
			// Old-style configuration, only hosts are configured.
			return entry
		} else if strings.HasPrefix(url, entry.url) {
			return entry
		}
	}

	return nil
}

type BackendConfiguration struct {
	storage BackendStorage
}

func NewBackendConfiguration(log *zap.Logger, config *goconf.ConfigFile, etcdClient *EtcdClient) (*BackendConfiguration, error) {
	backendType, _ := config.GetString("backend", "backendtype")
	if backendType == "" {
		backendType = DefaultBackendType
	}

	RegisterBackendConfigurationStats()

	var storage BackendStorage
	var err error
	switch backendType {
	case BackendTypeStatic:
		storage, err = NewBackendStorageStatic(log, config)
	case BackendTypeEtcd:
		storage, err = NewBackendStorageEtcd(log, config, etcdClient)
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
	if strings.Contains(u.Host, ":") && hasStandardPort(u) {
		u.Host = u.Hostname()
	}

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
