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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Name of the "Talk" app in Nextcloud.
	AppNameSpreed = "spreed"

	// Name of capability to enable the "v3" API for the signaling endpoint.
	FeatureSignalingV3Api = "signaling-v3"

	// Cache received capabilities for one hour.
	CapabilitiesCacheDuration = time.Hour

	// Don't invalidate more than once per minute.
	maxInvalidateInterval = time.Minute
)

type capabilitiesEntry struct {
	nextUpdate   time.Time
	capabilities map[string]interface{}
}

type Capabilities struct {
	mu sync.RWMutex

	// Can be overwritten by tests.
	getNow func() time.Time

	version        string
	pool           *HttpClientPool
	entries        map[string]*capabilitiesEntry
	nextInvalidate map[string]time.Time
}

func NewCapabilities(version string, pool *HttpClientPool) (*Capabilities, error) {
	result := &Capabilities{
		getNow: time.Now,

		version:        version,
		pool:           pool,
		entries:        make(map[string]*capabilitiesEntry),
		nextInvalidate: make(map[string]time.Time),
	}

	return result, nil
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
	Version      CapabilitiesVersion        `json:"version"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
}

func (c *Capabilities) getCapabilities(key string) (map[string]interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := c.getNow()
	if entry, found := c.entries[key]; found && entry.nextUpdate.After(now) {
		return entry.capabilities, true
	}

	return nil, false
}

func (c *Capabilities) setCapabilities(key string, capabilities map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.getNow()
	entry := &capabilitiesEntry{
		nextUpdate:   now.Add(CapabilitiesCacheDuration),
		capabilities: capabilities,
	}

	c.entries[key] = entry
}

func (c *Capabilities) invalidateCapabilities(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.getNow()
	if entry, found := c.nextInvalidate[key]; found && entry.After(now) {
		return
	}

	delete(c.entries, key)
	c.nextInvalidate[key] = now.Add(maxInvalidateInterval)
}

func (c *Capabilities) getKeyForUrl(u *url.URL) string {
	key := u.String()
	return key
}

func (c *Capabilities) loadCapabilities(ctx context.Context, u *url.URL) (map[string]interface{}, bool, error) {
	key := c.getKeyForUrl(u)
	if caps, found := c.getCapabilities(key); found {
		return caps, true, nil
	}

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

	client, pool, err := c.pool.Get(ctx, &capUrl)
	if err != nil {
		log.Printf("Could not get client for host %s: %s", capUrl.Host, err)
		return nil, false, err
	}
	defer pool.Put(client)

	req, err := http.NewRequestWithContext(ctx, "GET", capUrl.String(), nil)
	if err != nil {
		log.Printf("Could not create request to %s: %s", &capUrl, err)
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("User-Agent", "nextcloud-spreed-signaling/"+c.version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		log.Printf("Received unsupported content-type from %s: %s (%s)", capUrl.String(), ct, resp.Status)
		return nil, false, ErrUnsupportedContentType
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read response body from %s: %s", capUrl.String(), err)
		return nil, false, err
	}

	var ocs OcsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		log.Printf("Could not decode OCS response %s from %s: %s", string(body), capUrl.String(), err)
		return nil, false, err
	} else if ocs.Ocs == nil || len(ocs.Ocs.Data) == 0 {
		log.Printf("Incomplete OCS response %s from %s", string(body), u)
		return nil, false, fmt.Errorf("incomplete OCS response")
	}

	var response CapabilitiesResponse
	if err := json.Unmarshal(ocs.Ocs.Data, &response); err != nil {
		log.Printf("Could not decode OCS response body %s from %s: %s", string(ocs.Ocs.Data), capUrl.String(), err)
		return nil, false, err
	}

	capaObj, found := response.Capabilities[AppNameSpreed]
	if !found || len(capaObj) == 0 {
		log.Printf("No capabilities received for app spreed from %s: %+v", capUrl.String(), response)
		return nil, false, nil
	}

	var capa map[string]interface{}
	if err := json.Unmarshal(capaObj, &capa); err != nil {
		log.Printf("Unsupported capabilities received for app spreed from %s: %+v", capUrl.String(), response)
		return nil, false, nil
	}

	log.Printf("Received capabilities %+v from %s", capa, capUrl.String())
	c.setCapabilities(key, capa)
	return capa, false, nil
}

func (c *Capabilities) HasCapabilityFeature(ctx context.Context, u *url.URL, feature string) bool {
	caps, _, err := c.loadCapabilities(ctx, u)
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

func (c *Capabilities) getConfigGroup(ctx context.Context, u *url.URL, group string) (map[string]interface{}, bool, bool) {
	caps, cached, err := c.loadCapabilities(ctx, u)
	if err != nil {
		log.Printf("Could not get capabilities for %s: %s", u, err)
		return nil, cached, false
	}

	configInterface := caps["config"]
	if configInterface == nil {
		return nil, cached, false
	}

	config, ok := configInterface.(map[string]interface{})
	if !ok {
		log.Printf("Invalid config mapping received from %s: %+v", u, configInterface)
		return nil, cached, false
	}

	groupInterface := config[group]
	if groupInterface == nil {
		return nil, cached, false
	}

	groupConfig, ok := groupInterface.(map[string]interface{})
	if !ok {
		log.Printf("Invalid group mapping \"%s\" received from %s: %+v", group, u, groupInterface)
		return nil, cached, false
	}

	return groupConfig, cached, true
}

func (c *Capabilities) GetIntegerConfig(ctx context.Context, u *url.URL, group, key string) (int, bool, bool) {
	groupConfig, cached, found := c.getConfigGroup(ctx, u, group)
	if !found {
		return 0, cached, false
	}

	value, found := groupConfig[key]
	if !found {
		return 0, cached, false
	}

	switch value := value.(type) {
	case int:
		return value, cached, true
	case float32:
		return int(value), cached, true
	case float64:
		return int(value), cached, true
	default:
		log.Printf("Invalid config value for \"%s\" received from %s: %+v", key, u, value)
	}

	return 0, cached, false
}

func (c *Capabilities) GetStringConfig(ctx context.Context, u *url.URL, group, key string) (string, bool, bool) {
	groupConfig, cached, found := c.getConfigGroup(ctx, u, group)
	if !found {
		return "", cached, false
	}

	value, found := groupConfig[key]
	if !found {
		return "", cached, false
	}

	switch value := value.(type) {
	case string:
		return value, cached, true
	default:
		log.Printf("Invalid config value for \"%s\" received from %s: %+v", key, u, value)
	}

	return "", cached, false
}

func (c *Capabilities) InvalidateCapabilities(u *url.URL) {
	key := c.getKeyForUrl(u)

	c.invalidateCapabilities(key)
}
