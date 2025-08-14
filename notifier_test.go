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
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifierNoWaiter(t *testing.T) {
	var notifier Notifier

	// Notifications can be sent even if no waiter exists.
	notifier.Notify("foo")
}

func TestNotifierSimple(t *testing.T) {
	var notifier Notifier

	var wg sync.WaitGroup
	wg.Add(1)

	waiter := notifier.NewWaiter("foo")
	defer notifier.Release(waiter)

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, waiter.Wait(ctx))
	}()

	notifier.Notify("foo")
	wg.Wait()
}

func TestNotifierMultiNotify(t *testing.T) {
	var notifier Notifier

	waiter := notifier.NewWaiter("foo")
	defer notifier.Release(waiter)

	notifier.Notify("foo")
	// The second notification will be ignored while the first is still pending.
	notifier.Notify("foo")
}

func TestNotifierWaitClosed(t *testing.T) {
	var notifier Notifier

	waiter := notifier.NewWaiter("foo")
	notifier.Release(waiter)

	assert.NoError(t, waiter.Wait(context.Background()))
}

func TestNotifierWaitClosedMulti(t *testing.T) {
	var notifier Notifier

	waiter1 := notifier.NewWaiter("foo")
	waiter2 := notifier.NewWaiter("foo")
	notifier.Release(waiter1)
	notifier.Release(waiter2)

	assert.NoError(t, waiter1.Wait(context.Background()))
	assert.NoError(t, waiter2.Wait(context.Background()))
}

func TestNotifierResetWillNotify(t *testing.T) {
	var notifier Notifier

	var wg sync.WaitGroup
	wg.Add(1)

	waiter := notifier.NewWaiter("foo")
	defer notifier.Release(waiter)

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, waiter.Wait(ctx))
	}()

	notifier.Reset()
	wg.Wait()
}

func TestNotifierDuplicate(t *testing.T) {
	t.Parallel()
	var notifier Notifier
	var wgStart sync.WaitGroup
	var wgEnd sync.WaitGroup

	for range 2 {
		wgStart.Add(1)
		wgEnd.Add(1)

		go func() {
			defer wgEnd.Done()
			waiter := notifier.NewWaiter("foo")
			defer notifier.Release(waiter)

			// Goroutine has created the waiter and is ready.
			wgStart.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			assert.NoError(t, waiter.Wait(ctx))
		}()
	}

	wgStart.Wait()

	time.Sleep(100 * time.Millisecond)
	notifier.Notify("foo")
	wgEnd.Wait()
}
