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
	"fmt"
	"net/http"
)

type HttpClientPool struct {
	pool chan *http.Client
}

func NewHttpClientPool(constructor func() *http.Client, size int) (*HttpClientPool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("can't create empty pool")
	}

	p := &HttpClientPool{
		pool: make(chan *http.Client, size),
	}
	for size > 0 {
		c := constructor()
		p.pool <- c
		size -= 1
	}
	return p, nil
}

func (p *HttpClientPool) Get(ctx context.Context) (client *http.Client, err error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case client := <-p.pool:
		return client, nil
	}
}

func (p *HttpClientPool) Put(c *http.Client) {
	p.pool <- c
}
