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
package internal

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
)

var (
	StatsPublishersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "publishers",
		Help:      "The current number of publishers",
	}, []string{"type"})
	StatsPublishersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "publishers_total",
		Help:      "The total number of created publishers",
	}, []string{"type"})
	StatsSubscribersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "subscribers",
		Help:      "The current number of subscribers",
	}, []string{"type"})
	StatsSubscribersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "subscribers_total",
		Help:      "The total number of created subscribers",
	}, []string{"type"})
	StatsWaitingForPublisherTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "nopublisher_total",
		Help:      "The total number of subscribe requests where no publisher exists",
	}, []string{"type"})
	StatsMcuMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "messages_total",
		Help:      "The total number of MCU messages",
	}, []string{"type"})

	commonMcuStats = []prometheus.Collector{
		StatsPublishersCurrent,
		StatsPublishersTotal,
		StatsSubscribersCurrent,
		StatsSubscribersTotal,
		StatsWaitingForPublisherTotal,
		StatsMcuMessagesTotal,
	}
)

func RegisterCommonStats() {
	metrics.RegisterAll(commonMcuStats...)
}

func UnregisterCommonStats() {
	metrics.UnregisterAll(commonMcuStats...)
}
