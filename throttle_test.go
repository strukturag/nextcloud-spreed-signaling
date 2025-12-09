/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func newMemoryThrottlerForTest(t *testing.T) Throttler {
	t.Helper()
	result, err := NewMemoryThrottler()
	require.NoError(t, err)

	t.Cleanup(func() {
		result.Close()
	})

	return result
}

type throttlerTiming struct {
	t *testing.T

	now           time.Time
	expectedSleep time.Duration
}

func (t *throttlerTiming) getNow() time.Time {
	return t.now
}

func (t *throttlerTiming) doDelay(ctx context.Context, duration time.Duration) {
	t.t.Helper()
	assert.Equal(t.t, t.expectedSleep, duration)
}

func expectDelay(t *testing.T, f func(), delay time.Duration) {
	t.Helper()
	a := time.Now()
	f()
	b := time.Now()
	assert.Equal(t, delay, b.Sub(a))
}

func TestThrottler(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle1(ctx)
		}, 100*time.Millisecond)

		throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle2(ctx)
		}, 200*time.Millisecond)

		throttle3, err := th.CheckBruteforce(ctx, "192.168.0.2", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle3(ctx)
		}, 100*time.Millisecond)

		throttle4, err := th.CheckBruteforce(ctx, "192.168.0.1", "action2")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle4(ctx)
		}, 100*time.Millisecond)
	})
}

func TestThrottlerIPv6(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		// Make sure full /64 subnets are throttled for IPv6.
		throttle1, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle1(ctx)
		}, 100*time.Millisecond)

		throttle2, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::2", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle2(ctx)
		}, 200*time.Millisecond)

		// A diffent /64 subnet is not throttled yet.
		throttle3, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0013::1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle3(ctx)
		}, 100*time.Millisecond)

		// A different action is not throttled.
		throttle4, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::1", "action2")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle4(ctx)
		}, 100*time.Millisecond)
	})
}

func TestThrottler_Bruteforce(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		delay := 100 * time.Millisecond
		for range maxBruteforceAttempts {
			throttle, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
			assert.NoError(err)
			expectDelay(t, func() {
				throttle(ctx)
			}, delay)
			delay *= 2
			if delay > maxThrottleDelay {
				delay = maxThrottleDelay
			}
		}

		_, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.ErrorIs(err, ErrBruteforceDetected)
	})
}

func TestThrottler_Cleanup(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		throttler := newMemoryThrottlerForTest(t)
		th, ok := throttler.(*memoryThrottler)
		require.True(t, ok, "required memoryThrottler, got %T", throttler)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle1(ctx)
		}, 100*time.Millisecond)

		throttle2, err := th.CheckBruteforce(ctx, "192.168.0.2", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle2(ctx)
		}, 100*time.Millisecond)

		time.Sleep(time.Hour)

		throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action2")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle3(ctx)
		}, 100*time.Millisecond)

		throttle4, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle4(ctx)
		}, 200*time.Millisecond)

		cleanupNow := time.Now().Add(-time.Hour).Add(maxBruteforceAge).Add(time.Second)
		th.cleanup(cleanupNow)

		assert.Len(th.getEntries("192.168.0.1", "action1"), 1)
		assert.Len(th.getEntries("192.168.0.1", "action2"), 1)

		th.mu.RLock()
		if entries, found := th.clients["192.168.0.2"]; found {
			assert.Fail("should have removed client \"192.168.0.2\"", "got %+v", entries)
		}
		th.mu.RUnlock()

		throttle5, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle5(ctx)
		}, 200*time.Millisecond)
	})
}

func TestThrottler_ExpirePartial(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle1(ctx)
		}, 100*time.Millisecond)

		time.Sleep(time.Minute)

		throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle2(ctx)
		}, 200*time.Millisecond)

		time.Sleep(maxBruteforceAge - time.Minute + time.Second)

		throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle3(ctx)
		}, 200*time.Millisecond)
	})
}

func TestThrottler_ExpireAll(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle1(ctx)
		}, 100*time.Millisecond)

		time.Sleep(time.Millisecond)

		throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle2(ctx)
		}, 200*time.Millisecond)

		time.Sleep(maxBruteforceAge + time.Second)

		throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		expectDelay(t, func() {
			throttle3(ctx)
		}, 100*time.Millisecond)
	})
}

func TestThrottler_Negative(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		th := newMemoryThrottlerForTest(t)

		logger := log.NewLoggerForTest(t)
		ctx := log.NewLoggerContext(t.Context(), logger)

		delay := 100 * time.Millisecond
		for range maxBruteforceAttempts * 10 {
			throttle, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
			if err != nil {
				assert.ErrorIs(err, ErrBruteforceDetected)
			}
			expectDelay(t, func() {
				throttle(ctx)
			}, delay)
			delay *= 2
			if delay > maxThrottleDelay {
				delay = maxThrottleDelay
			}
		}
	})
}
