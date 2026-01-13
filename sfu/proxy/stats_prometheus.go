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

package proxy

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/internal"
)

var (
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
	statsProxyBandwidthCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "backend_bandwidth",
		Help:      "The current bandwidth of signaling proxy backends in bytes per second",
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
		statsProxyBandwidthCurrent,
		statsProxyNobackendAvailableTotal,
	}
)

func RegisterStats() {
	internal.RegisterCommonStats()
	metrics.RegisterAll(proxyMcuStats...)
}

func UnregisterStats() {
	internal.UnregisterCommonStats()
	metrics.UnregisterAll(proxyMcuStats...)
}
