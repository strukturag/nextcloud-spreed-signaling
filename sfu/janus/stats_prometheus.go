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
package janus

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/strukturag/nextcloud-spreed-signaling/metrics"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/internal"
)

var (
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
		statsMcuSubscriberStreamTypesCurrent,
		statsMcuPublisherStreamTypesCurrent,
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
)

func RegisterStats() {
	internal.RegisterCommonStats()
	metrics.RegisterAll(janusMcuStats...)
}

func UnregisterStats() {
	internal.UnregisterCommonStats()
	metrics.UnregisterAll(janusMcuStats...)
}
