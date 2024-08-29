/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHttpClientPool(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	_, err := NewHttpClientPool(0, false)
	assert.Error(err)

	pool, err := NewHttpClientPool(1, false)
	require.NoError(err)

	u, err := url.Parse("http://localhost/foo/bar")
	require.NoError(err)

	ctx := context.Background()
	_, _, err = pool.Get(ctx, u)
	require.NoError(err)

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, _, err = pool.Get(ctx2, u)
	assert.ErrorIs(err, context.DeadlineExceeded)

	// Pools are separated by hostname, so can get client for different host.
	u2, err := url.Parse("http://local.host/foo/bar")
	require.NoError(err)

	_, _, err = pool.Get(ctx, u2)
	require.NoError(err)

	ctx3, cancel2 := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel2()
	_, _, err = pool.Get(ctx3, u2)
	assert.ErrorIs(err, context.DeadlineExceeded)
}
