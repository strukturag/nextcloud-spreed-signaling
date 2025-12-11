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
package talk

import (
	"bytes"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

var (
	SessionLimitExceeded = api.NewError("session_limit_exceeded", "Too many sessions connected for this backend.") // +checklocksignore: Global readonly variable.
)

func init() {
	registerBackendStats()
}

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
	sessions map[api.PublicSessionId]bool

	counted bool
}

func NewCompatBackend(cfg *goconf.ConfigFile) *Backend {
	if cfg == nil {
		return &Backend{
			id: "compat",
		}
	}

	allowHttp, _ := cfg.GetBool("backend", "allowhttp")
	commonSecret, _ := config.GetStringOptionWithEnv(cfg, "backend", "secret")
	sessionLimit, err := cfg.GetInt("backend", "sessionlimit")
	if err != nil || sessionLimit < 0 {
		sessionLimit = 0
	}
	maxStreamBitrate, err := cfg.GetInt("backend", "maxstreambitrate")
	if err != nil || maxStreamBitrate < 0 {
		maxStreamBitrate = 0
	}
	maxScreenBitrate, err := cfg.GetInt("backend", "maxscreenbitrate")
	if err != nil || maxScreenBitrate < 0 {
		maxScreenBitrate = 0
	}

	return &Backend{
		id:     "compat",
		secret: []byte(commonSecret),

		allowHttp: allowHttp,

		sessionLimit: uint64(sessionLimit),
		counted:      true,

		maxStreamBitrate: api.BandwidthFromBits(uint64(maxStreamBitrate)),
		maxScreenBitrate: api.BandwidthFromBits(uint64(maxScreenBitrate)),
	}
}

func NewBackendFromConfig(logger log.Logger, id string, cfg *goconf.ConfigFile, commonSecret string) (*Backend, error) {
	secret, _ := config.GetStringOptionWithEnv(cfg, id, "secret")
	if secret == "" && commonSecret != "" {
		logger.Printf("Backend %s has no own shared secret set, using common shared secret", id)
		secret = commonSecret
	}
	if secret == "" {
		return nil, fmt.Errorf("backend %s is missing or incomplete, skipping", id)
	}

	sessionLimit, err := cfg.GetInt(id, "sessionlimit")
	if err != nil || sessionLimit < 0 {
		sessionLimit = 0
	}
	maxStreamBitrate, err := cfg.GetInt(id, "maxstreambitrate")
	if err != nil || maxStreamBitrate < 0 {
		maxStreamBitrate = 0
	}
	maxScreenBitrate, err := cfg.GetInt(id, "maxscreenbitrate")
	if err != nil || maxScreenBitrate < 0 {
		maxScreenBitrate = 0
	}

	return &Backend{
		id:     id,
		secret: []byte(secret),

		maxStreamBitrate: api.BandwidthFromBits(uint64(maxStreamBitrate)),
		maxScreenBitrate: api.BandwidthFromBits(uint64(maxScreenBitrate)),

		sessionLimit: uint64(sessionLimit),
	}, nil
}

func NewBackendFromEtcd(key string, info *etcd.BackendInformationEtcd) *Backend {
	allowHttp := slices.ContainsFunc(info.ParsedUrls, func(u *url.URL) bool {
		return u.Scheme == "http"
	})

	return &Backend{
		id:     key,
		urls:   info.Urls,
		secret: []byte(info.Secret),

		allowHttp: allowHttp,

		maxStreamBitrate: info.MaxStreamBitrate,
		maxScreenBitrate: info.MaxScreenBitrate,
		sessionLimit:     info.SessionLimit,
	}
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

func (b *Backend) AddUrl(u *url.URL) {
	b.urls = append(b.urls, u.String())
	if u.Scheme == "http" {
		b.allowHttp = true
	}
}

func (b *Backend) Limit() int {
	return int(b.sessionLimit)
}

func (b *Backend) SetMaxStreamBitrate(bitrate api.Bandwidth) {
	b.maxStreamBitrate = bitrate
}

func (b *Backend) MaxStreamBitrate() api.Bandwidth {
	return b.maxStreamBitrate
}

func (b *Backend) SetMaxScreenBitrate(bitrate api.Bandwidth) {
	b.maxScreenBitrate = bitrate
}

func (b *Backend) MaxScreenBitrate() api.Bandwidth {
	return b.maxScreenBitrate
}

func (b *Backend) CopyCount(other *Backend) {
	b.counted = other.counted
}

func (b *Backend) Count() bool {
	if b.counted {
		return false
	}

	b.counted = true
	return true
}

func (b *Backend) Uncount() bool {
	if !b.counted {
		return false
	}

	b.counted = false
	return true
}

func (b *Backend) Len() int {
	b.sessionsLock.Lock()
	defer b.sessionsLock.Unlock()
	return len(b.sessions)
}

type BackendSession interface {
	PublicId() api.PublicSessionId
	ClientType() api.ClientType
}

func (b *Backend) AddSession(session BackendSession) error {
	if session.ClientType() == api.HelloClientTypeInternal || session.ClientType() == api.HelloClientTypeVirtual {
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
		b.sessions = make(map[api.PublicSessionId]bool)
	} else if uint64(len(b.sessions)) >= b.sessionLimit {
		registerBackendStats()
		statsBackendLimitExceededTotal.WithLabelValues(b.id).Inc()
		return SessionLimitExceeded
	}

	b.sessions[session.PublicId()] = true
	return nil
}

func (b *Backend) RemoveSession(session BackendSession) {
	b.sessionsLock.Lock()
	defer b.sessionsLock.Unlock()

	delete(b.sessions, session.PublicId())
}

func (b *Backend) UpdateStats() {
	if b.sessionLimit > 0 {
		statsBackendLimit.WithLabelValues(b.id).Set(float64(b.sessionLimit))
	} else {
		statsBackendLimit.DeleteLabelValues(b.id)
	}
}

func (b *Backend) DeleteStats() {
	statsBackendLimit.DeleteLabelValues(b.id)
}
