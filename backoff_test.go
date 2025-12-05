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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackoff_Exponential(t *testing.T) {
	t.Parallel()
	SynctestTest(t, func(t *testing.T) {
		assert := assert.New(t)
		minWait := 100 * time.Millisecond
		backoff, err := NewExponentialBackoff(minWait, 500*time.Millisecond)
		require.NoError(t, err)

		waitTimes := []time.Duration{
			minWait,
			200 * time.Millisecond,
			400 * time.Millisecond,
			500 * time.Millisecond,
			500 * time.Millisecond,
		}

		for _, wait := range waitTimes {
			assert.Equal(wait, backoff.NextWait())
			a := time.Now()
			backoff.Wait(context.Background())
			b := time.Now()
			assert.Equal(b.Sub(a), wait)
		}

		backoff.Reset()
		a := time.Now()
		backoff.Wait(context.Background())
		b := time.Now()
		assert.Equal(b.Sub(a), minWait)
	})
}
