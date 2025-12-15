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

import (
	"context"
	"fmt"
	"maps"
	"net"
	"strings"
	"sync/atomic"

	"github.com/dlintw/goconf"
	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type Overrides map[*net.IPNet]Country

func (o Overrides) Lookup(ip net.IP) (Country, bool) {
	for overrideNet, country := range o {
		if overrideNet.Contains(ip) {
			return country, true
		}
	}

	return UnknownCountry, false
}

type AtomicOverrides struct {
	value atomic.Pointer[Overrides]
}

func (a *AtomicOverrides) Store(value Overrides) {
	if len(value) == 0 {
		a.value.Store(nil)
	} else {
		v := maps.Clone(value)
		a.value.Store(&v)
	}
}

func (a *AtomicOverrides) Load() Overrides {
	value := a.value.Load()
	if value == nil {
		return nil
	}

	return *value
}

func LoadOverrides(ctx context.Context, cfg *goconf.ConfigFile, ignoreErrors bool) (Overrides, error) {
	logger := log.LoggerFromContext(ctx)
	options, _ := config.GetStringOptions(cfg, "geoip-overrides", true)
	if len(options) == 0 {
		return nil, nil
	}

	var err error
	geoipOverrides := make(Overrides, len(options))
	for option, value := range options {
		var ip net.IP
		var ipNet *net.IPNet
		if strings.Contains(option, "/") {
			_, ipNet, err = net.ParseCIDR(option)
			if err != nil {
				if ignoreErrors {
					logger.Printf("could not parse CIDR %s (%s), skipping", option, err)
					continue
				}

				return nil, fmt.Errorf("could not parse CIDR %s: %w", option, err)
			}
		} else {
			ip = net.ParseIP(option)
			if ip == nil {
				if ignoreErrors {
					logger.Printf("could not parse IP %s, skipping", option)
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
			logger.Printf("IP %s doesn't have a country assigned, skipping", option)
			continue
		} else if !IsValidCountry(Country(value)) {
			logger.Printf("Country %s for IP %s is invalid, skipping", value, option)
			continue
		}

		logger.Printf("Using country %s for %s", value, ipNet)
		geoipOverrides[ipNet] = Country(value)
	}

	return geoipOverrides, nil
}
