/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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

func TestPublisherStatsCounter(t *testing.T) {
	RegisterJanusMcuStats()

	var c publisherStatsCounter

	c.Reset()
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	c.EnableStream("audio", false)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	c.EnableStream("audio", true)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	c.EnableStream("audio", true)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	c.EnableStream("video", true)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	c.EnableStream("audio", false)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	c.EnableStream("audio", false)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)

	c.AddSubscriber("1")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 1)
	c.EnableStream("audio", true)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 1)
	c.AddSubscriber("1")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 1)

	c.AddSubscriber("2")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 2)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 2)

	c.RemoveSubscriber("3")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 2)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 2)

	c.RemoveSubscriber("1")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 1)

	c.AddSubscriber("1")
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 2)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 2)

	c.EnableStream("audio", false)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 2)

	c.EnableStream("audio", true)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 1)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 1)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 2)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 2)

	c.EnableStream("audio", false)
	c.EnableStream("video", false)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuPublisherStreamTypesCurrent.WithLabelValues("video"), 0)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("audio"), 0)
	checkStatsValue(t, statsMcuSubscriberStreamTypesCurrent.WithLabelValues("video"), 0)
}
