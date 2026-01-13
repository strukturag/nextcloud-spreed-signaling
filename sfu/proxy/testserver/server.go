/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package testserver

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	etcdtest "github.com/strukturag/nextcloud-spreed-signaling/etcd/test"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/proxy"
)

const (
	TimeoutTestTimeout = 100 * time.Millisecond
)

type ProxyTestServer interface {
	URL() string
	SetServers(servers []ProxyTestServer)
	SetToken(tokenId string, key *rsa.PublicKey)

	getToken(tokenId string) (*rsa.PublicKey, bool)
	getPublisher(id api.PublicSessionId) *testProxyServerPublisher
}

type ProxyTestOptions struct {
	Etcd    *etcdtest.EtcdServer
	Servers []ProxyTestServer
}

type proxyServerClientHandler func(msg *proxy.ClientMessage) (*proxy.ServerMessage, error)

type testProxyServerPublisher struct {
	id api.PublicSessionId
}

type testProxyServerSubscriber struct {
	id  string
	sid string
	pub *testProxyServerPublisher

	remoteUrl string
}

type TestProxyClient interface {
	Close()
	SendMessage(msg *proxy.ServerMessage)
}

type testProxyServerClient struct {
	t *testing.T

	server *TestProxyServerHandler
	// +checklocks:mu
	ws             *websocket.Conn
	processMessage proxyServerClientHandler

	mu        sync.Mutex
	sessionId api.PublicSessionId
}

func (c *testProxyServerClient) processHello(msg *proxy.ClientMessage) (*proxy.ServerMessage, error) {
	if msg.Type != "hello" {
		return nil, fmt.Errorf("expected hello, got %+v", msg)
	}

	if msg.Hello.ResumeId != "" {
		client := c.server.getClient(msg.Hello.ResumeId)
		if client == nil {
			response := &proxy.ServerMessage{
				Id:   msg.Id,
				Type: "error",
				Error: &api.Error{
					Code: "no_such_session",
				},
			}
			return response, nil
		}

		c.sessionId = msg.Hello.ResumeId
		c.server.setClient(c.sessionId, c)
		response := &proxy.ServerMessage{
			Id:   msg.Id,
			Type: "hello",
			Hello: &proxy.HelloServerMessage{
				Version:   "1.0",
				SessionId: c.sessionId,
				Server: &api.WelcomeServerMessage{
					Version: "1.0",
					Country: c.server.country,
				},
			},
		}
		c.processMessage = c.processRegularMessage
		return response, nil
	}

	token, err := jwt.ParseWithClaims(msg.Hello.Token, &proxy.TokenClaims{}, func(token *jwt.Token) (any, error) {
		claims, ok := token.Claims.(*proxy.TokenClaims)
		if !assert.True(c.t, ok, "unsupported claims type: %+v", token.Claims) {
			return nil, errors.New("unsupported claims type")
		}

		key, found := c.server.Tokens[claims.Issuer]
		if !assert.True(c.t, found) {
			return nil, errors.New("no key found for issuer")
		}

		return key, nil
	})
	if assert.NoError(c.t, err) {
		if assert.True(c.t, token.Valid) {
			_, ok := token.Claims.(*proxy.TokenClaims)
			assert.True(c.t, ok)
		}
	}

	response := &proxy.ServerMessage{
		Id:   msg.Id,
		Type: "hello",
		Hello: &proxy.HelloServerMessage{
			Version:   "1.0",
			SessionId: c.sessionId,
			Server: &api.WelcomeServerMessage{
				Version: "1.0",
				Country: c.server.country,
			},
		},
	}
	c.processMessage = c.processRegularMessage
	return response, nil
}

func (c *testProxyServerClient) processRegularMessage(msg *proxy.ClientMessage) (*proxy.ServerMessage, error) {
	var handler proxyServerClientHandler
	switch msg.Type {
	case "command":
		handler = c.processCommandMessage
	}

	if handler == nil {
		response := msg.NewWrappedErrorServerMessage(fmt.Errorf("type \"%s\" is not implemented", msg.Type))
		return response, nil
	}

	return handler(msg)
}

func (c *testProxyServerClient) processCommandMessage(msg *proxy.ClientMessage) (*proxy.ServerMessage, error) {
	var response *proxy.ServerMessage
	switch msg.Command.Type {
	case "create-publisher":
		if strings.Contains(c.t.Name(), "ProxyPublisherTimeout") {
			time.Sleep(2 * TimeoutTestTimeout)
			defer c.server.Wakeup()
		}
		pub := c.server.createPublisher()

		if assert.NotNil(c.t, msg.Command.PublisherSettings) {
			if assert.NotEqualValues(c.t, 0, msg.Command.PublisherSettings.Bitrate) {
				assert.Equal(c.t, msg.Command.Bitrate, msg.Command.PublisherSettings.Bitrate) // nolint
			}
			assert.Equal(c.t, msg.Command.MediaTypes, msg.Command.PublisherSettings.MediaTypes) // nolint
			if strings.Contains(c.t.Name(), "Codecs") {
				assert.Equal(c.t, "opus,g722", msg.Command.PublisherSettings.AudioCodec)
				assert.Equal(c.t, "vp9,vp8,av1", msg.Command.PublisherSettings.VideoCodec)
			} else {
				assert.Empty(c.t, msg.Command.PublisherSettings.AudioCodec)
				assert.Empty(c.t, msg.Command.PublisherSettings.VideoCodec)
			}
		}

		response = &proxy.ServerMessage{
			Id:   msg.Id,
			Type: "command",
			Command: &proxy.CommandServerMessage{
				Id:      string(pub.id),
				Bitrate: msg.Command.Bitrate, // nolint
			},
		}
		c.server.updateLoad(1)
	case "delete-publisher":
		if strings.Contains(c.t.Name(), "ProxyPublisherTimeout") {
			defer c.server.Wakeup()
		}

		if pub, found := c.server.deletePublisher(api.PublicSessionId(msg.Command.ClientId)); !found {
			response = msg.NewWrappedErrorServerMessage(fmt.Errorf("publisher %s not found", msg.Command.ClientId))
		} else {
			response = &proxy.ServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &proxy.CommandServerMessage{
					Id: string(pub.id),
				},
			}
			c.server.updateLoad(-1)
		}
	case "create-subscriber":
		var pub *testProxyServerPublisher
		if msg.Command.RemoteUrl != "" {
			for _, server := range c.server.Servers {
				if server.URL() != msg.Command.RemoteUrl {
					continue
				}

				token, err := jwt.ParseWithClaims(msg.Command.RemoteToken, &proxy.TokenClaims{}, func(token *jwt.Token) (any, error) {
					claims, ok := token.Claims.(*proxy.TokenClaims)
					if !assert.True(c.t, ok, "unsupported claims type: %+v", token.Claims) {
						return nil, errors.New("unsupported claims type")
					}

					key, found := server.getToken(claims.Issuer)
					if !assert.True(c.t, found) {
						return nil, errors.New("no key found for issuer")
					}

					return key, nil
				})
				if assert.NoError(c.t, err) {
					if claims, ok := token.Claims.(*proxy.TokenClaims); assert.True(c.t, token.Valid) && assert.True(c.t, ok) {
						assert.EqualValues(c.t, msg.Command.PublisherId, claims.Subject)
					}
				}

				pub = server.getPublisher(msg.Command.PublisherId)
				break
			}
		} else {
			pub = c.server.getPublisher(msg.Command.PublisherId)
		}

		if pub == nil {
			response = msg.NewWrappedErrorServerMessage(fmt.Errorf("publisher %s not found", msg.Command.PublisherId))
		} else {
			if strings.Contains(c.t.Name(), "ProxySubscriberTimeout") {
				time.Sleep(2 * TimeoutTestTimeout)
				defer c.server.Wakeup()
			}
			sub := c.server.createSubscriber(pub)
			response = &proxy.ServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &proxy.CommandServerMessage{
					Id:  sub.id,
					Sid: sub.sid,
				},
			}
			c.server.updateLoad(1)
		}
	case "delete-subscriber":
		if strings.Contains(c.t.Name(), "ProxySubscriberTimeout") {
			defer c.server.Wakeup()
		}
		if sub, found := c.server.deleteSubscriber(msg.Command.ClientId); !found {
			response = msg.NewWrappedErrorServerMessage(fmt.Errorf("subscriber %s not found", msg.Command.ClientId))
		} else {
			if msg.Command.RemoteUrl != sub.remoteUrl {
				response = msg.NewWrappedErrorServerMessage(fmt.Errorf("remote subscriber %s not found", msg.Command.ClientId))
				return response, nil
			}

			response = &proxy.ServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &proxy.CommandServerMessage{
					Id: sub.id,
				},
			}
			c.server.updateLoad(-1)
		}
	}
	if response == nil {
		response = msg.NewWrappedErrorServerMessage(fmt.Errorf("command \"%s\" is not implemented", msg.Command.Type))
	}

	return response, nil
}

func (c *testProxyServerClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ws != nil {
		c.ws.Close()
		c.ws = nil
	}
}

func (c *testProxyServerClient) handleSendMessageError(fmt string, msg *proxy.ServerMessage, err error) {
	c.t.Helper()

	if !errors.Is(err, websocket.ErrCloseSent) || msg.Type != "event" || msg.Event.Type != "update-load" {
		assert.Fail(c.t, "error while sending message", fmt, msg, err)
	}
}

func (c *testProxyServerClient) SendMessage(msg *proxy.ServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ws == nil {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		c.handleSendMessageError("error marshalling %+v: %s", msg, err)
		return
	}

	w, err := c.ws.NextWriter(websocket.TextMessage)
	if err != nil {
		c.handleSendMessageError("error creating writer for %+v: %s", msg, err)
		return
	}

	if _, err := w.Write(data); err != nil {
		c.handleSendMessageError("error sending %+v: %s", msg, err)
		return
	}

	if err := w.Close(); err != nil {
		c.handleSendMessageError("error during close of sending %+v: %s", msg, err)
	}

	if msg.CloseAfterSend(nil) {
		c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // nolint
		c.ws.Close()
	}
}

func (c *testProxyServerClient) run() {
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		c.server.expireSession(30*time.Second, c)
		c.ws = nil
	}()
	c.processMessage = c.processHello
	assert := assert.New(c.t)
	for {
		c.mu.Lock()
		ws := c.ws
		c.mu.Unlock()
		if ws == nil {
			break
		}

		msgType, reader, err := ws.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				assert.NoError(err)
			}
			return
		}

		body, err := io.ReadAll(reader)
		if !assert.NoError(err) {
			continue
		}

		if !assert.Equal(websocket.TextMessage, msgType, "unexpected message type for %s", string(body)) {
			continue
		}

		var msg proxy.ClientMessage
		if err := json.Unmarshal(body, &msg); !assert.NoError(err, "could not decode message %s", string(body)) {
			continue
		}

		if err := msg.CheckValid(); !assert.NoError(err, "invalid message %s", string(body)) {
			continue
		}

		response, err := c.processMessage(&msg)
		if !assert.NoError(err) {
			continue
		}

		c.SendMessage(response)
		if response.Type == "hello" {
			c.server.sendLoad(c)
		}
	}
}

type TestProxyServerHandler struct {
	t *testing.T

	url      string
	server   *httptest.Server
	Servers  []ProxyTestServer
	Tokens   map[string]*rsa.PublicKey
	upgrader *websocket.Upgrader
	country  geoip.Country

	mu       sync.Mutex
	load     atomic.Uint64
	incoming atomic.Pointer[float64]
	outgoing atomic.Pointer[float64]
	// +checklocks:mu
	clients map[api.PublicSessionId]*testProxyServerClient
	// +checklocks:mu
	publishers map[api.PublicSessionId]*testProxyServerPublisher
	// +checklocks:mu
	subscribers map[string]*testProxyServerSubscriber

	wakeupChan chan struct{}
}

func (h *TestProxyServerHandler) Start() {
	h.server.Start()
	h.url = h.server.URL
}

func (h *TestProxyServerHandler) URL() string {
	return h.url
}

func (h *TestProxyServerHandler) SetURL(url string) {
	h.url = url
}

func (h *TestProxyServerHandler) Listener() net.Listener {
	return h.server.Listener
}

func (h *TestProxyServerHandler) SetListener(listener net.Listener) {
	h.server.Listener = listener
}

func (h *TestProxyServerHandler) SetServers(servers []ProxyTestServer) {
	h.Servers = servers
}

func (h *TestProxyServerHandler) SetToken(tokenId string, key *rsa.PublicKey) {
	h.Tokens[tokenId] = key
}

func (h *TestProxyServerHandler) getToken(tokenId string) (key *rsa.PublicKey, found bool) {
	key, found = h.Tokens[tokenId]
	return
}

func (h *TestProxyServerHandler) createPublisher() *testProxyServerPublisher {
	h.mu.Lock()
	defer h.mu.Unlock()
	pub := &testProxyServerPublisher{
		id: api.PublicSessionId(internal.RandomString(32)),
	}

	for {
		if _, found := h.publishers[pub.id]; !found {
			break
		}

		pub.id = api.PublicSessionId(internal.RandomString(32))
	}
	h.publishers[pub.id] = pub
	return pub
}

func (h *TestProxyServerHandler) getPublisher(id api.PublicSessionId) *testProxyServerPublisher {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.publishers[id]
}

func (h *TestProxyServerHandler) deletePublisher(id api.PublicSessionId) (*testProxyServerPublisher, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	pub, found := h.publishers[id]
	if !found {
		return nil, false
	}

	delete(h.publishers, id)
	return pub, true
}

func (h *TestProxyServerHandler) createSubscriber(pub *testProxyServerPublisher) *testProxyServerSubscriber {
	h.mu.Lock()
	defer h.mu.Unlock()

	sub := &testProxyServerSubscriber{
		id:  internal.RandomString(32),
		sid: internal.RandomString(8),
		pub: pub,
	}

	for {
		if _, found := h.subscribers[sub.id]; !found {
			break
		}

		sub.id = internal.RandomString(32)
	}
	h.subscribers[sub.id] = sub
	return sub
}

func (h *TestProxyServerHandler) deleteSubscriber(id string) (*testProxyServerSubscriber, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sub, found := h.subscribers[id]
	if !found {
		return nil, false
	}

	delete(h.subscribers, id)
	return sub, true
}

func (h *TestProxyServerHandler) UpdateBandwidth(incoming float64, outgoing float64) {
	h.incoming.Store(&incoming)
	h.outgoing.Store(&outgoing)

	h.mu.Lock()
	defer h.mu.Unlock()

	msg := h.getLoadMessage(h.load.Load())
	for _, c := range h.clients {
		c.SendMessage(msg)
	}
}

func (h *TestProxyServerHandler) Clear(incoming bool, outgoing bool) {
	if incoming {
		h.incoming.Store(nil)
	}
	if outgoing {
		h.outgoing.Store(nil)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	msg := h.getLoadMessage(h.load.Load())
	for _, c := range h.clients {
		c.SendMessage(msg)
	}
}

func (h *TestProxyServerHandler) getLoadMessage(load uint64) *proxy.ServerMessage {
	msg := &proxy.ServerMessage{
		Type: "event",
		Event: &proxy.EventServerMessage{
			Type: "update-load",
			Load: load,
		},
	}

	incoming := h.incoming.Load()
	outgoing := h.outgoing.Load()
	if incoming != nil || outgoing != nil {
		msg.Event.Bandwidth = &proxy.EventServerBandwidth{
			Incoming: incoming,
			Outgoing: outgoing,
		}
	}
	return msg
}

func (h *TestProxyServerHandler) updateLoad(delta int64) {
	if delta == 0 {
		return
	}

	var load uint64
	if delta > 0 {
		load = h.load.Add(uint64(delta))
	} else {
		load = h.load.Add(^uint64(delta - 1))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	msg := h.getLoadMessage(load)
	for _, c := range h.clients {
		c.SendMessage(msg)
	}
}

func (h *TestProxyServerHandler) sendLoad(c *testProxyServerClient) {
	msg := h.getLoadMessage(h.load.Load())
	c.SendMessage(msg)
}

func (h *TestProxyServerHandler) expireSession(timeout time.Duration, client *testProxyServerClient) {
	timer := time.AfterFunc(timeout, func() {
		h.removeClient(client)
	})

	h.t.Cleanup(func() {
		timer.Stop()
	})
}

func (h *TestProxyServerHandler) removeClient(client *testProxyServerClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, client.sessionId)
}

func (h *TestProxyServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if !assert.NoError(h.t, err) {
		return
	}

	client := &testProxyServerClient{
		t:         h.t,
		server:    h,
		ws:        ws,
		sessionId: api.PublicSessionId(internal.RandomString(32)),
	}

	h.setClient(client.sessionId, client)

	go client.run()
}

func (h *TestProxyServerHandler) getClient(sessionId api.PublicSessionId) *testProxyServerClient {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.clients[sessionId]
}

func (h *TestProxyServerHandler) setClient(sessionId api.PublicSessionId, client *testProxyServerClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if prev, found := h.clients[sessionId]; found {
		prev.SendMessage(&proxy.ServerMessage{
			Type: "bye",
			Bye: &proxy.ByeServerMessage{
				Reason: "session_resumed",
			},
		})
		prev.Close()
	}

	h.clients[sessionId] = client
}

func (h *TestProxyServerHandler) Wakeup() {
	h.wakeupChan <- struct{}{}
}

func (h *TestProxyServerHandler) WaitForWakeup(ctx context.Context) error {
	select {
	case <-h.wakeupChan:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *TestProxyServerHandler) GetSingleClient() TestProxyClient {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, c := range h.clients {
		return c
	}

	return nil
}

func (h *TestProxyServerHandler) ClearClients() {
	h.mu.Lock()
	defer h.mu.Unlock()

	clear(h.clients)
}

func NewProxyServerForTest(t *testing.T, country geoip.Country) *TestProxyServerHandler {
	t.Helper()

	upgrader := websocket.Upgrader{}
	proxyHandler := &TestProxyServerHandler{
		t:           t,
		Tokens:      make(map[string]*rsa.PublicKey),
		upgrader:    &upgrader,
		country:     country,
		clients:     make(map[api.PublicSessionId]*testProxyServerClient),
		publishers:  make(map[api.PublicSessionId]*testProxyServerPublisher),
		subscribers: make(map[string]*testProxyServerSubscriber),
		wakeupChan:  make(chan struct{}),
	}
	server := httptest.NewUnstartedServer(proxyHandler)
	if !strings.Contains(t.Name(), "DnsDiscovery") {
		server.Start()
	}
	proxyHandler.server = server
	proxyHandler.url = server.URL
	t.Cleanup(func() {
		server.Close()
		proxyHandler.mu.Lock()
		defer proxyHandler.mu.Unlock()
		for _, c := range proxyHandler.clients {
			c.Close()
		}
	})

	return proxyHandler
}
