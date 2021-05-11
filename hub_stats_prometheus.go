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
	statsHubRoomsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "hub",
		Name:      "rooms",
		Help:      "The current number of rooms per backend",
	}, []string{"backend"})
	statsHubSessionsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "hub",
		Name:      "sessions",
		Help:      "The current number of sessions per backend",
	}, []string{"backend", "clienttype"})
	statsHubSessionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "hub",
		Name:      "sessions_total",
		Help:      "The total number of sessions per backend",
	}, []string{"backend", "clienttype"})
	statsHubSessionsResumedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "hub",
		Name:      "sessions_resume_total",
		Help:      "The total number of resumed sessions per backend",
	}, []string{"backend", "clienttype"})
	statsHubSessionResumeFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "hub",
		Name:      "sessions_resume_failed_total",
		Help:      "The total number of failed session resume requests",
	})

	hubStats = []prometheus.Collector{
		statsHubRoomsCurrent,
		statsHubSessionsCurrent,
		statsHubSessionsTotal,
		statsHubSessionResumeFailed,
	}
)

func RegisterHubStats() {
	registerAll(hubStats...)
}
