/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
package async

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
)

var (
	statsThrottleDelayedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "throttle",
		Name:      "delayed_total",
		Help:      "The total number of delayed requests",
	}, []string{"action", "delay"})

	statsThrottleBruteforceTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "throttle",
		Name:      "bruteforce_total",
		Help:      "The total number of rejected bruteforce requests",
	}, []string{"action"})

	throttleStats = []prometheus.Collector{
		statsThrottleDelayedTotal,
		statsThrottleBruteforceTotal,
	}
)

func RegisterThrottleStats() {
	metrics.RegisterAll(throttleStats...)
}
