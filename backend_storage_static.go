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
	"log"
	"net/url"
	"reflect"
	"strings"

	"github.com/dlintw/goconf"
)

type backendStorageStatic struct {
	backendStorageCommon

	// Deprecated
	allowAll      bool
	commonSecret  []byte
	compatBackend *Backend
}

func NewBackendStorageStatic(config *goconf.ConfigFile) (BackendStorage, error) {
	allowAll, _ := config.GetBool("backend", "allowall")
	allowHttp, _ := config.GetBool("backend", "allowhttp")
	commonSecret, _ := config.GetString("backend", "secret")
	sessionLimit, err := config.GetInt("backend", "sessionlimit")
	if err != nil || sessionLimit < 0 {
		sessionLimit = 0
	}
	backends := make(map[string][]*Backend)
	var compatBackend *Backend
	numBackends := 0
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
		numBackends++
	} else if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		for host, configuredBackends := range getConfiguredHosts(backendIds, config) {
			backends[host] = append(backends[host], configuredBackends...)
			for _, be := range configuredBackends {
				log.Printf("Backend %s added for %s", be.id, be.url)
			}
			numBackends += len(configuredBackends)
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
			numBackends++
		}
	}

	statsBackendsCurrent.Add(float64(numBackends))
	return &backendStorageStatic{
		backendStorageCommon: backendStorageCommon{
			backends: backends,
		},

		allowAll:      allowAll,
		commonSecret:  []byte(commonSecret),
		compatBackend: compatBackend,
	}, nil
}

func (s *backendStorageStatic) Close() {
}

func (s *backendStorageStatic) RemoveBackendsForHost(host string) {
	if oldBackends := s.backends[host]; len(oldBackends) > 0 {
		for _, backend := range oldBackends {
			log.Printf("Backend %s removed for %s", backend.id, backend.url)
		}
		statsBackendsCurrent.Sub(float64(len(oldBackends)))
	}
	delete(s.backends, host)
}

func (s *backendStorageStatic) UpsertHost(host string, backends []*Backend) {
	for existingIndex, existingBackend := range s.backends[host] {
		found := false
		index := 0
		for _, newBackend := range backends {
			if reflect.DeepEqual(existingBackend, newBackend) { // otherwise we could manually compare the struct members here
				found = true
				backends = append(backends[:index], backends[index+1:]...)
				break
			} else if newBackend.id == existingBackend.id {
				found = true
				s.backends[host][existingIndex] = newBackend
				backends = append(backends[:index], backends[index+1:]...)
				log.Printf("Backend %s updated for %s", newBackend.id, newBackend.url)
				break
			}
			index++
		}
		if !found {
			removed := s.backends[host][existingIndex]
			log.Printf("Backend %s removed for %s", removed.id, removed.url)
			s.backends[host] = append(s.backends[host][:existingIndex], s.backends[host][existingIndex+1:]...)
			statsBackendsCurrent.Dec()
		}
	}

	s.backends[host] = append(s.backends[host], backends...)
	for _, added := range backends {
		log.Printf("Backend %s added for %s", added.id, added.url)
	}
	statsBackendsCurrent.Add(float64(len(backends)))
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

func (s *backendStorageStatic) Reload(config *goconf.ConfigFile) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.compatBackend != nil {
		log.Println("Old-style configuration active, reload is not supported")
		return
	}

	if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		configuredHosts := getConfiguredHosts(backendIds, config)

		// remove backends that are no longer configured
		for hostname := range s.backends {
			if _, ok := configuredHosts[hostname]; !ok {
				s.RemoveBackendsForHost(hostname)
			}
		}

		// rewrite backends adding newly configured ones and rewriting existing ones
		for hostname, configuredBackends := range configuredHosts {
			s.UpsertHost(hostname, configuredBackends)
		}
	}
}

func (s *backendStorageStatic) GetCompatBackend() *Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.compatBackend
}

func (s *backendStorageStatic) GetBackend(u *url.URL) *Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, found := s.backends[u.Host]; !found {
		if s.allowAll {
			return s.compatBackend
		}
		return nil
	}

	return s.getBackendLocked(u)
}
