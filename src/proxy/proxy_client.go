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
	"unsafe"

	"github.com/gorilla/websocket"

	"signaling"
)

type ProxyClient struct {
	signaling.Client

	proxy *ProxyServer

	session unsafe.Pointer
}

func NewProxyClient(proxy *ProxyServer, conn *websocket.Conn, addr string) (*ProxyClient, error) {
	client := &ProxyClient{
		proxy: proxy,
	}
	client.SetConn(conn, addr)
	return client, nil
}

func (c *ProxyClient) GetSession() *ProxySession {
	return (*ProxySession)(atomic.LoadPointer(&c.session))
}

func (c *ProxyClient) SetSession(session *ProxySession) {
	atomic.StorePointer(&c.session, unsafe.Pointer(session))
}
