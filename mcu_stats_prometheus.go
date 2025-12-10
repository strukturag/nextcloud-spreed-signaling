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

	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
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
	statsJanusSelectedCandidateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "selected_candidate_total",
		Help:      "Total number of selected candidates",
	}, []string{"origin", "type", "transport", "family"})
	statsJanusPeerConnectionStateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "peerconnection_state_total",
		Help:      "Total number of PeerConnections states",
	}, []string{"state", "reason"})
	statsJanusICEStateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "ice_state_total",
		Help:      "Total number of ICE connection states",
	}, []string{"state"})
	statsJanusDTLSStateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "dtls_state_total",
		Help:      "Total number of DTLS connection states",
	}, []string{"state"})
	statsJanusSlowLinkTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "slow_link_total",
		Help:      "Total number of slow link events",
	}, []string{"media", "direction"})
	statsJanusMediaRTT = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_rtt",
		Help:      "The roundtrip time of WebRTC media in milliseconds",
		Buckets:   prometheus.ExponentialBucketsRange(1, 10000, 25),
	}, []string{"media"})
	statsJanusMediaJitter = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_jitter",
		Help:      "The jitter of WebRTC media in milliseconds",
		Buckets:   prometheus.ExponentialBucketsRange(1, 2000, 20),
	}, []string{"media", "origin"})
	statsJanusMediaCodecsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_codecs_total",
		Help:      "The total number of codecs",
	}, []string{"media", "codec"})
	statsJanusMediaNACKTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_nacks_total",
		Help:      "The total number of NACKs",
	}, []string{"media", "direction"})
	statsJanusMediaRetransmissionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_retransmissions_total",
		Help:      "The total number of received retransmissions",
	}, []string{"media"})
	statsJanusMediaBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_bytes_total",
		Help:      "The total number of media bytes sent / received",
	}, []string{"media", "direction"})
	statsJanusMediaLostTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "mcu",
		Name:      "media_lost_total",
		Help:      "The total number of lost media packets",
	}, []string{"media", "origin"})

	janusMcuStats = []prometheus.Collector{
		statsJanusBandwidthCurrent,
		statsJanusSelectedCandidateTotal,
		statsJanusPeerConnectionStateTotal,
		statsJanusICEStateTotal,
		statsJanusDTLSStateTotal,
		statsJanusSlowLinkTotal,
		statsJanusMediaRTT,
		statsJanusMediaJitter,
		statsJanusMediaCodecsTotal,
		statsJanusMediaNACKTotal,
		statsJanusMediaRetransmissionsTotal,
		statsJanusMediaBytesTotal,
		statsJanusMediaLostTotal,
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

func RegisterJanusMcuStats() {
	metrics.RegisterAll(commonMcuStats...)
	metrics.RegisterAll(janusMcuStats...)
}

func UnregisterJanusMcuStats() {
	metrics.UnregisterAll(commonMcuStats...)
	metrics.UnregisterAll(janusMcuStats...)
}

func RegisterProxyMcuStats() {
	metrics.RegisterAll(commonMcuStats...)
	metrics.RegisterAll(proxyMcuStats...)
}

func UnregisterProxyMcuStats() {
	metrics.UnregisterAll(commonMcuStats...)
	metrics.UnregisterAll(proxyMcuStats...)
}
