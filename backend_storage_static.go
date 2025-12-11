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
	"net/url"
	"slices"
	"strings"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type backendStorageStatic struct {
	backendStorageCommon

	logger       log.Logger
	backendsById map[string]*talk.Backend

	// Deprecated
	allowAll      bool
	commonSecret  []byte
	compatBackend *talk.Backend
}

func NewBackendStorageStatic(logger log.Logger, cfg *goconf.ConfigFile, stats BackendStorageStats) (BackendStorage, error) {
	allowAll, _ := cfg.GetBool("backend", "allowall")
	commonSecret, _ := config.GetStringOptionWithEnv(cfg, "backend", "secret")
	backends := make(map[string][]*talk.Backend)
	backendsById := make(map[string]*talk.Backend)
	var compatBackend *talk.Backend
	numBackends := 0
	if allowAll {
		logger.Println("WARNING: All backend hostnames are allowed, only use for development!")
		compatBackend = talk.NewCompatBackend(cfg)
		if sessionLimit := compatBackend.Limit(); sessionLimit > 0 {
			logger.Printf("Allow a maximum of %d sessions", sessionLimit)
		}
		compatBackend.UpdateStats()
		backendsById[compatBackend.Id()] = compatBackend
		numBackends++
	} else if backendIds, _ := cfg.GetString("backend", "backends"); backendIds != "" {
		added := make(map[string]*talk.Backend)
		for host, configuredBackends := range getConfiguredHosts(logger, backendIds, cfg, commonSecret) {
			backends[host] = append(backends[host], configuredBackends...)
			for _, be := range configuredBackends {
				added[be.Id()] = be
			}
		}
		for _, be := range added {
			logger.Printf("Backend %s added for %s", be.Id(), strings.Join(be.Urls(), ", "))
			backendsById[be.Id()] = be
			be.UpdateStats()
			be.Count()
		}
		numBackends += len(added)
	} else if allowedUrls, _ := cfg.GetString("backend", "allowed"); allowedUrls != "" {
		// Old-style configuration, only hosts are configured and are using a common secret.
		allowMap := make(map[string]bool)
		for u := range internal.SplitEntries(allowedUrls, ",") {
			if idx := strings.IndexByte(u, '/'); idx != -1 {
				logger.Printf("WARNING: Removing path from allowed hostname \"%s\", check your configuration!", u)
				if u = u[:idx]; u == "" {
					continue
				}
			}

			allowMap[strings.ToLower(u)] = true
		}

		if len(allowMap) == 0 {
			logger.Println("WARNING: No backend hostnames are allowed, check your configuration!")
		} else {
			compatBackend = talk.NewCompatBackend(cfg)
			hosts := make([]string, 0, len(allowMap))
			for host := range allowMap {
				hosts = append(hosts, host)
				backends[host] = []*talk.Backend{compatBackend}
			}
			if len(hosts) > 1 {
				logger.Println("WARNING: Using deprecated backend configuration. Please migrate the \"allowed\" setting to the new \"backends\" configuration.")
			}
			logger.Printf("Allowed backend hostnames: %s", hosts)
			if sessionLimit := compatBackend.Limit(); sessionLimit > 0 {
				logger.Printf("Allow a maximum of %d sessions", sessionLimit)
			}
			compatBackend.UpdateStats()
			backendsById[compatBackend.Id()] = compatBackend
			numBackends++
		}
	}

	if numBackends == 0 {
		logger.Printf("WARNING: No backends configured, client connections will not be possible.")
	}

	stats.AddBackends(numBackends)
	return &backendStorageStatic{
		backendStorageCommon: backendStorageCommon{
			backends: backends,
			stats:    stats,
		},

		logger:       logger,
		backendsById: backendsById,

		allowAll:      allowAll,
		commonSecret:  []byte(commonSecret),
		compatBackend: compatBackend,
	}, nil
}

func (s *backendStorageStatic) Close() {
}

// +checklocks:s.mu
func (s *backendStorageStatic) RemoveBackendsForHost(host string, seen map[string]seenState) {
	if oldBackends := s.backends[host]; len(oldBackends) > 0 {
		deleted := 0
		for _, backend := range oldBackends {
			if seen[backend.Id()] == seenDeleted {
				continue
			}

			seen[backend.Id()] = seenDeleted
			urls := slices.DeleteFunc(backend.Urls(), func(s string) bool {
				return !strings.Contains(s, "://"+host)
			})
			s.logger.Printf("Backend %s removed for %s", backend.Id(), strings.Join(urls, ", "))
			if len(urls) == len(backend.Urls()) && backend.Uncount() {
				backend.DeleteStats()
				delete(s.backendsById, backend.Id())
				deleted++
			}
		}
		s.stats.RemoveBackends(deleted)
	}
	delete(s.backends, host)
}

type seenState int

const (
	seenNotSeen seenState = iota
	seenAdded
	seenUpdated
	seenDeleted
)

// +checklocks:s.mu
func (s *backendStorageStatic) UpsertHost(host string, backends []*talk.Backend, seen map[string]seenState) {
	for existingIndex, existingBackend := range s.backends[host] {
		found := false
		index := 0
		for _, newBackend := range backends {
			if existingBackend.Equal(newBackend) {
				found = true
				backends = slices.Delete(backends, index, index+1)
				break
			} else if newBackend.Id() == existingBackend.Id() {
				found = true
				s.backends[host][existingIndex] = newBackend
				backends = slices.Delete(backends, index, index+1)
				if seen[newBackend.Id()] != seenUpdated {
					seen[newBackend.Id()] = seenUpdated
					s.logger.Printf("Backend %s updated for %s", newBackend.Id(), strings.Join(newBackend.Urls(), ", "))
					newBackend.UpdateStats()
					newBackend.CopyCount(existingBackend)
					s.backendsById[newBackend.Id()] = newBackend
				}
				break
			}
			index++
		}
		if !found {
			removed := s.backends[host][existingIndex]
			s.backends[host] = slices.Delete(s.backends[host], existingIndex, existingIndex+1)
			if seen[removed.Id()] != seenDeleted {
				seen[removed.Id()] = seenDeleted
				urls := slices.DeleteFunc(removed.Urls(), func(s string) bool {
					return !strings.Contains(s, "://"+host)
				})
				s.logger.Printf("Backend %s removed for %s", removed.Id(), strings.Join(urls, ", "))
				if len(urls) == len(removed.Urls()) && removed.Uncount() {
					removed.DeleteStats()
					delete(s.backendsById, removed.Id())
					s.stats.DecBackends()
				}
			}
		}
	}

	s.backends[host] = append(s.backends[host], backends...)

	addedBackends := 0
	for _, added := range backends {
		if seen[added.Id()] == seenAdded {
			continue
		}

		seen[added.Id()] = seenAdded
		if prev, found := s.backendsById[added.Id()]; found {
			added.CopyCount(prev)
		} else {
			s.backendsById[added.Id()] = added
		}

		s.logger.Printf("Backend %s added for %s", added.Id(), strings.Join(added.Urls(), ", "))
		if added.Count() {
			added.UpdateStats()
			addedBackends++
		}
	}
	s.stats.AddBackends(addedBackends)
}

func getConfiguredBackendIDs(backendIds string) (ids []string) {
	seen := make(map[string]bool)

	for id := range internal.SplitEntries(backendIds, ",") {
		if seen[id] {
			continue
		}

		ids = append(ids, id)
		seen[id] = true
	}

	return ids
}

func getConfiguredHosts(logger log.Logger, backendIds string, cfg *goconf.ConfigFile, commonSecret string) (hosts map[string][]*talk.Backend) {
	hosts = make(map[string][]*talk.Backend)
	seenUrls := make(map[string]string)
	for _, id := range getConfiguredBackendIDs(backendIds) {
		var urls []string
		if u, _ := config.GetStringOptionWithEnv(cfg, id, "urls"); u != "" {
			urls = slices.Sorted(internal.SplitEntries(u, ","))
			urls = slices.Compact(urls)
		} else if u, _ := config.GetStringOptionWithEnv(cfg, id, "url"); u != "" {
			if u = strings.TrimSpace(u); u != "" {
				urls = []string{u}
			}
		}

		if len(urls) == 0 {
			logger.Printf("Backend %s is missing or incomplete, skipping", id)
			continue
		}

		backend, err := talk.NewBackendFromConfig(logger, id, cfg, commonSecret)
		if err != nil {
			logger.Printf("%s", err)
			continue
		}

		if sessionLimit := backend.Limit(); sessionLimit > 0 {
			logger.Printf("Backend %s allows a maximum of %d sessions", id, sessionLimit)
		}

		added := make(map[string]bool)
		for _, u := range urls {
			if u[len(u)-1] != '/' {
				u += "/"
			}

			parsed, err := url.Parse(u)
			if err != nil {
				logger.Printf("Backend %s has an invalid url %s configured (%s), skipping", id, u, err)
				continue
			}

			var changed bool
			if parsed, changed = internal.CanonicalizeUrl(parsed); changed {
				u = parsed.String()
			}

			if prev, found := seenUrls[u]; found {
				logger.Printf("Url %s in backend %s was already used in backend %s, skipping", u, id, prev)
				continue
			}

			seenUrls[u] = id
			backend.AddUrl(parsed)

			if !added[parsed.Host] {
				hosts[parsed.Host] = append(hosts[parsed.Host], backend)
				added[parsed.Host] = true
			}
		}
	}

	return hosts
}

func (s *backendStorageStatic) Reload(cfg *goconf.ConfigFile) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.compatBackend != nil {
		s.logger.Println("Old-style configuration active, reload is not supported")
		return
	}

	commonSecret, _ := config.GetStringOptionWithEnv(cfg, "backend", "secret")

	if backendIds, _ := cfg.GetString("backend", "backends"); backendIds != "" {
		configuredHosts := getConfiguredHosts(s.logger, backendIds, cfg, commonSecret)

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

func (s *backendStorageStatic) GetCompatBackend() *talk.Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.compatBackend
}

func (s *backendStorageStatic) GetBackend(u *url.URL) *talk.Backend {
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
