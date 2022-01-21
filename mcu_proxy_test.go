/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"testing"
)

func TestMcuProxyStats(t *testing.T) {
	collectAndLint(t, proxyMcuStats...)
}

func newProxyConnectionWithCountry(country string) *mcuProxyConnection {
	conn := &mcuProxyConnection{}
	conn.country.Store(country)
	return conn
}

func Test_sortConnectionsForCountry(t *testing.T) {
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[string][][]*mcuProxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
	}

	for country, test := range testcases {
		country := country
		test := test
		t.Run(country, func(t *testing.T) {
			sorted := sortConnectionsForCountry(test[0], country, nil)
			for idx, conn := range sorted {
				if test[1][idx] != conn {
					t.Errorf("Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
				}
			}
		})
	}
}

func Test_sortConnectionsForCountryWithOverride(t *testing.T) {
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[string][][]*mcuProxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AR": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
		// No match but override (OC -> AS / NA)
		"AU": {
			{conn_us, conn_jp},
			{conn_us, conn_jp},
		},
		// No match but override (AF -> EU)
		"ZA": {
			{conn_de, conn_at},
			{conn_de, conn_at},
		},
	}

	continentMap := map[string][]string{
		// Use European connections for Africa.
		"AF": {"EU"},
		// Use Asian and North American connections for Oceania.
		"OC": {"AS", "NA"},
	}
	for country, test := range testcases {
		country := country
		test := test
		t.Run(country, func(t *testing.T) {
			sorted := sortConnectionsForCountry(test[0], country, continentMap)
			for idx, conn := range sorted {
				if test[1][idx] != conn {
					t.Errorf("Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
				}
			}
		})
	}
}
