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
package signaling

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	statsBackendClientRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "backend_client",
		Name:      "requests_total",
		Help:      "The total number of backend client requests",
	}, []string{"backend"})
	statsBackendClientDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "signaling",
		Subsystem: "backend_client",
		Name:      "requests_duration",
		Help:      "The duration of backend client requests in seconds",
		Buckets:   prometheus.ExponentialBucketsRange(0.01, 30, 30),
	}, []string{"backend"})
	statsBackendClientError = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "backend_client",
		Name:      "requests_errors_total",
		Help:      "The total number of backend client requests that had an error",
	}, []string{"backend", "error"})

	backendClientStats = []prometheus.Collector{
		statsBackendClientRequests,
		statsBackendClientDuration,
		statsBackendClientError,
	}
)

func RegisterBackendClientStats() {
	registerAll(backendClientStats...)
}
