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
	"net"
	"os"
	"testing"
)

func TestGeoLookup(t *testing.T) {
	license := os.Getenv("MAXMIND_GEOLITE2_LICENSE")
	if license == "" {
		t.Skip("No MaxMind GeoLite2 license was set in MAXMIND_GEOLITE2_LICENSE environment variable.")
	}

	tests := map[string]string{
		// Example from maxminddb-golang code.
		"81.2.69.142": "GB",
		// Local addresses don't have a country assigned.
		"127.0.0.1": "",
	}
	reader, err := NewGeoLookup(GetGeoIpDownloadUrl(license))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	if err := reader.Update(); err != nil {
		t.Fatal(err)
	}

	for ip, expected := range tests {
		country, err := reader.LookupCountry(net.ParseIP(ip))
		if err != nil {
			t.Errorf("Could not lookup %s: %s", ip, err)
			continue
		}

		if country != expected {
			t.Errorf("Expected %s for %s, got %s", expected, ip, country)
		}
	}
}

func TestGeoLookupCaching(t *testing.T) {
	license := os.Getenv("MAXMIND_GEOLITE2_LICENSE")
	if license == "" {
		t.Skip("No MaxMind GeoLite2 license was set in MAXMIND_GEOLITE2_LICENSE environment variable.")
	}

	reader, err := NewGeoLookup(GetGeoIpDownloadUrl(license))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	if err := reader.Update(); err != nil {
		t.Fatal(err)
	}

	// Updating the second time will most likely return a "304 Not Modified".
	// Make sure this doesn't trigger an error.
	if err := reader.Update(); err != nil {
		t.Fatal(err)
	}
}

func TestGeoLookupContinent(t *testing.T) {
	tests := map[string][]string{
		"AU":       []string{"OC"},
		"DE":       []string{"EU"},
		"RU":       []string{"EU", "AS"},
		"":         nil,
		"INVALID ": nil,
	}

	for country, expected := range tests {
		continents := LookupContinents(country)
		if len(continents) != len(expected) {
			t.Errorf("Continents didn't match for %s: got %s, expected %s", country, continents, expected)
			continue
		}
		for idx, c := range expected {
			if continents[idx] != c {
				t.Errorf("Continents didn't match for %s: got %s, expected %s", country, continents, expected)
				break
			}
		}
	}
}

func TestGeoLookupCloseEmpty(t *testing.T) {
	reader, err := NewGeoLookup("ignore-url")
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
}
