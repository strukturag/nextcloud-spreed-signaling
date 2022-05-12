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
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
)

type Pool struct {
	pool chan *http.Client
}

func (p *Pool) get(ctx context.Context) (client *http.Client, err error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case client := <-p.pool:
		return client, nil
	}
}

func (p *Pool) Put(c *http.Client) {
	p.pool <- c
}

func newPool(constructor func() *http.Client, size int) (*Pool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("can't create empty pool")
	}

	p := &Pool{
		pool: make(chan *http.Client, size),
	}
	for size > 0 {
		c := constructor()
		p.pool <- c
		size--
	}
	return p, nil
}

type HttpClientPool struct {
	mu sync.Mutex

	transport *http.Transport
	clients   map[string]*Pool

	maxConcurrentRequestsPerHost int
}

func NewHttpClientPool(maxConcurrentRequestsPerHost int, skipVerify bool) (*HttpClientPool, error) {
	if maxConcurrentRequestsPerHost <= 0 {
		return nil, fmt.Errorf("can't create empty pool")
	}

	tlsconfig := &tls.Config{
		InsecureSkipVerify: skipVerify,
	}
	transport := &http.Transport{
		MaxIdleConnsPerHost: maxConcurrentRequestsPerHost,
		TLSClientConfig:     tlsconfig,
	}

	result := &HttpClientPool{
		transport: transport,
		clients:   make(map[string]*Pool),

		maxConcurrentRequestsPerHost: maxConcurrentRequestsPerHost,
	}
	return result, nil
}

func (p *HttpClientPool) getPool(url *url.URL) (*Pool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pool, found := p.clients[url.Host]; found {
		return pool, nil
	}

	pool, err := newPool(func() *http.Client {
		return &http.Client{
			Transport: p.transport,
			// Only send body in redirect if going to same scheme / host.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				} else if len(via) > 0 {
					viaReq := via[len(via)-1]
					if req.URL.Scheme != viaReq.URL.Scheme || req.URL.Host != viaReq.URL.Host {
						return ErrNotRedirecting
					}
				}
				return nil
			},
		}
	}, p.maxConcurrentRequestsPerHost)
	if err != nil {
		return nil, err
	}

	p.clients[url.Host] = pool
	return pool, nil
}

func (p *HttpClientPool) Get(ctx context.Context, url *url.URL) (*http.Client, *Pool, error) {
	pool, err := p.getPool(url)
	if err != nil {
		return nil, nil, err
	}

	client, err := pool.get(ctx)
	if err != nil {
		return nil, nil, err
	}

	return client, pool, err
}
