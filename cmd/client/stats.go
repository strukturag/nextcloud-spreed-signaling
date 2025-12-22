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
package main

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

type Stats struct {
	numRecvMessages   atomic.Int64
	numSentMessages   atomic.Int64
	resetRecvMessages int64
	resetSentMessages int64
	numRecvBytes      atomic.Uint64
	numSentBytes      atomic.Uint64
	resetRecvBytes    uint64
	resetSentBytes    uint64

	start time.Time
}

func (s *Stats) reset(start time.Time) {
	s.resetRecvMessages = s.numRecvMessages.Load()
	s.resetSentMessages = s.numSentMessages.Load()
	s.resetRecvBytes = s.numRecvBytes.Load()
	s.resetSentBytes = s.numSentBytes.Load()
	s.start = start
}

type statsLogEntries struct {
	totalSentMessages  int64
	sentMessagesPerSec int64
	sentBytesPerSec    api.Bandwidth

	totalRecvMessages  int64
	recvMessagesPerSec int64
	recvBytesPerSec    api.Bandwidth
}

func (s *Stats) getLogEntries(now time.Time) *statsLogEntries {
	duration := now.Sub(s.start)
	perSec := int64(duration / time.Second)
	if perSec == 0 {
		return nil
	}

	totalSentMessages := s.numSentMessages.Load()
	sentMessages := totalSentMessages - s.resetSentMessages
	sentBytes := api.BandwidthFromBytes(s.numSentBytes.Load() - s.resetSentBytes)
	totalRecvMessages := s.numRecvMessages.Load()
	recvMessages := totalRecvMessages - s.resetRecvMessages
	recvBytes := api.BandwidthFromBytes(s.numRecvBytes.Load() - s.resetRecvBytes)

	s.reset(now)
	return &statsLogEntries{
		totalSentMessages:  totalSentMessages,
		sentMessagesPerSec: sentMessages / perSec,
		sentBytesPerSec:    sentBytes,

		totalRecvMessages:  totalRecvMessages,
		recvMessagesPerSec: recvMessages / perSec,
		recvBytesPerSec:    recvBytes,
	}
}

func (s *Stats) Log() {
	now := time.Now()
	if entries := s.getLogEntries(now); entries != nil {
		log.Printf("Stats: sent=%d (%d/sec, %s), recv=%d (%d/sec, %s), delta=%d",
			entries.totalSentMessages, entries.sentMessagesPerSec, entries.sentBytesPerSec,
			entries.totalRecvMessages, entries.recvMessagesPerSec, entries.recvBytesPerSec,
			entries.totalSentMessages-entries.totalRecvMessages)
	}
}
