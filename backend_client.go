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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dlintw/goconf"
)

var (
	ErrNotRedirecting         = errors.New("not redirecting to different host")
	ErrUnsupportedContentType = errors.New("unsupported_content_type")

	ErrIncompleteResponse = errors.New("incomplete OCS response")
	ErrThrottledResponse  = errors.New("throttled OCS response")
)

func init() {
	RegisterBackendClientStats()
}

type BackendClient struct {
	hub      *Hub
	version  string
	backends *BackendConfiguration

	pool         *HttpClientPool
	capabilities *Capabilities
	buffers      BufferPool
}

func NewBackendClient(config *goconf.ConfigFile, maxConcurrentRequestsPerHost int, version string, etcdClient *EtcdClient) (*BackendClient, error) {
	backends, err := NewBackendConfiguration(config, etcdClient)
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

func (b *BackendClient) Close() {
	b.backends.Close()
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

	backend := b.backends.GetBackend(u)
	if backend == nil {
		return fmt.Errorf("no backend configured for %s", u)
	}

	var requestUrl *url.URL
	if b.capabilities.HasCapabilityFeature(ctx, u, FeatureSignalingV3Api) {
		newUrl := *u
		newUrl.Path = strings.ReplaceAll(newUrl.Path, "/spreed/api/v1/signaling/", "/spreed/api/v3/signaling/")
		newUrl.Path = strings.ReplaceAll(newUrl.Path, "/spreed/api/v2/signaling/", "/spreed/api/v3/signaling/")
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

	data, err := b.buffers.MarshalAsJSON(request)
	if err != nil {
		log.Printf("Could not marshal request %+v: %s", request, err)
		return err
	}

	defer b.buffers.Put(data)
	req, err := http.NewRequestWithContext(ctx, "POST", requestUrl.String(), data)
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
	AddBackendChecksum(req, data.Bytes(), backend.Secret())

	start := time.Now()
	resp, err := c.Do(req)
	end := time.Now()
	duration := end.Sub(start)
	statsBackendClientRequests.WithLabelValues(backend.Id()).Inc()
	statsBackendClientDuration.WithLabelValues(backend.Id()).Observe(duration.Seconds())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			statsBackendClientError.WithLabelValues(backend.Id(), "timeout").Inc()
		} else if errors.Is(err, context.Canceled) {
			statsBackendClientError.WithLabelValues(backend.Id(), "canceled").Inc()
		} else {
			statsBackendClientError.WithLabelValues(backend.Id(), "unknown").Inc()
		}
		log.Printf("Could not send request %s to %s: %s", data.String(), req.URL, err)
		return err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		log.Printf("Received unsupported content-type from %s: %s (%s)", req.URL, ct, resp.Status)
		statsBackendClientError.WithLabelValues(backend.Id(), "invalid_content_type").Inc()
		return ErrUnsupportedContentType
	}

	body, err := b.buffers.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read response body from %s: %s", req.URL, err)
		statsBackendClientError.WithLabelValues(backend.Id(), "error_reading_body").Inc()
		return err
	}

	defer b.buffers.Put(body)

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
		if err := json.Unmarshal(body.Bytes(), &ocs); err != nil {
			log.Printf("Could not decode OCS response %s from %s: %s", body.String(), req.URL, err)
			statsBackendClientError.WithLabelValues(backend.Id(), "error_decoding_ocs").Inc()
			return err
		} else if ocs.Ocs == nil || len(ocs.Ocs.Data) == 0 {
			log.Printf("Incomplete OCS response %s from %s", body.String(), req.URL)
			statsBackendClientError.WithLabelValues(backend.Id(), "error_incomplete_ocs").Inc()
			return ErrIncompleteResponse
		}

		switch ocs.Ocs.Meta.StatusCode {
		case http.StatusTooManyRequests:
			log.Printf("Throttled OCS response %s from %s", body.String(), req.URL)
			statsBackendClientError.WithLabelValues(backend.Id(), "throttled").Inc()
			return ErrThrottledResponse
		}

		if err := json.Unmarshal(ocs.Ocs.Data, response); err != nil {
			log.Printf("Could not decode OCS response body %s from %s: %s", string(ocs.Ocs.Data), req.URL, err)
			statsBackendClientError.WithLabelValues(backend.Id(), "error_decoding_ocs_data").Inc()
			return err
		}
	} else if err := json.Unmarshal(body.Bytes(), response); err != nil {
		log.Printf("Could not decode response body %s from %s: %s", body.String(), req.URL, err)
		statsBackendClientError.WithLabelValues(backend.Id(), "error_decoding_body").Inc()
		return err
	}
	return nil
}
