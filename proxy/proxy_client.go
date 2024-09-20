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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

type ProxyClient struct {
	signaling.Client

	proxy *ProxyServer

	session atomic.Pointer[ProxySession]
}

func NewProxyClient(log *zap.Logger, proxy *ProxyServer, conn *websocket.Conn, addr string) (*ProxyClient, error) {
	client := &ProxyClient{
		proxy: proxy,
	}
	client.SetConn(log, conn, addr, client)
	return client, nil
}

func (c *ProxyClient) GetSession() *ProxySession {
	return c.session.Load()
}

func (c *ProxyClient) SetSession(session *ProxySession) {
	c.session.Store(session)
}

func (c *ProxyClient) OnClosed(client signaling.HandlerClient) {
	if session := c.GetSession(); session != nil {
		session.MarkUsed()
	}
	c.proxy.clientClosed(&c.Client)
}

func (c *ProxyClient) OnMessageReceived(client signaling.HandlerClient, data []byte) {
	c.proxy.processMessage(c, data)
}

func (c *ProxyClient) OnRTTReceived(client signaling.HandlerClient, rtt time.Duration) {
	if session := c.GetSession(); session != nil {
		session.MarkUsed()
	}
}
