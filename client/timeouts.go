/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
 */package client

import (
	"strconv"
	"time"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
)

const (
	// DefaultPingInterval is the default interval in which ping messages are
	// sent to websocket clients.
	DefaultPingInterval = 45 * time.Second

	// DefaultReadTimeout is the default timeout for reading from connections.
	DefaultReadTimeout = 30 * time.Second

	// DefaultWriteTimeout is the default timeout for writing to connections.
	DefaultWriteTimeout = 30 * time.Second
)

// Timeouts contain the timeouts used for (websocket) connections.
type Timeouts struct {
	// PingInterval is the interval in which ping messages are sent.
	PingInterval time.Duration
	// ReadTimeout is the time allowed to read a message. For websocket
	// connections, this is the time the peer has to respond to a ping.
	ReadTimeout time.Duration
	// WriteTimeout is the time allowed to write a message.
	WriteTimeout time.Duration
}

// ReadWait returns the time allowed to read the next message from a websocket
// peer that is sent pings in the configured interval. The peer must respond to
// pings within the read timeout.
func (t Timeouts) ReadWait() time.Duration {
	return t.PingInterval + t.ReadTimeout
}

// WithDefaults returns a copy of the timeouts where all values that are not
// set are replaced by the package defaults.
func (t Timeouts) WithDefaults() Timeouts {
	if t.PingInterval <= 0 {
		t.PingInterval = DefaultPingInterval
	}
	if t.ReadTimeout <= 0 {
		t.ReadTimeout = DefaultReadTimeout
	}
	if t.WriteTimeout <= 0 {
		t.WriteTimeout = DefaultWriteTimeout
	}
	return t
}

func getSeconds(logger log.Logger, cfg *goconf.ConfigFile, section string, option string, defaultValue time.Duration) time.Duration {
	value, _ := cfg.GetString(section, option)
	if value == "" {
		return defaultValue
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		logger.Printf("Invalid value \"%s\" for \"%s\" in section \"%s\", using default of %s", value, option, section, defaultValue)
		return defaultValue
	}

	return time.Duration(seconds) * time.Second
}

// LoadTimeouts loads the "readtimeout", "writetimeout" and the "pinginterval"
// settings from the given section.
// Missing or invalid values are taken from the given defaults (or the package
// defaults if not set there).
func LoadTimeouts(logger log.Logger, cfg *goconf.ConfigFile, section string, defaults Timeouts) Timeouts {
	defaults = defaults.WithDefaults()
	return Timeouts{
		PingInterval: getSeconds(logger, cfg, section, "pinginterval", defaults.PingInterval),
		ReadTimeout:  getSeconds(logger, cfg, section, "readtimeout", defaults.ReadTimeout),
		WriteTimeout: getSeconds(logger, cfg, section, "writetimeout", defaults.WriteTimeout),
	}
}
