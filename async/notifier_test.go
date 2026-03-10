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
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifierNoWaiter(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	// Notifications can be sent even if no waiter exists.
	notifier.Notify("foo")
}

func TestNotifierWaitTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var notifier Notifier

		notified := make(chan struct{})
		go func() {
			defer close(notified)
			time.Sleep(time.Second)
			notifier.Notify("foo")
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

	notifier.Notify("foo")
	wg.Wait()
}

func TestNotifierMultiNotify(t *testing.T) {
	t.Parallel()
	var notifier Notifier

	_, release := notifier.NewWaiter("foo")
	defer release()

	notifier.Notify("foo")
	// The second notification will be ignored while the first is still pending.
	notifier.Notify("foo")
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

		notifier.Notify("foo")
		done.Wait()
	})
}
