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
package test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func ResetStatsValue[T prometheus.Gauge](t *testing.T, collector T) {
	// Make sure test is not executed with "t.Parallel()"
	t.Setenv("PARALLEL_CHECK", "1")

	collector.Set(0)
	t.Cleanup(func() {
		collector.Set(0)
	})
}

func AssertCollectorChangeBy(t *testing.T, collector prometheus.Collector, delta float64) {
	t.Helper()

	ch := make(chan *prometheus.Desc, 1)
	collector.Describe(ch)
	desc := <-ch

	before := testutil.ToFloat64(collector)
	t.Cleanup(func() {
		t.Helper()

		after := testutil.ToFloat64(collector)
		assert.InEpsilon(t, delta, after-before, 0.0001, "failed for %s", desc)
	})
}

func CheckStatsValue(t *testing.T, collector prometheus.Collector, value float64) { // nolint:unused
	// Make sure test is not executed with "t.Parallel()"
	t.Setenv("PARALLEL_CHECK", "1")

	ch := make(chan *prometheus.Desc, 1)
	collector.Describe(ch)
	desc := <-ch
	v := testutil.ToFloat64(collector)
	if v != value {
		assert := assert.New(t)
		pc := make([]uintptr, 10)
		n := runtime.Callers(2, pc)
		if n == 0 {
			assert.InDelta(value, v, 0.0001, "failed for %s", desc)
			return
		}

		pc = pc[:n]
		frames := runtime.CallersFrames(pc)
		var stack strings.Builder
		for {
			frame, more := frames.Next()
			if !strings.Contains(frame.File, "nextcloud-spreed-signaling") {
				break
			}
			fmt.Fprintf(&stack, "%s:%d\n", frame.File, frame.Line)
			if !more {
				break
			}
		}
		assert.InDelta(value, v, 0.0001, "Unexpected value for %s at\n%s", desc, stack.String())
	}
}

func CollectAndLint(t *testing.T, collectors ...prometheus.Collector) {
	assert := assert.New(t)
	for _, collector := range collectors {
		problems, err := testutil.CollectAndLint(collector)
		if !assert.NoError(err) {
			continue
		}

		for _, problem := range problems {
			assert.Fail("Problem with metric", "%s: %s", problem.Metric, problem.Text)
		}
	}
}
