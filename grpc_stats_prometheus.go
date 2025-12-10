/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	statsGrpcClients = prometheus.NewGauge(prometheus.GaugeOpts{ // +checklocksignore: Global readonly variable.
		Namespace: "signaling",
		Subsystem: "grpc",
		Name:      "clients",
		Help:      "The current number of GRPC clients",
	})
	statsGrpcClientCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "grpc",
		Name:      "client_calls_total",
		Help:      "The total number of GRPC client calls",
	}, []string{"method"})

	grpcClientStats = []prometheus.Collector{
		statsGrpcClients,
		statsGrpcClientCalls,
	}

	statsGrpcServerCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signaling",
		Subsystem: "grpc",
		Name:      "server_calls_total",
		Help:      "The total number of GRPC server calls",
	}, []string{"method"})

	grpcServerStats = []prometheus.Collector{
		statsGrpcServerCalls,
	}
)

func RegisterGrpcClientStats() {
	metrics.RegisterAll(grpcClientStats...)
}

func RegisterGrpcServerStats() {
	metrics.RegisterAll(grpcServerStats...)
}
