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
package pool

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

func TestHttpClientPoolIdleConnTimeout(t *testing.T) {
	// A pooled idle connection must be closed by the CLIENT before the backend
	// webserver closes it. Otherwise the pool eventually hands out a connection
	// the server is tearing down, the request written into it fails with EOF,
	// and Go will not retry a POST (not replayable without an Idempotency-Key),
	// so the backend request is silently lost.
	//
	// This is easy to reintroduce: http.Transport's zero value for
	// IdleConnTimeout means "no limit" (unlike http.DefaultTransport, which sets
	// 90s), so simply dropping the field restores the bug while the code still
	// looks correct.
	assert := assert.New(t)
	pool, err := NewHttpClientPool(1, false)
	require.NoError(t, err)

	assert.NotZero(pool.transport.IdleConnTimeout,
		"IdleConnTimeout must be set: the zero value means idle connections are "+
			"pooled forever, so the backend webserver will close them first and "+
			"requests will intermittently fail with EOF")

	// Apache's default KeepAliveTimeout is 5s and nginx's keepalive_timeout is
	// 75s; staying well below those keeps the client closing first.
	assert.Less(pool.transport.IdleConnTimeout, 75*time.Second,
		"IdleConnTimeout must stay below a realistic backend keep-alive timeout")
}
