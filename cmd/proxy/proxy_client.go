/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/client"
)

type ProxyClient struct {
	client.Client

	proxy *ProxyServer

	session atomic.Pointer[ProxySession]
}

func NewProxyClient(ctx context.Context, proxy *ProxyServer, conn *websocket.Conn, addr string, agent string) (*ProxyClient, error) {
	client := &ProxyClient{
		proxy: proxy,
	}
	client.SetConn(ctx, conn, addr, agent, false, client)
	return client, nil
}

func (c *ProxyClient) GetSessionId() api.PublicSessionId {
	if session := c.GetSession(); session != nil {
		return session.PublicId()
	}

	return ""
}

func (c *ProxyClient) GetSession() *ProxySession {
	return c.session.Load()
}

func (c *ProxyClient) SetSession(session *ProxySession) {
	c.session.Store(session)
}

func (c *ProxyClient) OnClosed() {
	if session := c.session.Swap(nil); session != nil {
		session.MarkUsed()
	}
	c.proxy.clientClosed(c)
}

func (c *ProxyClient) OnMessageReceived(data []byte) {
	c.proxy.processMessage(c, data)
}

func (c *ProxyClient) OnRTTReceived(rtt time.Duration) {
	if session := c.GetSession(); session != nil {
		session.MarkUsed()
	}
}
