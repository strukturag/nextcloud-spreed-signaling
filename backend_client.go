/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/dlintw/goconf"
)

var (
	ErrNotRedirecting         = errors.New("not redirecting to different host")
	ErrUnsupportedContentType = errors.New("unsupported_content_type")
)

type BackendClient struct {
	hub      *Hub
	version  string
	backends *BackendConfiguration

	pool         *HttpClientPool
	capabilities *Capabilities
}

func NewBackendClient(config *goconf.ConfigFile, maxConcurrentRequestsPerHost int, version string) (*BackendClient, error) {
	backends, err := NewBackendConfiguration(config)
	if err != nil {
		return nil, err
	}

	skipverify, _ := config.GetBool("backend", "skipverify")
	if skipverify {
		log.Println("WARNING: Backend verification is disabled!")
	}

	pool, err := NewHttpClientPool(maxConcurrentRequestsPerHost, skipverify)
	if err != nil {
		return nil, err
	}

	capabilities, err := NewCapabilities(version, pool)
	if err != nil {
		return nil, err
	}

	return &BackendClient{
		version:  version,
		backends: backends,

		pool:         pool,
		capabilities: capabilities,
	}, nil
}

func (b *BackendClient) Reload(config *goconf.ConfigFile) {
	b.backends.Reload(config)
}

func (b *BackendClient) GetCompatBackend() *Backend {
	return b.backends.GetCompatBackend()
}

func (b *BackendClient) GetBackend(u *url.URL) *Backend {
	return b.backends.GetBackend(u)
}

func (b *BackendClient) GetBackends() []*Backend {
	return b.backends.GetBackends()
}

func (b *BackendClient) IsUrlAllowed(u *url.URL) bool {
	return b.backends.IsUrlAllowed(u)
}

func isOcsRequest(u *url.URL) bool {
	return strings.Contains(u.Path, "/ocs/v2.php") || strings.Contains(u.Path, "/ocs/v1.php")
}

// PerformJSONRequest sends a JSON POST request to the given url and decodes
// the result into "response".
func (b *BackendClient) PerformJSONRequest(ctx context.Context, u *url.URL, request interface{}, response interface{}) error {
	if u == nil {
		return fmt.Errorf("no url passed to perform JSON request %+v", request)
	}

	secret := b.backends.GetSecret(u)
	if secret == nil {
		return fmt.Errorf("no backend secret configured for for %s", u)
	}

	var requestUrl *url.URL
	if b.capabilities.HasCapabilityFeature(ctx, u, FeatureSignalingV3Api) {
		newUrl := *u
		newUrl.Path = strings.Replace(newUrl.Path, "/spreed/api/v1/signaling/", "/spreed/api/v3/signaling/", -1)
		newUrl.Path = strings.Replace(newUrl.Path, "/spreed/api/v2/signaling/", "/spreed/api/v3/signaling/", -1)
		requestUrl = &newUrl
	} else {
		requestUrl = u
	}

	c, pool, err := b.pool.Get(ctx, u)
	if err != nil {
		log.Printf("Could not get client for host %s: %s", u.Host, err)
		return err
	}
	defer pool.Put(c)

	data, err := json.Marshal(request)
	if err != nil {
		log.Printf("Could not marshal request %+v: %s", request, err)
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", requestUrl.String(), bytes.NewReader(data))
	if err != nil {
		log.Printf("Could not create request to %s: %s", requestUrl, err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("User-Agent", "nextcloud-spreed-signaling/"+b.version)
	if b.hub != nil {
		req.Header.Set("X-Spreed-Signaling-Features", strings.Join(b.hub.info.Features, ", "))
	}

	// Add checksum so the backend can validate the request.
	AddBackendChecksum(req, data, secret)

	resp, err := c.Do(req)
	if err != nil {
		log.Printf("Could not send request %s to %s: %s", string(data), req.URL, err)
		return err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		log.Printf("Received unsupported content-type from %s: %s (%s)", req.URL, ct, resp.Status)
		return ErrUnsupportedContentType
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read response body from %s: %s", req.URL, err)
		return err
	}

	if isOcsRequest(u) || req.Header.Get("OCS-APIRequest") != "" {
		// OCS response are wrapped in an OCS container that needs to be parsed
		// to get the actual contents:
		// {
		//   "ocs": {
		//     "meta": { ... },
		//     "data": { ... }
		//   }
		// }
		var ocs OcsResponse
		if err := json.Unmarshal(body, &ocs); err != nil {
			log.Printf("Could not decode OCS response %s from %s: %s", string(body), req.URL, err)
			return err
		} else if ocs.Ocs == nil || ocs.Ocs.Data == nil {
			log.Printf("Incomplete OCS response %s from %s", string(body), req.URL)
			return fmt.Errorf("incomplete OCS response")
		} else if err := json.Unmarshal(*ocs.Ocs.Data, response); err != nil {
			log.Printf("Could not decode OCS response body %s from %s: %s", string(*ocs.Ocs.Data), req.URL, err)
			return err
		}
	} else if err := json.Unmarshal(body, response); err != nil {
		log.Printf("Could not decode response body %s from %s: %s", string(body), req.URL, err)
		return err
	}
	return nil
}
