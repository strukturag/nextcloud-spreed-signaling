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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dlintw/goconf"
)

var (
	ErrUseLastResponse = fmt.Errorf("use last response")
)

const (
	// Name of the "Talk" app in Nextcloud.
	AppNameSpreed = "spreed"

	// Name of capability to enable the "v3" API for the signaling endpoint.
	FeatureSignalingV3Api = "signaling-v3"

	// Cache received capabilities for one hour.
	CapabilitiesCacheDuration = time.Hour
)

type BackendClient struct {
	transport *http.Transport
	version   string
	backends  *BackendConfiguration
	clients   map[string]*HttpClientPool

	mu sync.Mutex

	maxConcurrentRequestsPerHost int

	capabilitiesLock sync.RWMutex
	capabilities     map[string]map[string]interface{}
	nextCapabilities map[string]time.Time
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

	tlsconfig := &tls.Config{
		InsecureSkipVerify: skipverify,
	}
	transport := &http.Transport{
		MaxIdleConnsPerHost: maxConcurrentRequestsPerHost,
		TLSClientConfig:     tlsconfig,
	}

	return &BackendClient{
		transport: transport,
		version:   version,
		backends:  backends,
		clients:   make(map[string]*HttpClientPool),

		maxConcurrentRequestsPerHost: maxConcurrentRequestsPerHost,

		capabilities:     make(map[string]map[string]interface{}),
		nextCapabilities: make(map[string]time.Time),
	}, nil
}

func (b *BackendClient) Reload(config *goconf.ConfigFile) {
	b.backends.Reload(config)
}

func (b *BackendClient) getPool(url *url.URL) (*HttpClientPool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if pool, found := b.clients[url.Host]; found {
		return pool, nil
	}

	pool, err := NewHttpClientPool(func() *http.Client {
		return &http.Client{
			Transport: b.transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Should be http.ErrUseLastResponse with go 1.8
				return ErrUseLastResponse
			},
		}
	}, b.maxConcurrentRequestsPerHost)
	if err != nil {
		return nil, err
	}

	b.clients[url.Host] = pool
	return pool, nil
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

func closeBody(response *http.Response) {
	if response.Body != nil {
		response.Body.Close()
	}
}

// refererForURL returns a referer without any authentication info or
// an empty string if lastReq scheme is https and newReq scheme is http.
func refererForURL(lastReq, newReq *url.URL) string {
	// https://tools.ietf.org/html/rfc7231#section-5.5.2
	//   "Clients SHOULD NOT include a Referer header field in a
	//    (non-secure) HTTP request if the referring page was
	//    transferred with a secure protocol."
	if lastReq.Scheme == "https" && newReq.Scheme == "http" {
		return ""
	}
	referer := lastReq.String()
	if lastReq.User != nil {
		// This is not very efficient, but is the best we can
		// do without:
		// - introducing a new method on URL
		// - creating a race condition
		// - copying the URL struct manually, which would cause
		//   maintenance problems down the line
		auth := lastReq.User.String() + "@"
		referer = strings.Replace(referer, auth, "", 1)
	}
	return referer
}

// urlErrorOp returns the (*url.Error).Op value to use for the
// provided (*Request).Method value.
func urlErrorOp(method string) string {
	if method == "" {
		return "Get"
	}
	return method[:1] + strings.ToLower(method[1:])
}

func performRequestWithRedirects(ctx context.Context, client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	var reqs []*http.Request
	var resp *http.Response

	uerr := func(err error) error {
		var urlStr string
		if resp != nil && resp.Request != nil {
			urlStr = resp.Request.URL.String()
		} else {
			urlStr = req.URL.String()
		}
		return &url.Error{
			Op:  urlErrorOp(reqs[0].Method),
			URL: urlStr,
			Err: err,
		}
	}
	for {
		if len(reqs) >= 10 {
			return nil, fmt.Errorf("stopped after 10 redirects")
		}

		if len(reqs) > 0 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				closeBody(resp)
				return nil, uerr(fmt.Errorf("%d response missing Location header", resp.StatusCode))
			}
			u, err := req.URL.Parse(loc)
			if err != nil {
				closeBody(resp)
				return nil, uerr(fmt.Errorf("failed to parse Location header %q: %v", loc, err))
			}

			if len(reqs) == 1 {
				log.Printf("Got a redirect from %s to %s, please check your configuration", req.URL, u)
			}

			host := ""
			if req.Host != "" && req.Host != req.URL.Host {
				// If the caller specified a custom Host header and the
				// redirect location is relative, preserve the Host header
				// through the redirect. See issue #22233.
				if u, _ := url.Parse(loc); u != nil && !u.IsAbs() {
					host = req.Host
				}
			}
			ireq := reqs[0]
			req = &http.Request{
				Method: ireq.Method,
				URL:    u,
				Header: ireq.Header,
				Host:   host,
			}

			// Add the Referer header from the most recent
			// request URL to the new one, if it's not https->http:
			if ref := refererForURL(reqs[len(reqs)-1].URL, req.URL); ref != "" {
				req.Header.Set("Referer", ref)
			}
			// Close the previous response's body. But
			// read at least some of the body so if it's
			// small the underlying TCP connection will be
			// re-used. No need to check for errors: if it
			// fails, the Transport won't reuse it anyway.
			const maxBodySlurpSize = 2 << 10
			if resp.ContentLength == -1 || resp.ContentLength <= maxBodySlurpSize {
				io.CopyN(ioutil.Discard, resp.Body, maxBodySlurpSize) // nolint
			}
			resp.Body.Close()
		}
		reqs = append(reqs, req)
		var err error

		if body != nil {
			req.Body = ioutil.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}
		resp, err = client.Do(req.WithContext(ctx))
		if err != nil {
			// Prefer context error if it has been cancelled.
			select {
			case <-ctx.Done():
				err = ctx.Err()
			default:
			}
			if e, ok := err.(*url.Error); !ok || resp == nil || e.Err != ErrUseLastResponse {
				return nil, err
			}
		}

		switch resp.StatusCode {
		case 301, 302, 303:
			break
		case 307, 308:
			if resp.Header.Get("Location") == "" {
				// 308s have been observed in the wild being served
				// without Location headers. Since Go 1.7 and earlier
				// didn't follow these codes, just stop here instead
				// of returning an error.
				// See Issue 17773.
				return resp, nil
			}
			if req.Body == nil {
				// We had a request body, and 307/308 require
				// re-sending it, but GetBody is not defined. So just
				// return this response to the user instead of an
				// error, like we did in Go 1.7 and earlier.
				return resp, nil
			}
		default:
			return resp, nil
		}
	}
}

type CapabilitiesVersion struct {
	Major           int    `json:"major"`
	Minor           int    `json:"minor"`
	Micro           int    `json:"micro"`
	String          string `json:"string"`
	Edition         string `json:"edition"`
	ExtendedSupport bool   `json:"extendedSupport"`
}

type CapabilitiesResponse struct {
	Version      CapabilitiesVersion               `json:"version"`
	Capabilities map[string]map[string]interface{} `json:"capabilities"`
}

func (b *BackendClient) getCapabilities(ctx context.Context, u *url.URL) (map[string]interface{}, error) {
	key := u.String()
	now := time.Now()

	b.capabilitiesLock.RLock()
	if caps, found := b.capabilities[key]; found {
		if next, found := b.nextCapabilities[key]; found && next.After(now) {
			b.capabilitiesLock.RUnlock()
			return caps, nil
		}
	}
	b.capabilitiesLock.RUnlock()

	capUrl := *u
	if !strings.Contains(capUrl.Path, "ocs/v2.php") {
		if !strings.HasSuffix(capUrl.Path, "/") {
			capUrl.Path += "/"
		}
		capUrl.Path = capUrl.Path + "ocs/v2.php/cloud/capabilities"
	} else if pos := strings.Index(capUrl.Path, "/ocs/v2.php/"); pos >= 0 {
		capUrl.Path = capUrl.Path[:pos+11] + "/cloud/capabilities"
	}

	log.Printf("Capabilities expired for %s, updating", capUrl.String())

	pool, err := b.getPool(&capUrl)
	if err != nil {
		log.Printf("Could not get client pool for host %s: %s", capUrl.Host, err)
		return nil, err
	}

	c, err := pool.Get(ctx)
	if err != nil {
		log.Printf("Could not get client for host %s: %s", capUrl.Host, err)
		return nil, err
	}
	defer pool.Put(c)

	req := &http.Request{
		Method:     "GET",
		URL:        &capUrl,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("User-Agent", "nextcloud-spreed-signaling/"+b.version)

	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		log.Printf("Received unsupported content-type from %s: %s (%s)", capUrl.String(), ct, resp.Status)
		return nil, fmt.Errorf("unsupported_content_type")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read response body from %s: %s", capUrl.String(), err)
		return nil, err
	}

	var ocs OcsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		log.Printf("Could not decode OCS response %s from %s: %s", string(body), capUrl.String(), err)
		return nil, err
	} else if ocs.Ocs == nil || ocs.Ocs.Data == nil {
		log.Printf("Incomplete OCS response %s from %s", string(body), u)
		return nil, fmt.Errorf("incomplete OCS response")
	}

	var response CapabilitiesResponse
	if err := json.Unmarshal(*ocs.Ocs.Data, &response); err != nil {
		log.Printf("Could not decode OCS response body %s from %s: %s", string(*ocs.Ocs.Data), capUrl.String(), err)
		return nil, err
	}

	capa, found := response.Capabilities[AppNameSpreed]
	if !found {
		log.Printf("No capabilities received for app spreed from %s: %+v", capUrl.String(), response)
		return nil, nil
	}

	log.Printf("Received capabilities %+v from %s", capa, capUrl.String())
	b.capabilitiesLock.Lock()
	b.capabilities[key] = capa
	b.nextCapabilities[key] = now.Add(CapabilitiesCacheDuration)
	b.capabilitiesLock.Unlock()
	return capa, nil
}

func (b *BackendClient) HasCapabilityFeature(ctx context.Context, u *url.URL, feature string) bool {
	caps, err := b.getCapabilities(ctx, u)
	if err != nil {
		log.Printf("Could not get capabilities for %s: %s", u, err)
		return false
	}

	featuresInterface := caps["features"]
	if featuresInterface == nil {
		return false
	}

	features, ok := featuresInterface.([]interface{})
	if !ok {
		log.Printf("Invalid features list received for %s: %+v", u, featuresInterface)
		return false
	}

	for _, entry := range features {
		if entry == feature {
			return true
		}
	}
	return false
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
	if b.HasCapabilityFeature(ctx, u, FeatureSignalingV3Api) {
		newUrl := *u
		newUrl.Path = strings.Replace(newUrl.Path, "/spreed/api/v1/signaling/", "/spreed/api/v3/signaling/", -1)
		newUrl.Path = strings.Replace(newUrl.Path, "/spreed/api/v2/signaling/", "/spreed/api/v3/signaling/", -1)
		requestUrl = &newUrl
	} else {
		requestUrl = u
	}

	pool, err := b.getPool(u)
	if err != nil {
		log.Printf("Could not get client pool for host %s: %s", u.Host, err)
		return err
	}

	c, err := pool.Get(ctx)
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

	req := &http.Request{
		Method:     "POST",
		URL:        requestUrl,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("User-Agent", "nextcloud-spreed-signaling/"+b.version)

	// Add checksum so the backend can validate the request.
	AddBackendChecksum(req, data, secret)

	resp, err := performRequestWithRedirects(ctx, c, req, data)
	if err != nil {
		log.Printf("Could not send request %s to %s: %s", string(data), req.URL, err)
		return err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		log.Printf("Received unsupported content-type from %s: %s (%s)", req.URL, ct, resp.Status)
		return fmt.Errorf("unsupported_content_type")
	}

	body, err := ioutil.ReadAll(resp.Body)
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
