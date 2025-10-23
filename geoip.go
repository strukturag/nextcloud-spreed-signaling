/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2019 struktur AG
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
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/oschwald/maxminddb-golang"
)

var (
	ErrDatabaseNotInitialized = fmt.Errorf("GeoIP database not initialized yet")
)

func GetGeoIpDownloadUrl(license string) string {
	if license == "" {
		return ""
	}

	result := "https://download.maxmind.com/app/geoip_download"
	result += "?edition_id=GeoLite2-Country"
	result += "&license_key=" + url.QueryEscape(license)
	result += "&suffix=tar.gz"
	return result
}

type GeoLookup struct {
	url    string
	isFile bool
	client http.Client

	lastModifiedHeader atomic.Value
	lastModifiedTime   atomic.Int64

	reader atomic.Pointer[maxminddb.Reader]
}

func NewGeoLookupFromUrl(url string) (*GeoLookup, error) {
	geoip := &GeoLookup{
		url: url,
	}
	return geoip, nil
}

func NewGeoLookupFromFile(filename string) (*GeoLookup, error) {
	geoip := &GeoLookup{
		url:    filename,
		isFile: true,
	}
	if err := geoip.Update(); err != nil {
		geoip.Close()
		return nil, err
	}
	return geoip, nil
}

func (g *GeoLookup) Close() {
	if reader := g.reader.Swap(nil); reader != nil {
		reader.Close()
	}
}

func (g *GeoLookup) Update() error {
	if g.isFile {
		return g.updateFile()
	}

	return g.updateUrl()
}

func (g *GeoLookup) updateFile() error {
	info, err := os.Stat(g.url)
	if err != nil {
		return err
	}

	if info.ModTime().UnixNano() == g.lastModifiedTime.Load() {
		return nil
	}

	reader, err := maxminddb.Open(g.url)
	if err != nil {
		return err
	}

	if err := reader.Verify(); err != nil {
		return err
	}

	metadata := reader.Metadata
	log.Printf("Using %s GeoIP database from %s (built on %s)", metadata.DatabaseType, g.url, time.Unix(int64(metadata.BuildEpoch), 0).UTC())

	if old := g.reader.Swap(reader); old != nil {
		old.Close()
	}

	g.lastModifiedTime.Store(info.ModTime().UnixNano())
	return nil
}

func (g *GeoLookup) updateUrl() error {
	request, err := http.NewRequest("GET", g.url, nil)
	if err != nil {
		return err
	}
	if header := g.lastModifiedHeader.Load(); header != nil {
		request.Header.Add("If-Modified-Since", header.(string))
	}
	response, err := g.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotModified {
		log.Printf("GeoIP database at %s has not changed", g.url)
		return nil
	} else if response.StatusCode/100 != 2 {
		return fmt.Errorf("downloading %s returned an error: %s", g.url, response.Status)
	}

	body := response.Body
	url := g.url
	if strings.HasSuffix(url, ".gz") {
		body, err = gzip.NewReader(body)
		if err != nil {
			return err
		}
		url = strings.TrimSuffix(url, ".gz")
	}

	var geoipdata []byte
	if strings.HasSuffix(url, ".tar") || strings.HasSuffix(url, "=tar") {
		tarfile := tar.NewReader(body)
		for {
			header, err := tarfile.Next()
			if err == io.EOF {
				break
			} else if err != nil {
				return err
			}

			if !strings.HasSuffix(header.Name, ".mmdb") {
				continue
			}

			geoipdata, err = io.ReadAll(tarfile)
			if err != nil {
				return err
			}
			break
		}
	} else {
		geoipdata, err = io.ReadAll(body)
		if err != nil {
			return err
		}
	}

	if len(geoipdata) == 0 {
		return fmt.Errorf("did not find GeoIP database in download from %s", g.url)
	}

	reader, err := maxminddb.FromBytes(geoipdata)
	if err != nil {
		return err
	}

	if err := reader.Verify(); err != nil {
		return err
	}

	metadata := reader.Metadata
	log.Printf("Using %s GeoIP database from %s (built on %s)", metadata.DatabaseType, g.url, time.Unix(int64(metadata.BuildEpoch), 0).UTC())

	if old := g.reader.Swap(reader); old != nil {
		old.Close()
	}

	g.lastModifiedHeader.Store(response.Header.Get("Last-Modified"))
	return nil
}

func (g *GeoLookup) LookupCountry(ip net.IP) (string, error) {
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}

	reader := g.reader.Load()
	if reader == nil {
		return "", ErrDatabaseNotInitialized
	}

	if err := reader.Lookup(ip, &record); err != nil {
		return "", err
	}

	return record.Country.ISOCode, nil
}

func LookupContinents(country string) []string {
	continents, found := ContinentMap[country]
	if !found {
		return nil
	}

	return continents
}

func IsValidContinent(continent string) bool {
	switch continent {
	case "AF":
		// Africa
		fallthrough
	case "AN":
		// Antartica
		fallthrough
	case "AS":
		// Asia
		fallthrough
	case "EU":
		// Europe
		fallthrough
	case "NA":
		// North America
		fallthrough
	case "SA":
		// South America
		fallthrough
	case "OC":
		// Oceania
		return true
	default:
		return false
	}
}

func LoadGeoIPOverrides(config *goconf.ConfigFile, ignoreErrors bool) (map[*net.IPNet]string, error) {
	options, _ := GetStringOptions(config, "geoip-overrides", true)
	if len(options) == 0 {
		return nil, nil
	}

	var err error
	geoipOverrides := make(map[*net.IPNet]string, len(options))
	for option, value := range options {
		var ip net.IP
		var ipNet *net.IPNet
		if strings.Contains(option, "/") {
			_, ipNet, err = net.ParseCIDR(option)
			if err != nil {
				if ignoreErrors {
					log.Printf("could not parse CIDR %s (%s), skipping", option, err)
					continue
				}

				return nil, fmt.Errorf("could not parse CIDR %s: %s", option, err)
			}
		} else {
			ip = net.ParseIP(option)
			if ip == nil {
				if ignoreErrors {
					log.Printf("could not parse IP %s, skipping", option)
					continue
				}

				return nil, fmt.Errorf("could not parse IP %s", option)
			}

			var mask net.IPMask
			if ipv4 := ip.To4(); ipv4 != nil {
				mask = net.CIDRMask(32, 32)
			} else {
				mask = net.CIDRMask(128, 128)
			}
			ipNet = &net.IPNet{
				IP:   ip,
				Mask: mask,
			}
		}

		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			log.Printf("IP %s doesn't have a country assigned, skipping", option)
			continue
		} else if !IsValidCountry(value) {
			log.Printf("Country %s for IP %s is invalid, skipping", value, option)
			continue
		}

		log.Printf("Using country %s for %s", value, ipNet)
		geoipOverrides[ipNet] = value
	}

	return geoipOverrides, nil
}
