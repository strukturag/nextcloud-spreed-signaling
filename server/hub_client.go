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
package server

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/client"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
)

var (
	InvalidFormat = client.InvalidFormat
)

func init() {
	RegisterClientStats()
}

type HubClient struct {
	client.Client

	hub     *Hub
	session atomic.Pointer[Session]
}

func NewHubClient(ctx context.Context, conn *websocket.Conn, remoteAddress string, agent string, hub *Hub) (*HubClient, error) {
	remoteAddress = strings.TrimSpace(remoteAddress)
	if remoteAddress == "" {
		remoteAddress = "unknown remote address"
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "unknown user agent"
	}

	client := &HubClient{
		hub: hub,
	}
	client.SetConn(ctx, conn, remoteAddress, agent, true, client)
	return client, nil
}

func (c *HubClient) OnLookupCountry(addr string) geoip.Country {
	return c.hub.LookupCountry(addr)
}

func (c *HubClient) OnClosed() {
	c.hub.processUnregister(c)
}

func (c *HubClient) OnMessageReceived(data []byte) {
	c.hub.processMessage(c, data)
}

func (c *HubClient) OnRTTReceived(rtt time.Duration) {
	// Ignore
}

func (c *HubClient) CloseSession() {
	if session := c.GetSession(); session != nil {
		session.Close()
	}
}

func (c *HubClient) IsAuthenticated() bool {
	return c.GetSession() != nil
}

func (c *HubClient) GetSession() Session {
	session := c.session.Load()
	if session == nil {
		return nil
	}

	return *session
}

func (c *HubClient) SetSession(session Session) {
	if session == nil {
		c.session.Store(nil)
	} else {
		c.session.Store(&session)
	}
}

func (c *HubClient) GetSessionId() api.PublicSessionId {
	session := c.GetSession()
	if session == nil {
		return ""
	}

	return session.PublicId()
}

func (c *HubClient) IsInRoom(id string) bool {
	session := c.GetSession()
	if session == nil {
		return false
	}

	return session.IsInRoom(id)
}
