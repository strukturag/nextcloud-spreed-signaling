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
	statsClientCountries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "client",
		Name:      "countries_total",
		Help:      "The total number of connections by country",
	}, []string{"country"})
	statsClientRTT = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "signaling",
		Subsystem: "client",
		Name:      "rtt",
		Help:      "The roundtrip time of WebSocket ping messages in milliseconds",
		Buckets:   prometheus.ExponentialBucketsRange(1, 30000, 50),
	})

	clientStats = []prometheus.Collector{
		statsClientCountries,
		statsClientRTT,
	}
)

func RegisterClientStats() {
	registerAll(clientStats...)
}
