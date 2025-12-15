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
package geoip

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func testLookupReader(t *testing.T, reader *Lookup) {
	tests := map[string]Country{
		// Example from maxminddb-golang code.
		"81.2.69.142": "GB",
		// Local addresses don't have a country assigned.
		"127.0.0.1": "",
	}

	for ip, expected := range tests {
		t.Run(ip, func(t *testing.T) {
			country, err := reader.LookupCountry(net.ParseIP(ip))
			if !assert.NoError(t, err, "Could not lookup %s", ip) {
				return
			}

			assert.Equal(t, expected, country, "Unexpected country for %s", ip)
		})
	}
}

func GetIpUrlForTest(t *testing.T) string {
	t.Helper()

	var geoIpUrl string
	if os.Getenv("USE_DB_IP_GEOIP_DATABASE") != "" {
		now := time.Now().UTC()
		geoIpUrl = fmt.Sprintf("https://download.db-ip.com/free/dbip-country-lite-%d-%.2d.mmdb.gz", now.Year(), now.Month())
	}
	if geoIpUrl == "" {
		license := os.Getenv("MAXMIND_GEOLITE2_LICENSE")
		if license == "" {
			t.Skip("No MaxMind GeoLite2 license was set in MAXMIND_GEOLITE2_LICENSE environment variable.")
		}
		geoIpUrl = GetMaxMindDownloadUrl(license)
	}
	return geoIpUrl
}

func TestLookup(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	require := require.New(t)
	reader, err := NewLookupFromUrl(logger, GetIpUrlForTest(t))
	require.NoError(err)
	defer reader.Close()

	require.NoError(reader.Update())

	testLookupReader(t, reader)
}

func TestLookupCaching(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	require := require.New(t)
	reader, err := NewLookupFromUrl(logger, GetIpUrlForTest(t))
	require.NoError(err)
	defer reader.Close()

	require.NoError(reader.Update())

	// Updating the second time will most likely return a "304 Not Modified".
	// Make sure this doesn't trigger an error.
	require.NoError(reader.Update())
}

func TestLookupContinent(t *testing.T) {
	t.Parallel()
	tests := map[Country][]Continent{
		"AU":      {"OC"},
		"DE":      {"EU"},
		"RU":      {"EU"},
		"":        nil,
		"INVALID": nil,
	}

	for country, expected := range tests {
		t.Run(string(country), func(t *testing.T) {
			t.Parallel()
			continents := LookupContinents(country)
			if !assert.Len(t, continents, len(expected), "Continents didn't match for %s: got %s, expected %s", country, continents, expected) {
				return
			}
			for idx, c := range expected {
				if !assert.Equal(t, c, continents[idx], "Continents didn't match for %s: got %s, expected %s", country, continents, expected) {
					break
				}
			}
		})
	}
}

func TestLookupCloseEmpty(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	reader, err := NewLookupFromUrl(logger, "ignore-url")
	require.NoError(t, err)
	reader.Close()
}

func TestLookupFromFile(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	require := require.New(t)
	geoIpUrl := GetIpUrlForTest(t)

	resp, err := http.Get(geoIpUrl)
	require.NoError(err)
	defer resp.Body.Close()

	body := resp.Body
	url := geoIpUrl
	if strings.HasSuffix(geoIpUrl, ".gz") {
		body, err = gzip.NewReader(body)
		require.NoError(err)
		url = strings.TrimSuffix(url, ".gz")
	}

	tmpfile, err := os.CreateTemp("", "geoipdb")
	require.NoError(err)
	t.Cleanup(func() {
		os.Remove(tmpfile.Name())
	})

	foundDatabase := false
	if strings.HasSuffix(url, ".tar") || strings.HasSuffix(url, "=tar") {
		tarfile := tar.NewReader(body)
		for {
			header, err := tarfile.Next()
			if err == io.EOF {
				break
			}
			require.NoError(err)

			if !strings.HasSuffix(header.Name, ".mmdb") {
				continue
			}

			if _, err := io.Copy(tmpfile, tarfile); err != nil {
				tmpfile.Close()
				require.NoError(err)
			}
			require.NoError(tmpfile.Close())
			foundDatabase = true
			break
		}
	} else {
		if _, err := io.Copy(tmpfile, body); err != nil {
			tmpfile.Close()
			require.NoError(err)
		}
		require.NoError(tmpfile.Close())
		foundDatabase = true
	}

	require.True(foundDatabase, "Did not find GeoIP database in download from %s", geoIpUrl)

	reader, err := NewLookupFromFile(logger, tmpfile.Name())
	require.NoError(err)
	defer reader.Close()

	testLookupReader(t, reader)
}

func TestIsValidContinent(t *testing.T) {
	t.Parallel()
	for country, continents := range ContinentMap {
		for _, continent := range continents {
			assert.True(t, IsValidContinent(continent), "Continent %s of country %s is not valid", continent, country)
		}
	}
}
