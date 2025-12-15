/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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

import "strings"

type (
	Country   string
	Continent string
)

var (
	NoCountry      = Country("no-country")
	noCountryUpper = Country(strings.ToUpper("no-country"))

	Loopback      = Country("loopback")
	loopbackUpper = Country(strings.ToUpper("loopback"))

	UnknownCountry      = Country("unknown-country")
	unknownCountryUpper = Country(strings.ToUpper("unknown-country"))
)

func IsValidCountry(country Country) bool {
	switch country {
	case "":
		fallthrough
	case NoCountry:
		fallthrough
	case noCountryUpper:
		fallthrough
	case Loopback:
		fallthrough
	case loopbackUpper:
		fallthrough
	case UnknownCountry:
		fallthrough
	case unknownCountryUpper:
		return false
	default:
		return true
	}
}

func LookupContinents(country Country) []Continent {
	continents, found := ContinentMap[country]
	if !found {
		return nil
	}

	return continents
}

func IsValidContinent(continent Continent) bool {
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
