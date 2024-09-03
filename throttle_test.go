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
)

func newMemoryThrottlerForTest(t *testing.T) *memoryThrottler {
	t.Helper()
	result, err := NewMemoryThrottler()
	require.NoError(t, err)

	t.Cleanup(func() {
		result.Close()
	})

	return result.(*memoryThrottler)
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

func TestThrottler(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle1(ctx)

	timing.now = timing.now.Add(time.Millisecond)
	throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle2(ctx)

	timing.now = timing.now.Add(time.Millisecond)
	throttle3, err := th.CheckBruteforce(ctx, "192.168.0.2", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle3(ctx)

	timing.now = timing.now.Add(time.Millisecond)
	throttle4, err := th.CheckBruteforce(ctx, "192.168.0.1", "action2")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle4(ctx)
}

func TestThrottlerIPv6(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	// Make sure full /64 subnets are throttled for IPv6.
	throttle1, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle1(ctx)

	timing.now = timing.now.Add(time.Millisecond)
	throttle2, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::2", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle2(ctx)

	// A diffent /64 subnet is not throttled yet.
	timing.now = timing.now.Add(time.Millisecond)
	throttle3, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0013::1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle3(ctx)

	// A different action is not throttled.
	timing.now = timing.now.Add(time.Millisecond)
	throttle4, err := th.CheckBruteforce(ctx, "2001:db8:abcd:0012::1", "action2")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle4(ctx)
}

func TestThrottler_Bruteforce(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	for i := 0; i < maxBruteforceAttempts; i++ {
		timing.now = timing.now.Add(time.Millisecond)
		throttle, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		assert.NoError(err)
		if i == 0 {
			timing.expectedSleep = 100 * time.Millisecond
		} else {
			timing.expectedSleep *= 2
			if timing.expectedSleep > maxThrottleDelay {
				timing.expectedSleep = maxThrottleDelay
			}
		}
		throttle(ctx)
	}

	timing.now = timing.now.Add(time.Millisecond)
	_, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.ErrorIs(err, ErrBruteforceDetected)
}

func TestThrottler_Cleanup(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle1(ctx)

	throttle2, err := th.CheckBruteforce(ctx, "192.168.0.2", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle2(ctx)

	timing.now = timing.now.Add(time.Hour)
	throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action2")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle3(ctx)

	throttle4, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle4(ctx)

	timing.now = timing.now.Add(-time.Hour).Add(maxBruteforceAge).Add(time.Second)
	th.cleanup(timing.now)

	assert.Len(th.getEntries("192.168.0.1", "action1"), 1)
	assert.Len(th.getEntries("192.168.0.1", "action2"), 1)

	th.mu.RLock()
	if _, found := th.clients["192.168.0.2"]; found {
		assert.Fail("should have removed client \"192.168.0.2\"")
	}
	th.mu.RUnlock()

	throttle5, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle5(ctx)
}

func TestThrottler_ExpirePartial(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle1(ctx)

	timing.now = timing.now.Add(time.Minute)

	throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle2(ctx)

	timing.now = timing.now.Add(maxBruteforceAge).Add(-time.Minute + time.Second)

	throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle3(ctx)
}

func TestThrottler_ExpireAll(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	throttle1, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle1(ctx)

	timing.now = timing.now.Add(time.Millisecond)

	throttle2, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 200 * time.Millisecond
	throttle2(ctx)

	timing.now = timing.now.Add(maxBruteforceAge).Add(time.Second)

	throttle3, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
	assert.NoError(err)
	timing.expectedSleep = 100 * time.Millisecond
	throttle3(ctx)
}

func TestThrottler_Negative(t *testing.T) {
	assert := assert.New(t)
	timing := &throttlerTiming{
		t:   t,
		now: time.Now(),
	}
	th := newMemoryThrottlerForTest(t)
	th.getNow = timing.getNow
	th.doDelay = timing.doDelay

	ctx := context.Background()

	for i := 0; i < maxBruteforceAttempts*10; i++ {
		timing.now = timing.now.Add(time.Millisecond)
		throttle, err := th.CheckBruteforce(ctx, "192.168.0.1", "action1")
		if err != nil {
			assert.ErrorIs(err, ErrBruteforceDetected)
		}
		if i == 0 {
			timing.expectedSleep = 100 * time.Millisecond
		} else {
			timing.expectedSleep *= 2
			if timing.expectedSleep > maxThrottleDelay {
				timing.expectedSleep = maxThrottleDelay
			}
		}
		throttle(ctx)
	}
}
