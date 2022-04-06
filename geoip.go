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
	"sync"
	"time"

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
	mu     sync.Mutex

	lastModifiedHeader string
	lastModifiedTime   time.Time

	reader *maxminddb.Reader
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
	g.mu.Lock()
	if g.reader != nil {
		g.reader.Close()
		g.reader = nil
	}
	g.mu.Unlock()
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

	if info.ModTime().Equal(g.lastModifiedTime) {
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

	g.mu.Lock()
	if g.reader != nil {
		g.reader.Close()
	}
	g.reader = reader
	g.lastModifiedTime = info.ModTime()
	g.mu.Unlock()
	return nil
}

func (g *GeoLookup) updateUrl() error {
	request, err := http.NewRequest("GET", g.url, nil)
	if err != nil {
		return err
	}
	if g.lastModifiedHeader != "" {
		request.Header.Add("If-Modified-Since", g.lastModifiedHeader)
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
	if strings.HasSuffix(g.url, ".gz") {
		body, err = gzip.NewReader(body)
		if err != nil {
			return err
		}
	}

	tarfile := tar.NewReader(body)
	var geoipdata []byte
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

	if len(geoipdata) == 0 {
		return fmt.Errorf("did not find MaxMind database in tarball from %s", g.url)
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

	g.mu.Lock()
	if g.reader != nil {
		g.reader.Close()
	}
	g.reader = reader
	g.lastModifiedHeader = response.Header.Get("Last-Modified")
	g.mu.Unlock()
	return nil
}

func (g *GeoLookup) LookupCountry(ip net.IP) (string, error) {
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}

	g.mu.Lock()
	if g.reader == nil {
		g.mu.Unlock()
		return "", ErrDatabaseNotInitialized
	}
	err := g.reader.Lookup(ip, &record)
	g.mu.Unlock()
	if err != nil {
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
