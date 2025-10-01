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
	statsBackendLimit = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "backend",
		Name:      "session_limit",
		Help:      "The session limit of a backend",
	}, []string{"backend"})
	statsBackendLimitExceededTotal = prometheus.NewCounterVec(prometheus.CounterOpts{ // +checklocksignore: Global readonly variable.
		Namespace: "signaling",
		Subsystem: "backend",
		Name:      "session_limit_exceeded_total",
		Help:      "The number of times the session limit exceeded",
	}, []string{"backend"})
	statsBackendsCurrent = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "backend",
		Name:      "current",
		Help:      "The current number of configured backends",
	})

	backendConfigurationStats = []prometheus.Collector{
		statsBackendLimit,
		statsBackendLimitExceededTotal,
		statsBackendsCurrent,
	}
)

func RegisterBackendConfigurationStats() {
	registerAll(backendConfigurationStats...)
}

func updateBackendStats(backend *Backend) {
	if backend.sessionLimit > 0 {
		statsBackendLimit.WithLabelValues(backend.id).Set(float64(backend.sessionLimit))
	} else {
		statsBackendLimit.DeleteLabelValues(backend.id)
	}
}

func deleteBackendStats(backend *Backend) {
	statsBackendLimit.DeleteLabelValues(backend.id)
}
