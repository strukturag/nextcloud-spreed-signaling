/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package etcd

import (
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

// Information on a backend in the etcd cluster.

type BackendInformationEtcd struct {
	// Compat setting.
	Url string `json:"url,omitempty"`

	Urls       []string   `json:"urls,omitempty"`
	ParsedUrls []*url.URL `json:"-"`
	Secret     string     `json:"secret"`

	MaxStreamBitrate api.Bandwidth `json:"maxstreambitrate,omitempty"`
	MaxScreenBitrate api.Bandwidth `json:"maxscreenbitrate,omitempty"`

	SessionLimit uint64 `json:"sessionlimit,omitempty"`
}

func (p *BackendInformationEtcd) CheckValid() (err error) {
	if p.Secret == "" {
		return errors.New("secret missing")
	}

	if len(p.Urls) > 0 {
		slices.Sort(p.Urls)
		p.Urls = slices.Compact(p.Urls)
		seen := make(map[string]bool)
		outIdx := 0
		for _, u := range p.Urls {
			parsedUrl, err := url.Parse(u)
			if err != nil {
				return fmt.Errorf("invalid url %s: %w", u, err)
			}

			var changed bool
			if parsedUrl, changed = internal.CanonicalizeUrl(parsedUrl); changed {
				u = parsedUrl.String()
			}
			p.Urls[outIdx] = u
			if seen[u] {
				continue
			}
			seen[u] = true
			p.ParsedUrls = append(p.ParsedUrls, parsedUrl)
			outIdx++
		}
		if len(p.Urls) != outIdx {
			clear(p.Urls[outIdx:])
			p.Urls = p.Urls[:outIdx]
		}
	} else if p.Url != "" {
		parsedUrl, err := url.Parse(p.Url)
		if err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}
		var changed bool
		if parsedUrl, changed = internal.CanonicalizeUrl(parsedUrl); changed {
			p.Url = parsedUrl.String()
		}

		p.Urls = append(p.Urls, p.Url)
		p.ParsedUrls = append(p.ParsedUrls, parsedUrl)
	} else {
		return errors.New("urls missing")
	}

	return nil
}
