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
	"log"
	"net/url"
	"reflect"
	"strings"
	"sync"

	"github.com/dlintw/goconf"
)

var (
	SessionLimitExceeded = NewError("session_limit_exceeded", "Too many sessions connected for this backend.")
)

type Backend struct {
	id     string
	url    string
	secret []byte
	compat bool

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

type BackendConfiguration struct {
	backends map[string][]*Backend

	// Deprecated
	allowAll      bool
	commonSecret  []byte
	compatBackend *Backend
}

func NewBackendConfiguration(config *goconf.ConfigFile) (*BackendConfiguration, error) {
	allowAll, _ := config.GetBool("backend", "allowall")
	allowHttp, _ := config.GetBool("backend", "allowhttp")
	commonSecret, _ := config.GetString("backend", "secret")
	sessionLimit, err := config.GetInt("backend", "sessionlimit")
	if err != nil || sessionLimit < 0 {
		sessionLimit = 0
	}
	backends := make(map[string][]*Backend)
	var compatBackend *Backend
	if allowAll {
		log.Println("WARNING: All backend hostnames are allowed, only use for development!")
		compatBackend = &Backend{
			id:     "compat",
			secret: []byte(commonSecret),
			compat: true,

			allowHttp: allowHttp,

			sessionLimit: uint64(sessionLimit),
		}
		if sessionLimit > 0 {
			log.Printf("Allow a maximum of %d sessions", sessionLimit)
		}
	} else if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		for host, configuredBackends := range getConfiguredHosts(backendIds, config) {
			backends[host] = append(backends[host], configuredBackends...)
			for _, be := range configuredBackends {
				log.Printf("Backend %s added for %s", be.id, be.url)
			}
		}
	} else if allowedUrls, _ := config.GetString("backend", "allowed"); allowedUrls != "" {
		// Old-style configuration, only hosts are configured and are using a common secret.
		allowMap := make(map[string]bool)
		for _, u := range strings.Split(allowedUrls, ",") {
			u = strings.TrimSpace(u)
			if idx := strings.IndexByte(u, '/'); idx != -1 {
				log.Printf("WARNING: Removing path from allowed hostname \"%s\", check your configuration!", u)
				u = u[:idx]
			}
			if u != "" {
				allowMap[strings.ToLower(u)] = true
			}
		}

		if len(allowMap) == 0 {
			log.Println("WARNING: No backend hostnames are allowed, check your configuration!")
		} else {
			compatBackend = &Backend{
				id:     "compat",
				secret: []byte(commonSecret),
				compat: true,

				allowHttp: allowHttp,

				sessionLimit: uint64(sessionLimit),
			}
			hosts := make([]string, 0, len(allowMap))
			for host := range allowMap {
				hosts = append(hosts, host)
				backends[host] = []*Backend{compatBackend}
			}
			if len(hosts) > 1 {
				log.Println("WARNING: Using deprecated backend configuration. Please migrate the \"allowed\" setting to the new \"backends\" configuration.")
			}
			log.Printf("Allowed backend hostnames: %s", hosts)
			if sessionLimit > 0 {
				log.Printf("Allow a maximum of %d sessions", sessionLimit)
			}
		}
	}

	return &BackendConfiguration{
		backends: backends,

		allowAll:      allowAll,
		commonSecret:  []byte(commonSecret),
		compatBackend: compatBackend,
	}, nil
}

func (b *BackendConfiguration) RemoveBackendsForHost(host string) {
	if oldBackends := b.backends[host]; len(oldBackends) > 0 {
		for _, backend := range oldBackends {
			log.Printf("Backend %s removed for %s", backend.id, backend.url)
		}
	}
	delete(b.backends, host)
}

func (b *BackendConfiguration) UpsertHost(host string, backends []*Backend) {
	for existingIndex, existingBackend := range b.backends[host] {
		found := false
		index := 0
		for _, newBackend := range backends {
			if reflect.DeepEqual(existingBackend, newBackend) { // otherwise we could manually compare the struct members here
				found = true
				backends = append(backends[:index], backends[index+1:]...)
				break
			} else if newBackend.id == existingBackend.id {
				found = true
				b.backends[host][existingIndex] = newBackend
				backends = append(backends[:index], backends[index+1:]...)
				log.Printf("Backend %s updated for %s", newBackend.id, newBackend.url)
				break
			}
			index++
		}
		if !found {
			removed := b.backends[host][existingIndex]
			log.Printf("Backend %s removed for %s", removed.id, removed.url)
			b.backends[host] = append(b.backends[host][:existingIndex], b.backends[host][existingIndex+1:]...)
		}
	}

	b.backends[host] = append(b.backends[host], backends...)
	for _, added := range backends {
		log.Printf("Backend %s added for %s", added.id, added.url)
	}
}

func getConfiguredBackendIDs(backendIds string) (ids []string) {
	seen := make(map[string]bool)

	for _, id := range strings.Split(backendIds, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		if seen[id] {
			continue
		}
		ids = append(ids, id)
		seen[id] = true
	}

	return ids
}

func getConfiguredHosts(backendIds string, config *goconf.ConfigFile) (hosts map[string][]*Backend) {
	hosts = make(map[string][]*Backend)
	for _, id := range getConfiguredBackendIDs(backendIds) {
		u, _ := config.GetString(id, "url")
		if u == "" {
			log.Printf("Backend %s is missing or incomplete, skipping", id)
			continue
		}

		if u[len(u)-1] != '/' {
			u += "/"
		}
		parsed, err := url.Parse(u)
		if err != nil {
			log.Printf("Backend %s has an invalid url %s configured (%s), skipping", id, u, err)
			continue
		}

		if strings.Contains(parsed.Host, ":") && hasStandardPort(parsed) {
			parsed.Host = parsed.Hostname()
			u = parsed.String()
		}

		secret, _ := config.GetString(id, "secret")
		if u == "" || secret == "" {
			log.Printf("Backend %s is missing or incomplete, skipping", id)
			continue
		}

		sessionLimit, err := config.GetInt(id, "sessionlimit")
		if err != nil || sessionLimit < 0 {
			sessionLimit = 0
		}
		if sessionLimit > 0 {
			log.Printf("Backend %s allows a maximum of %d sessions", id, sessionLimit)
		}

		maxStreamBitrate, err := config.GetInt(id, "maxstreambitrate")
		if err != nil || maxStreamBitrate < 0 {
			maxStreamBitrate = 0
		}
		maxScreenBitrate, err := config.GetInt(id, "maxscreenbitrate")
		if err != nil || maxScreenBitrate < 0 {
			maxScreenBitrate = 0
		}

		hosts[parsed.Host] = append(hosts[parsed.Host], &Backend{
			id:     id,
			url:    u,
			secret: []byte(secret),

			allowHttp: parsed.Scheme == "http",

			maxStreamBitrate: maxStreamBitrate,
			maxScreenBitrate: maxScreenBitrate,

			sessionLimit: uint64(sessionLimit),
		})
	}

	return hosts
}

func (b *BackendConfiguration) Reload(config *goconf.ConfigFile) {
	if b.compatBackend != nil {
		log.Println("Old-style configuration active, reload is not supported")
		return
	}

	if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		configuredHosts := getConfiguredHosts(backendIds, config)

		// remove backends that are no longer configured
		for hostname := range b.backends {
			if _, ok := configuredHosts[hostname]; !ok {
				b.RemoveBackendsForHost(hostname)
			}
		}

		// rewrite backends adding newly configured ones and rewriting existing ones
		for hostname, configuredBackends := range configuredHosts {
			b.UpsertHost(hostname, configuredBackends)
		}
	}
}

func (b *BackendConfiguration) GetCompatBackend() *Backend {
	return b.compatBackend
}

func (b *BackendConfiguration) GetBackend(u *url.URL) *Backend {
	if strings.Contains(u.Host, ":") && hasStandardPort(u) {
		u.Host = u.Hostname()
	}

	entries, found := b.backends[u.Host]
	if !found {
		if b.allowAll {
			return b.compatBackend
		}
		return nil
	}

	s := u.String()
	if s[len(s)-1] != '/' {
		s += "/"
	}
	for _, entry := range entries {
		if !entry.IsUrlAllowed(u) {
			continue
		}

		if entry.url == "" {
			// Old-style configuration, only hosts are configured.
			return entry
		} else if strings.HasPrefix(s, entry.url) {
			return entry
		}
	}

	return nil
}

func (b *BackendConfiguration) GetBackends() []*Backend {
	var result []*Backend
	for _, entries := range b.backends {
		result = append(result, entries...)
	}
	return result
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

	return entry.secret
}
