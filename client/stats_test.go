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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

func TestStats(t *testing.T) {
	assert := assert.New(t)

	var stats Stats
	assert.Nil(stats.getLogEntries(time.Time{}))
	now := time.Now()
	if entries := stats.getLogEntries(now); assert.NotNil(entries) {
		assert.EqualValues(0, entries.totalSentMessages)
		assert.EqualValues(0, entries.sentMessagesPerSec)
		assert.EqualValues(0, entries.sentBytesPerSec)

		assert.EqualValues(0, entries.totalRecvMessages)
		assert.EqualValues(0, entries.recvMessagesPerSec)
		assert.EqualValues(0, entries.recvBytesPerSec)
	}

	stats.numSentMessages.Add(10)
	stats.numSentBytes.Add((api.Bandwidth(20) * api.Kilobit).Bits())

	stats.numRecvMessages.Add(30)
	stats.numRecvBytes.Add((api.Bandwidth(40) * api.Kilobit).Bits())

	if entries := stats.getLogEntries(now.Add(time.Second)); assert.NotNil(entries) {
		assert.EqualValues(10, entries.totalSentMessages)
		assert.EqualValues(10, entries.sentMessagesPerSec)
		assert.EqualValues(20*1024*8, entries.sentBytesPerSec)

		assert.EqualValues(30, entries.totalRecvMessages)
		assert.EqualValues(30, entries.recvMessagesPerSec)
		assert.EqualValues(40*1024*8, entries.recvBytesPerSec)
	}

	stats.numSentMessages.Add(100)
	stats.numSentBytes.Add((api.Bandwidth(200) * api.Kilobit).Bits())

	stats.numRecvMessages.Add(300)
	stats.numRecvBytes.Add((api.Bandwidth(400) * api.Kilobit).Bits())

	if entries := stats.getLogEntries(now.Add(2 * time.Second)); assert.NotNil(entries) {
		assert.EqualValues(110, entries.totalSentMessages)
		assert.EqualValues(100, entries.sentMessagesPerSec)
		assert.EqualValues(200*1024*8, entries.sentBytesPerSec)

		assert.EqualValues(330, entries.totalRecvMessages)
		assert.EqualValues(300, entries.recvMessagesPerSec)
		assert.EqualValues(400*1024*8, entries.recvBytesPerSec)
	}
}
