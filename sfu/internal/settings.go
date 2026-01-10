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
 */
package internal

import (
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

var (
	defaultMaxStreamBitrate = api.BandwidthFromMegabits(1)
	defaultMaxScreenBitrate = api.BandwidthFromMegabits(2)
)

type CommonSettings struct {
	Logger log.Logger

	maxStreamBitrate api.AtomicBandwidth
	maxScreenBitrate api.AtomicBandwidth

	timeout atomic.Int64
}

func (s *CommonSettings) MaxStreamBitrate() api.Bandwidth {
	return s.maxStreamBitrate.Load()
}

func (s *CommonSettings) MaxScreenBitrate() api.Bandwidth {
	return s.maxScreenBitrate.Load()
}

func (s *CommonSettings) Timeout() time.Duration {
	return time.Duration(s.timeout.Load())
}

func (s *CommonSettings) SetTimeout(timeout time.Duration) {
	s.timeout.Store(timeout.Nanoseconds())
}

func (s *CommonSettings) Load(config *goconf.ConfigFile) error {
	maxStreamBitrateValue, _ := config.GetInt("mcu", "maxstreambitrate")
	if maxStreamBitrateValue <= 0 {
		maxStreamBitrateValue = int(defaultMaxStreamBitrate.Bits())
	}
	maxStreamBitrate := api.BandwidthFromBits(uint64(maxStreamBitrateValue))
	s.Logger.Printf("Maximum bandwidth %s per publishing stream", maxStreamBitrate)
	s.maxStreamBitrate.Store(maxStreamBitrate)

	maxScreenBitrateValue, _ := config.GetInt("mcu", "maxscreenbitrate")
	if maxScreenBitrateValue <= 0 {
		maxScreenBitrateValue = int(defaultMaxScreenBitrate.Bits())
	}
	maxScreenBitrate := api.BandwidthFromBits(uint64(maxScreenBitrateValue))
	s.Logger.Printf("Maximum bandwidth %s per screensharing stream", maxScreenBitrate)
	s.maxScreenBitrate.Store(maxScreenBitrate)
	return nil
}
