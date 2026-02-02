/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestRegistration(t *testing.T) {
	t.Parallel()

	collectors := []prometheus.Collector{
		prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "signaling",
			Subsystem: "test",
			Name:      "value_total",
			Help:      "Total value.",
		}),
		prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "signaling",
			Subsystem: "test",
			Name:      "value",
			Help:      "Current value.",
		}, []string{"foo", "bar"}),
	}
	// Can unregister without previous registration
	UnregisterAll(collectors...)
	RegisterAll(collectors...)
	// Can register multiple times
	RegisterAll(collectors...)
	UnregisterAll(collectors...)
}

func TestRegistrationError(t *testing.T) {
	t.Parallel()

	defer func() {
		value := recover()
		if err, ok := value.(error); assert.True(t, ok) {
			assert.ErrorContains(t, err, "is not a valid metric name")
		}
	}()

	RegisterAll(prometheus.NewCounter(prometheus.CounterOpts{}))
}
