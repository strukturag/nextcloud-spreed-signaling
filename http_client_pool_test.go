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
)

func TestHttpClientPool(t *testing.T) {
	if _, err := NewHttpClientPool(0, false); err == nil {
		t.Error("should not be possible to create empty pool")
	}

	pool, err := NewHttpClientPool(1, false)
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse("http://localhost/foo/bar")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, _, err := pool.Get(ctx, u); err != nil {
		t.Fatal(err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, _, err := pool.Get(ctx2, u); err == nil {
		t.Error("fetching from empty pool should have timed out")
	} else if err != context.DeadlineExceeded {
		t.Errorf("fetching from empty pool should have timed out, got %s", err)
	}

	// Pools are separated by hostname, so can get client for different host.
	u2, err := url.Parse("http://local.host/foo/bar")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := pool.Get(ctx, u2); err != nil {
		t.Fatal(err)
	}

	ctx3, cancel2 := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel2()
	if _, _, err := pool.Get(ctx3, u2); err == nil {
		t.Error("fetching from empty pool should have timed out")
	} else if err != context.DeadlineExceeded {
		t.Errorf("fetching from empty pool should have timed out, got %s", err)
	}
}
