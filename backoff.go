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
	"context"
	"fmt"
	"time"
)

type Backoff interface {
	Reset()
	NextWait() time.Duration
	Wait(context.Context)
}

type exponentialBackoff struct {
	initial  time.Duration
	maxWait  time.Duration
	nextWait time.Duration

	getContextWithTimeout func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc)
}

func NewExponentialBackoff(initial time.Duration, maxWait time.Duration) (Backoff, error) {
	if initial <= 0 {
		return nil, fmt.Errorf("initial must be larger than 0")
	}
	if maxWait < initial {
		return nil, fmt.Errorf("maxWait must be larger or equal to initial")
	}

	return &exponentialBackoff{
		initial: initial,
		maxWait: maxWait,

		nextWait: initial,

		getContextWithTimeout: context.WithTimeout,
	}, nil
}

func (b *exponentialBackoff) Reset() {
	b.nextWait = b.initial
}

func (b *exponentialBackoff) NextWait() time.Duration {
	return b.nextWait
}

func (b *exponentialBackoff) Wait(ctx context.Context) {
	waiter, cancel := b.getContextWithTimeout(ctx, b.nextWait)
	defer cancel()

	b.nextWait = min(b.nextWait*2, b.maxWait)
	<-waiter.Done()
}
