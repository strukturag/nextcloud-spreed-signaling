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
	"github.com/prometheus/client_golang/prometheus"
)

var (
	statsPublishersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "publishers",
		Help:      "The current number of publishers",
	}, []string{"type"})
	statsPublishersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "publishers_total",
		Help:      "The total number of created publishers",
	}, []string{"type"})
	statsSubscribersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "subscribers",
		Help:      "The current number of subscribers",
	}, []string{"type"})
	statsSubscribersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "subscribers_total",
		Help:      "The total number of created subscribers",
	}, []string{"type"})
	statsWaitingForPublisherTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "nopublisher_total",
		Help:      "The total number of subscribe requests where no publisher exists",
	}, []string{"type"})
	statsMcuMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "messages_total",
		Help:      "The total number of MCU messages",
	}, []string{"type"})
	statsMcuSubscriberStreamTypesCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "subscriber_streams",
		Help:      "The current number of subscribed media streams",
	}, []string{"type"})
	statsMcuPublisherStreamTypesCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "publisher_streams",
		Help:      "The current number of published media streams",
	}, []string{"type"})

	commonMcuStats = []prometheus.Collector{
		statsPublishersCurrent,
		statsPublishersTotal,
		statsSubscribersCurrent,
		statsSubscribersTotal,
		statsWaitingForPublisherTotal,
		statsMcuMessagesTotal,
		statsMcuSubscriberStreamTypesCurrent,
		statsMcuPublisherStreamTypesCurrent,
	}

	statsJanusBandwidthCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "bandwidth",
		Help:      "The current bandwidth in bytes per second",
	}, []string{"direction"})

	janusMcuStats = []prometheus.Collector{
		statsJanusBandwidthCurrent,
	}

	statsConnectedProxyBackendsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "backend_connections",
		Help:      "Current number of connections to signaling proxy backends",
	}, []string{"country"})
	statsProxyBackendLoadCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "backend_load",
		Help:      "Current load of signaling proxy backends",
	}, []string{"url"})
	statsProxyUsageCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "backend_usage",
		Help:      "The current usage of signaling proxy backends in percent",
	}, []string{"url", "direction"})
	statsProxyNobackendAvailableTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "no_backend_available_total",
		Help:      "Total number of publishing requests where no backend was available",
	}, []string{"type"})

	proxyMcuStats = []prometheus.Collector{
		statsConnectedProxyBackendsCurrent,
		statsProxyBackendLoadCurrent,
		statsProxyUsageCurrent,
		statsProxyNobackendAvailableTotal,
	}
)

func RegisterJanusMcuStats() {
	registerAll(commonMcuStats...)
	registerAll(janusMcuStats...)
}

func UnregisterJanusMcuStats() {
	unregisterAll(commonMcuStats...)
	unregisterAll(janusMcuStats...)
}

func RegisterProxyMcuStats() {
	registerAll(commonMcuStats...)
	registerAll(proxyMcuStats...)
}

func UnregisterProxyMcuStats() {
	unregisterAll(commonMcuStats...)
	unregisterAll(proxyMcuStats...)
}
