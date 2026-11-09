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
package async

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifierNoWaiter(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	// Notifications can be sent even if no waiter exists.
	signaler, release := notifier.NewSignaler("foo")
	defer release()
	signaler.Signal()
}

func TestNotifierWaitTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var notifier Notifier

		notified := make(chan struct{})
		go func() {
			defer close(notified)
			time.Sleep(time.Second)

			signaler, release := notifier.NewSignaler("foo")
			defer release()
			signaler.Signal()
		}()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		waiter, release := notifier.NewWaiter("foo")
		defer release()

		err := waiter.Wait(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		<-notified

		assert.NoError(t, waiter.Wait(t.Context()))
	})
}

func TestNotifierSimple(t *testing.T) {
	t.Parallel()

	var notifier Notifier
	waiter, release := notifier.NewWaiter("foo")
	defer release()

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, waiter.Wait(ctx))
	})

	signaler, release := notifier.NewSignaler("foo")
	defer release()
	signaler.Signal()

	wg.Wait()
}

func TestNotifierMultiNotify(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	_, release := notifier.NewWaiter("foo")
	defer release()

	signaler, release := notifier.NewSignaler("foo")
	defer release()
	signaler.Signal()

	// The second notification will be ignored while the first is still pending.
	signaler.Signal()
}

func TestNotifierWaitClosed(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	waiter, release := notifier.NewWaiter("foo")
	release()

	assert.NoError(t, waiter.Wait(context.Background()))
}

func TestNotifierWaitClosedMulti(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	waiter1, release1 := notifier.NewWaiter("foo")
	waiter2, release2 := notifier.NewWaiter("foo")
	release1()
	release2()

	assert.NoError(t, waiter1.Wait(context.Background()))
	assert.NoError(t, waiter2.Wait(context.Background()))
}

func TestNotifierResetWillNotify(t *testing.T) {
	t.Parallel()

	var notifier Notifier
	waiter, release := notifier.NewWaiter("foo")
	defer release()

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, waiter.Wait(ctx))
	})

	notifier.Reset()
	wg.Wait()
}

func TestNotifierDuplicate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var notifier Notifier
		var done sync.WaitGroup

		for range 2 {
			done.Go(func() {
				waiter, release := notifier.NewWaiter("foo")
				defer release()

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				assert.NoError(t, waiter.Wait(ctx))
			})
		}

		synctest.Wait()

		signaler, release := notifier.NewSignaler("foo")
		defer release()
		signaler.Signal()

		done.Wait()
	})
}

func TestNotifierSignalBeforeWait(t *testing.T) {
	t.Parallel()

	var notifier Notifier

	signaler, release := notifier.NewSignaler("foo")
	defer release()
	signaler.Signal()

	waiter, release := notifier.NewWaiter("foo")
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.NoError(t, waiter.Wait(ctx))
}

func TestNotifierDuplicateSignaler(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		assert := assert.New(t)
		var notifier Notifier

		// Multiple signalers can exist for the same key, waiters are notified by
		// any of them.
		signaler1, release1 := notifier.NewSignaler("foo")
		defer release1()
		signaler2, release2 := notifier.NewSignaler("foo")
		defer release2()

		waiter, release := notifier.NewWaiter("foo")
		defer release()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		var done sync.WaitGroup
		done.Go(func() {
			assert.NoError(waiter.Wait(ctx))
		})

		synctest.Wait()
		signaler2.Signal()
		done.Wait()

		// Signaling again is a no-op.
		signaler1.Signal()
	})
}

func TestNotifierConcurrentSignal(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	var notifier Notifier

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Multiple signalers of the same key can signal concurrently.
	for range 100 {
		signaler1, release1 := notifier.NewSignaler("foo")
		signaler2, release2 := notifier.NewSignaler("foo")
		waiter, release := notifier.NewWaiter("foo")

		var wg sync.WaitGroup
		wg.Go(signaler1.Signal)
		wg.Go(signaler2.Signal)
		wg.Wait()

		assert.NoError(waiter.Wait(ctx))
		release()
		release1()
		release2()
	}
	notifier.Lock()
	defer notifier.Unlock()
	assert.Empty(notifier.entries)
}

func TestNotifierReplaceSignaler(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		assert := assert.New(t)
		var notifier Notifier

		// The previous signaler has signaled and is still active (e.g. being closed).
		signaler1, release1 := notifier.NewSignaler("foo")
		signaler1.Signal()

		signaler2, release2 := notifier.NewSignaler("foo")
		defer release2()

		// Waiters must wait for the new signaler.
		waiter, release := notifier.NewWaiter("foo")
		defer release()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		var done sync.WaitGroup
		var notified atomic.Bool
		done.Go(func() {
			assert.NoError(waiter.Wait(ctx))
			notified.Store(true)
		})

		synctest.Wait()
		assert.False(notified.Load())

		// Releasing the previous signaler must not affect the new one.
		release1()
		synctest.Wait()
		assert.False(notified.Load())

		signaler2.Signal()
		done.Wait()
		assert.True(notified.Load())

		// Waiters created afterwards are notified immediately.
		waiter2, release3 := notifier.NewWaiter("foo")
		defer release3()
		assert.NoError(waiter2.Wait(ctx))
	})
}

func TestNotifierReleasedSignalerWithWaiter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		assert := assert.New(t)
		var notifier Notifier

		// The signaler signals and is released while a waiter still exists.
		signaler, releaseSignaler := notifier.NewSignaler("foo")
		waiter1, release1 := notifier.NewWaiter("foo")
		defer release1()
		signaler.Signal()
		releaseSignaler()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(waiter1.Wait(ctx))

		// New waiters must wait for a new signaler.
		waiter2, release2 := notifier.NewWaiter("foo")
		defer release2()

		var done sync.WaitGroup
		var notified atomic.Bool
		done.Go(func() {
			assert.NoError(waiter2.Wait(ctx))
			notified.Store(true)
		})

		synctest.Wait()
		assert.False(notified.Load())

		signaler2, releaseSignaler2 := notifier.NewSignaler("foo")
		defer releaseSignaler2()
		synctest.Wait()
		assert.False(notified.Load())

		signaler2.Signal()
		done.Wait()
		assert.True(notified.Load())
	})
}

func TestNotifierSignalerReleaseTwice(t *testing.T) {
	t.Parallel()

	var notifier Notifier
	_, release := notifier.NewSignaler("foo")
	release()
	release()
}

func TestNotifierWaiterReleaseTwice(t *testing.T) {
	t.Parallel()

	var notifier Notifier
	_, release := notifier.NewWaiter("foo")
	release()
	release()
}

func TestNotifierResetKeepsSignaler(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		assert := assert.New(t)
		var notifier Notifier

		signaler, releaseSignaler := notifier.NewSignaler("foo")
		signaler.Signal()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		waiter1, release1 := notifier.NewWaiter("foo")
		assert.NoError(waiter1.Wait(ctx))

		// After a reset (e.g. reconnect to the server), waiters must wait until the
		// existing signaler signals again.
		notifier.Reset()

		waiter2, release2 := notifier.NewWaiter("foo")
		var done sync.WaitGroup
		var notified atomic.Bool
		done.Go(func() {
			assert.NoError(waiter2.Wait(ctx))
			notified.Store(true)
		})

		synctest.Wait()
		assert.False(notified.Load())

		// Releasing waiters of the previous entry doesn't affect the new one.
		release1()
		synctest.Wait()
		assert.False(notified.Load())

		signaler.Signal()
		done.Wait()
		assert.True(notified.Load())

		release2()
		releaseSignaler()

		notifier.Lock()
		defer notifier.Unlock()
		assert.Empty(notifier.entries)
	})
}

func TestNotifierResetConcurrent(t *testing.T) {
	t.Parallel()

	var notifier Notifier
	var wg sync.WaitGroup
	for i := range 10 {
		// Signalers are re-armed by a reset and must signal again (e.g. once a
		// publisher connected again), so use a separate key for each goroutine.
		key := fmt.Sprintf("foo-%d", i)
		wg.Go(func() {
			for range 100 {
				signaler, release := notifier.NewSignaler(key)
				waiter, releaseWaiter := notifier.NewWaiter(key)
				signaler.Signal()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				// The waiter is notified by the signal or the reset.
				assert.NoError(t, waiter.Wait(ctx))
				cancel()
				releaseWaiter()
				release()
			}
		})
	}
	for range 100 {
		notifier.Reset()
	}
	wg.Wait()

	notifier.Lock()
	defer notifier.Unlock()
	assert.Empty(t, notifier.entries)
}
