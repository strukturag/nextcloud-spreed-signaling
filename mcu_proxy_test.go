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
		"DE": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_at, conn_jp, conn_de},
			[]*mcuProxyConnection{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_at, conn_jp, conn_de},
			[]*mcuProxyConnection{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_de, conn_jp, conn_at},
			[]*mcuProxyConnection{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_de, conn_jp, conn_at},
			[]*mcuProxyConnection{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_de, conn_jp, conn_at},
			[]*mcuProxyConnection{conn_jp, conn_de, conn_at},
		},
		// Partial continent match
		"RU": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_us, conn_de, conn_jp, conn_at},
			[]*mcuProxyConnection{conn_de, conn_jp, conn_at, conn_us},
		},
		// No match
		"AU": [][]*mcuProxyConnection{
			[]*mcuProxyConnection{conn_us, conn_de, conn_jp, conn_at},
			[]*mcuProxyConnection{conn_us, conn_de, conn_jp, conn_at},
		},
	}

	for country, test := range testcases {
		sorted := sortConnectionsForCountry(test[0], country)
		for idx, conn := range sorted {
			if test[1][idx] != conn {
				t.Errorf("Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
			}
		}
	}
}
