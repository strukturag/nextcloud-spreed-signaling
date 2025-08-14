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
	"slices"
	"strings"

	"github.com/dlintw/goconf"
)

type backendStorageStatic struct {
	backendStorageCommon

	backendsById map[string]*Backend

	// Deprecated
	allowAll      bool
	commonSecret  []byte
	compatBackend *Backend
}

func NewBackendStorageStatic(config *goconf.ConfigFile) (BackendStorage, error) {
	allowAll, _ := config.GetBool("backend", "allowall")
	allowHttp, _ := config.GetBool("backend", "allowhttp")
	commonSecret, _ := GetStringOptionWithEnv(config, "backend", "secret")
	sessionLimit, err := config.GetInt("backend", "sessionlimit")
	if err != nil || sessionLimit < 0 {
		sessionLimit = 0
	}
	backends := make(map[string][]*Backend)
	backendsById := make(map[string]*Backend)
	var compatBackend *Backend
	numBackends := 0
	if allowAll {
		log.Println("WARNING: All backend hostnames are allowed, only use for development!")
		compatBackend = &Backend{
			id:     "compat",
			secret: []byte(commonSecret),

			allowHttp: allowHttp,

			sessionLimit: uint64(sessionLimit),
			counted:      true,
		}
		if sessionLimit > 0 {
			log.Printf("Allow a maximum of %d sessions", sessionLimit)
		}
		updateBackendStats(compatBackend)
		backendsById[compatBackend.id] = compatBackend
		numBackends++
	} else if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		added := make(map[string]*Backend)
		for host, configuredBackends := range getConfiguredHosts(backendIds, config, commonSecret) {
			backends[host] = append(backends[host], configuredBackends...)
			for _, be := range configuredBackends {
				added[be.id] = be
			}
		}
		for _, be := range added {
			log.Printf("Backend %s added for %s", be.id, strings.Join(be.urls, ", "))
			backendsById[be.id] = be
			updateBackendStats(be)
			be.counted = true
		}
		numBackends += len(added)
	} else if allowedUrls, _ := config.GetString("backend", "allowed"); allowedUrls != "" {
		// Old-style configuration, only hosts are configured and are using a common secret.
		allowMap := make(map[string]bool)
		for u := range strings.SplitSeq(allowedUrls, ",") {
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

				allowHttp: allowHttp,

				sessionLimit: uint64(sessionLimit),
				counted:      true,
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
			updateBackendStats(compatBackend)
			backendsById[compatBackend.id] = compatBackend
			numBackends++
		}
	}

	if numBackends == 0 {
		log.Printf("WARNING: No backends configured, client connections will not be possible.")
	}

	statsBackendsCurrent.Add(float64(numBackends))
	return &backendStorageStatic{
		backendStorageCommon: backendStorageCommon{
			backends: backends,
		},

		backendsById: backendsById,

		allowAll:      allowAll,
		commonSecret:  []byte(commonSecret),
		compatBackend: compatBackend,
	}, nil
}

func (s *backendStorageStatic) Close() {
}

func (s *backendStorageStatic) RemoveBackendsForHost(host string, seen map[string]seenState) {
	if oldBackends := s.backends[host]; len(oldBackends) > 0 {
		deleted := 0
		for _, backend := range oldBackends {
			if seen[backend.Id()] == seenDeleted {
				continue
			}

			seen[backend.Id()] = seenDeleted
			urls := filter(backend.urls, func(s string) bool {
				return !strings.Contains(s, "://"+host)
			})
			log.Printf("Backend %s removed for %s", backend.id, strings.Join(urls, ", "))
			if len(urls) == len(backend.urls) && backend.counted {
				deleteBackendStats(backend)
				delete(s.backendsById, backend.Id())
				deleted++
				backend.counted = false
			}
		}
		statsBackendsCurrent.Sub(float64(deleted))
	}
	delete(s.backends, host)
}

func filter[T any](s []T, del func(T) bool) []T {
	result := make([]T, 0, len(s))
	for _, e := range s {
		if !del(e) {
			result = append(result, e)
		}
	}
	return result
}

type seenState int

const (
	seenNotSeen seenState = iota
	seenAdded
	seenUpdated
	seenDeleted
)

func (s *backendStorageStatic) UpsertHost(host string, backends []*Backend, seen map[string]seenState) {
	for existingIndex, existingBackend := range s.backends[host] {
		found := false
		index := 0
		for _, newBackend := range backends {
			if existingBackend.Equal(newBackend) {
				found = true
				backends = append(backends[:index], backends[index+1:]...)
				break
			} else if newBackend.id == existingBackend.id {
				found = true
				s.backends[host][existingIndex] = newBackend
				backends = append(backends[:index], backends[index+1:]...)
				if seen[newBackend.id] != seenUpdated {
					seen[newBackend.id] = seenUpdated
					log.Printf("Backend %s updated for %s", newBackend.id, strings.Join(newBackend.urls, ", "))
					updateBackendStats(newBackend)
					newBackend.counted = existingBackend.counted
					s.backendsById[newBackend.id] = newBackend
				}
				break
			}
			index++
		}
		if !found {
			removed := s.backends[host][existingIndex]
			s.backends[host] = append(s.backends[host][:existingIndex], s.backends[host][existingIndex+1:]...)
			if seen[removed.id] != seenDeleted {
				seen[removed.id] = seenDeleted
				urls := filter(removed.urls, func(s string) bool {
					return !strings.Contains(s, "://"+host)
				})
				log.Printf("Backend %s removed for %s", removed.id, strings.Join(urls, ", "))
				if len(urls) == len(removed.urls) && removed.counted {
					deleteBackendStats(removed)
					delete(s.backendsById, removed.Id())
					statsBackendsCurrent.Dec()
					removed.counted = false
				}
			}
		}
	}

	s.backends[host] = append(s.backends[host], backends...)

	addedBackends := 0
	for _, added := range backends {
		if seen[added.id] == seenAdded {
			continue
		}

		seen[added.id] = seenAdded
		if prev, found := s.backendsById[added.id]; found {
			added.counted = prev.counted
		} else {
			s.backendsById[added.id] = added
		}

		log.Printf("Backend %s added for %s", added.id, strings.Join(added.urls, ", "))
		if !added.counted {
			updateBackendStats(added)
			addedBackends++
			added.counted = true
		}
	}
	statsBackendsCurrent.Add(float64(addedBackends))
}

func getConfiguredBackendIDs(backendIds string) (ids []string) {
	seen := make(map[string]bool)

	for id := range strings.SplitSeq(backendIds, ",") {
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

func MapIf[T any](s []T, f func(T) (T, bool)) []T {
	result := make([]T, 0, len(s))
	for _, v := range s {
		if v, ok := f(v); ok {
			result = append(result, v)
		}
	}
	return result
}

func getConfiguredHosts(backendIds string, config *goconf.ConfigFile, commonSecret string) (hosts map[string][]*Backend) {
	hosts = make(map[string][]*Backend)
	seenUrls := make(map[string]string)
	for _, id := range getConfiguredBackendIDs(backendIds) {
		secret, _ := GetStringOptionWithEnv(config, id, "secret")
		if secret == "" && commonSecret != "" {
			log.Printf("Backend %s has no own shared secret set, using common shared secret", id)
			secret = commonSecret
		}
		if secret == "" {
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

		var urls []string
		if u, _ := GetStringOptionWithEnv(config, id, "urls"); u != "" {
			urls = strings.Split(u, ",")
			urls = MapIf(urls, func(s string) (string, bool) {
				s = strings.TrimSpace(s)
				return s, len(s) > 0
			})
			slices.Sort(urls)
			urls = slices.Compact(urls)
		} else if u, _ := GetStringOptionWithEnv(config, id, "url"); u != "" {
			if u = strings.TrimSpace(u); u != "" {
				urls = []string{u}
			}
		}

		if len(urls) == 0 {
			log.Printf("Backend %s is missing or incomplete, skipping", id)
			continue
		}

		backend := &Backend{
			id:     id,
			secret: []byte(secret),

			maxStreamBitrate: maxStreamBitrate,
			maxScreenBitrate: maxScreenBitrate,

			sessionLimit: uint64(sessionLimit),
		}

		added := make(map[string]bool)
		for _, u := range urls {
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

			if prev, found := seenUrls[u]; found {
				log.Printf("Url %s in backend %s was already used in backend %s, skipping", u, id, prev)
				continue
			}

			seenUrls[u] = id
			backend.urls = append(backend.urls, u)
			if parsed.Scheme == "http" {
				backend.allowHttp = true
			}

			if !added[parsed.Host] {
				hosts[parsed.Host] = append(hosts[parsed.Host], backend)
				added[parsed.Host] = true
			}
		}
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

	commonSecret, _ := GetStringOptionWithEnv(config, "backend", "secret")

	if backendIds, _ := config.GetString("backend", "backends"); backendIds != "" {
		configuredHosts := getConfiguredHosts(backendIds, config, commonSecret)

		// remove backends that are no longer configured
		seen := make(map[string]seenState)
		for hostname := range s.backends {
			if _, ok := configuredHosts[hostname]; !ok {
				s.RemoveBackendsForHost(hostname, seen)
			}
		}

		// rewrite backends adding newly configured ones and rewriting existing ones
		for hostname, configuredBackends := range configuredHosts {
			s.UpsertHost(hostname, configuredBackends, seen)
		}
	} else {
		// remove all backends
		seen := make(map[string]seenState)
		for hostname := range s.backends {
			s.RemoveBackendsForHost(hostname, seen)
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
