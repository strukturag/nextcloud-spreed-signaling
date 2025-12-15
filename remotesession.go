/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type RemoteSession struct {
	logger       log.Logger
	hub          *Hub
	client       *Client
	remoteClient *GrpcClient
	sessionId    api.PublicSessionId

	proxy atomic.Pointer[SessionProxy]
}

func NewRemoteSession(hub *Hub, client *Client, remoteClient *GrpcClient, sessionId api.PublicSessionId) (*RemoteSession, error) {
	remoteSession := &RemoteSession{
		logger:       hub.logger,
		hub:          hub,
		client:       client,
		remoteClient: remoteClient,
		sessionId:    sessionId,
	}

	client.SetSessionId(sessionId)
	client.SetHandler(remoteSession)

	// Don't use "client.Context()" here as it could close the proxy connection
	// before any final messages are forwarded to the remote end.
	proxy, err := remoteClient.ProxySession(context.Background(), sessionId, remoteSession)
	if err != nil {
		return nil, err
	}
	remoteSession.proxy.Store(proxy)

	return remoteSession, nil
}

func (s *RemoteSession) Country() geoip.Country {
	return s.client.Country()
}

func (s *RemoteSession) RemoteAddr() string {
	return s.client.RemoteAddr()
}

func (s *RemoteSession) UserAgent() string {
	return s.client.UserAgent()
}

func (s *RemoteSession) IsConnected() bool {
	return true
}

func (s *RemoteSession) Start(message *api.ClientMessage) error {
	return s.sendMessage(message)
}

func (s *RemoteSession) OnProxyMessage(msg *ServerSessionMessage) error {
	var message *api.ServerMessage
	if err := json.Unmarshal(msg.Message, &message); err != nil {
		return err
	}

	if !s.client.SendMessage(message) {
		return errors.New("could not send message to client")
	}

	return nil
}

func (s *RemoteSession) OnProxyClose(err error) {
	if err != nil {
		s.logger.Printf("Proxy connection for session %s to %s was closed with error: %s", s.sessionId, s.remoteClient.Target(), err)
	}
	s.Close()
}

func (s *RemoteSession) SendMessage(message WritableClientMessage) bool {
	return s.sendMessage(message) == nil
}

func (s *RemoteSession) sendProxyMessage(message []byte) error {
	proxy := s.proxy.Load()
	if proxy == nil {
		return errors.New("proxy already closed")
	}

	msg := &ClientSessionMessage{
		Message: message,
	}
	return proxy.Send(msg)
}

func (s *RemoteSession) sendMessage(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return s.sendProxyMessage(data)
}

func (s *RemoteSession) Close() {
	if proxy := s.proxy.Swap(nil); proxy != nil {
		proxy.Close()
	}
	s.hub.unregisterRemoteSession(s)
	s.client.Close()
}

func (s *RemoteSession) OnLookupCountry(client HandlerClient) geoip.Country {
	return s.hub.OnLookupCountry(client)
}

func (s *RemoteSession) OnClosed(client HandlerClient) {
	s.Close()
}

func (s *RemoteSession) OnMessageReceived(client HandlerClient, message []byte) {
	if err := s.sendProxyMessage(message); err != nil {
		s.logger.Printf("Error sending %s to the proxy for session %s: %s", string(message), s.sessionId, err)
		s.Close()
	}
}

func (s *RemoteSession) OnRTTReceived(client HandlerClient, rtt time.Duration) {
}
