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
	"net/http"
	"testing"
	"time"
)

func TestHttpClientPool(t *testing.T) {
	transport := &http.Transport{}
	if _, err := NewHttpClientPool(func() *http.Client {
		return &http.Client{
			Transport: transport,
		}
	}, 0); err == nil {
		t.Error("should not be possible to create empty pool")
	}

	pool, err := NewHttpClientPool(func() *http.Client {
		return &http.Client{
			Transport: transport,
		}
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := pool.Get(ctx); err != nil {
		t.Fatal(err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := pool.Get(ctx2); err == nil {
		t.Error("fetching from empty pool should have timed out")
	} else if err != context.DeadlineExceeded {
		t.Errorf("fetching from empty pool should have timed out, got %s", err)
	}
}
