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
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	ErrNotRedirecting = errors.New("not redirecting to different host")
)

func init() {
	RegisterHttpClientPoolStats()
}

type Pool struct {
	pool chan *http.Client

	currentConnections prometheus.Gauge
}

func (p *Pool) get(ctx context.Context) (client *http.Client, err error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case client := <-p.pool:
		p.currentConnections.Inc()
		return client, nil
	}
}

func (p *Pool) Put(c *http.Client) {
	p.currentConnections.Dec()
	p.pool <- c
}

func newPool(host string, constructor func() *http.Client, size int) (*Pool, error) {
	if size <= 0 {
		return nil, errors.New("can't create empty pool")
	}

	p := &Pool{
		pool:               make(chan *http.Client, size),
		currentConnections: connectionsPerHostCurrent.WithLabelValues(host),
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
	// +checklocks:mu
	clients map[string]*Pool

	maxConcurrentRequestsPerHost int // +checklocksignore: Only written to from constructor.
}

// idleConnTimeout limits how long an idle backend connection stays in the pool.
// It must stay below any realistic backend keep-alive timeout (Apache defaults to
// 300s, nginx to 75s) so that the client always closes an idle connection before the
// server does. See the comment in NewHttpClientPool.
const idleConnTimeout = 30 * time.Second

func NewHttpClientPool(maxConcurrentRequestsPerHost int, skipVerify bool) (*HttpClientPool, error) {
	if maxConcurrentRequestsPerHost <= 0 {
		return nil, errors.New("can't create empty pool")
	}

	tlsconfig := &tls.Config{
		InsecureSkipVerify: skipVerify,
	}
	transport := &http.Transport{
		MaxIdleConnsPerHost: maxConcurrentRequestsPerHost,
		TLSClientConfig:     tlsconfig,
		Proxy:               http.ProxyFromEnvironment,
		// Limit how long an idle connection may stay pooled.
		//
		// This is NOT the default transport, so IdleConnTimeout defaults to its
		// zero value, which means "no limit" - idle connections are kept forever.
		// Backend webservers, however, close idle keep-alive connections after
		// their own timeout (Apache's KeepAliveTimeout, nginx's
		// keepalive_timeout, and every load balancer in between). The pool
		// therefore hands out connections the server is closing, and a request
		// written into one fails with EOF. Go does not retry a POST once bytes
		// have been written - it is not replayable without an Idempotency-Key -
		// so the failure surfaces to the caller.
		//
		// In practice this shows up as backend requests intermittently failing
		// with EOF under otherwise normal conditions; on a room-join validation
		// it means the participant never actually joins the room while the
		// caller keeps ringing.
		//
		// Keeping this below any realistic backend keep-alive timeout makes the
		// CLIENT close idle connections first, so it can never pick up one that
		// the server is concurrently tearing down.
		IdleConnTimeout: idleConnTimeout,
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

	pool, err := newPool(url.Host, func() *http.Client {
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
