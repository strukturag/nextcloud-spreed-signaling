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
		if err := waiter.Wait(ctx); err != nil {
			t.Error(err)
		}
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

	if err := waiter.Wait(context.Background()); err != nil {
		t.Error(err)
	}
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
		if err := waiter.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()

	notifier.Reset()
	wg.Wait()
}

func TestNotifierDuplicate(t *testing.T) {
	var notifier Notifier

	waiter := notifier.NewWaiter("foo")
	defer notifier.Release(waiter)

	defer func() {
		if e := recover(); e != nil {
			if e.(string) != "already waiting" {
				t.Errorf("Expected error about already waiting, got %+v", e)
			}
		}
	}()

	// Creating a waiter for an existing key will panic.
	notifier.NewWaiter("foo")
}
