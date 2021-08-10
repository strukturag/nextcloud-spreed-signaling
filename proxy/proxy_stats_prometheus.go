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
package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	statsSessionsCurrent = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "sessions",
		Help:      "The current number of sessions",
	})
	statsSessionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "sessions_total",
		Help:      "The total number of created sessions",
	})
	statsSessionsResumedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "sessions_resumed_total",
		Help:      "The total number of resumed sessions",
	})
	statsPublishersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "publishers",
		Help:      "The current number of publishers",
	}, []string{"type"})
	statsPublishersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "publishers_total",
		Help:      "The total number of created publishers",
	}, []string{"type"})
	statsSubscribersCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "subscribers",
		Help:      "The current number of subscribers",
	}, []string{"type"})
	statsSubscribersTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "subscribers_total",
		Help:      "The total number of created subscribers",
	}, []string{"type"})
	statsCommandMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "command_messages_total",
		Help:      "The total number of command messages",
	}, []string{"type"})
	statsPayloadMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "payload_messages_total",
		Help:      "The total number of payload messages",
	}, []string{"type"})
	statsTokenErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "proxy",
		Name:      "token_errors_total",
		Help:      "The total number of token errors",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(statsSessionsCurrent)
	prometheus.MustRegister(statsSessionsTotal)
	prometheus.MustRegister(statsSessionsResumedTotal)
	prometheus.MustRegister(statsPublishersCurrent)
	prometheus.MustRegister(statsPublishersTotal)
	prometheus.MustRegister(statsSubscribersCurrent)
	prometheus.MustRegister(statsSubscribersTotal)
	prometheus.MustRegister(statsCommandMessagesTotal)
	prometheus.MustRegister(statsPayloadMessagesTotal)
	prometheus.MustRegister(statsTokenErrorsTotal)
}
