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
	statsMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "server",
		Name:      "messages_total",
		Help:      "The total number of signaling messages",
	}, []string{"type"})

	signalingStats = []prometheus.Collector{
		statsMessagesTotal,
	}
)

func registerAll(cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := prometheus.DefaultRegisterer.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
	}
}

func unregisterAll(cs ...prometheus.Collector) {
	for _, c := range cs {
		prometheus.Unregister(c)
	}
}

func RegisterStats() {
	registerAll(signalingStats...)
}
