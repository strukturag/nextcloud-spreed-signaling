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
package server

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
)

var (
	statsRoomSessionsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "room",
		Name:      "sessions",
		Help:      "The current number of sessions in a room",
	}, []string{"backend", "room", "clienttype"})
	statsCallSessionsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "call",
		Name:      "sessions",
		Help:      "The current number of sessions in a call",
	}, []string{"backend", "room", "clienttype"})
	statsCallSessionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "call",
		Name:      "sessions_total",
		Help:      "The total number of sessions in a call",
	}, []string{"backend", "clienttype"})
	statsCallRoomsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "call",
		Name:      "rooms_total",
		Help:      "The total number of rooms with an active call",
	}, []string{"backend"})

	roomStats = []prometheus.Collector{
		statsRoomSessionsCurrent,
		statsCallSessionsCurrent,
		statsCallSessionsTotal,
		statsCallRoomsTotal,
	}
)

func RegisterRoomStats() {
	metrics.RegisterAll(roomStats...)
}
