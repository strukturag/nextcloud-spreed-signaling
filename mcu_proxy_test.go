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
package signaling

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"
)

func TestMcuProxyStats(t *testing.T) {
	collectAndLint(t, proxyMcuStats...)
}

func newProxyConnectionWithCountry(country string) *mcuProxyConnection {
	conn := &mcuProxyConnection{}
	conn.country.Store(country)
	return conn
}

func Test_sortConnectionsForCountry(t *testing.T) {
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[string][][]*mcuProxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
	}

	for country, test := range testcases {
		country := country
		test := test
		t.Run(country, func(t *testing.T) {
			sorted := sortConnectionsForCountry(test[0], country, nil)
			for idx, conn := range sorted {
				assert.Equal(t, test[1][idx], conn, "Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
			}
		})
	}
}

func Test_sortConnectionsForCountryWithOverride(t *testing.T) {
	conn_de := newProxyConnectionWithCountry("DE")
	conn_at := newProxyConnectionWithCountry("AT")
	conn_jp := newProxyConnectionWithCountry("JP")
	conn_us := newProxyConnectionWithCountry("US")

	testcases := map[string][][]*mcuProxyConnection{
		// Direct country match
		"DE": {
			{conn_at, conn_jp, conn_de},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"AT": {
			{conn_at, conn_jp, conn_de},
			{conn_at, conn_de, conn_jp},
		},
		// Continent match
		"CH": {
			{conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_jp},
		},
		// Direct country match
		"JP": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"CN": {
			{conn_de, conn_jp, conn_at},
			{conn_jp, conn_de, conn_at},
		},
		// Continent match
		"RU": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_de, conn_at, conn_us, conn_jp},
		},
		// No match
		"AR": {
			{conn_us, conn_de, conn_jp, conn_at},
			{conn_us, conn_de, conn_jp, conn_at},
		},
		// No match but override (OC -> AS / NA)
		"AU": {
			{conn_us, conn_jp},
			{conn_us, conn_jp},
		},
		// No match but override (AF -> EU)
		"ZA": {
			{conn_de, conn_at},
			{conn_de, conn_at},
		},
	}

	continentMap := map[string][]string{
		// Use European connections for Africa.
		"AF": {"EU"},
		// Use Asian and North American connections for Oceania.
		"OC": {"AS", "NA"},
	}
	for country, test := range testcases {
		country := country
		test := test
		t.Run(country, func(t *testing.T) {
			sorted := sortConnectionsForCountry(test[0], country, continentMap)
			for idx, conn := range sorted {
				assert.Equal(t, test[1][idx], conn, "Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
			}
		})
	}
}

type proxyServerClientHandler func(msg *ProxyClientMessage) (*ProxyServerMessage, error)

type testProxyServerPublisher struct {
	id string
}

type testProxyServerSubscriber struct {
	id  string
	sid string
	pub *testProxyServerPublisher

	remoteUrl string
}

type testProxyServerClient struct {
	t *testing.T

	server         *TestProxyServerHandler
	ws             *websocket.Conn
	processMessage proxyServerClientHandler

	mu        sync.Mutex
	sessionId string
}

func (c *testProxyServerClient) processHello(msg *ProxyClientMessage) (*ProxyServerMessage, error) {
	if msg.Type != "hello" {
		return nil, fmt.Errorf("expected hello, got %+v", msg)
	}

	token, err := jwt.ParseWithClaims(msg.Hello.Token, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		claims, ok := token.Claims.(*TokenClaims)
		if !assert.True(c.t, ok, "unsupported claims type: %+v", token.Claims) {
			return nil, errors.New("unsupported claims type")
		}

		key, found := c.server.tokens[claims.Issuer]
		if !assert.True(c.t, found) {
			return nil, fmt.Errorf("no key found for issuer")
		}

		return key, nil
	})
	if assert.NoError(c.t, err) {
		if assert.True(c.t, token.Valid) {
			_, ok := token.Claims.(*TokenClaims)
			assert.True(c.t, ok)
		}
	}

	response := &ProxyServerMessage{
		Id:   msg.Id,
		Type: "hello",
		Hello: &HelloProxyServerMessage{
			Version:   "1.0",
			SessionId: c.sessionId,
			Server: &WelcomeServerMessage{
				Version: "1.0",
				Country: c.server.country,
			},
		},
	}
	c.processMessage = c.processRegularMessage
	return response, nil
}

func (c *testProxyServerClient) processRegularMessage(msg *ProxyClientMessage) (*ProxyServerMessage, error) {
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

func (c *testProxyServerClient) processCommandMessage(msg *ProxyClientMessage) (*ProxyServerMessage, error) {
	var response *ProxyServerMessage
	switch msg.Command.Type {
	case "create-publisher":
		pub := c.server.createPublisher()

		if assert.NotNil(c.t, msg.Command.PublisherSettings) {
			if assert.NotEqualValues(c.t, 0, msg.Command.PublisherSettings.Bitrate) {
				assert.EqualValues(c.t, msg.Command.Bitrate, msg.Command.PublisherSettings.Bitrate)
			}
			assert.EqualValues(c.t, msg.Command.MediaTypes, msg.Command.PublisherSettings.MediaTypes)
			if strings.Contains(c.t.Name(), "Codecs") {
				assert.Equal(c.t, "opus,g722", msg.Command.PublisherSettings.AudioCodec)
				assert.Equal(c.t, "vp9,vp8,av1", msg.Command.PublisherSettings.VideoCodec)
			} else {
				assert.Empty(c.t, msg.Command.PublisherSettings.AudioCodec)
				assert.Empty(c.t, msg.Command.PublisherSettings.VideoCodec)
			}
		}

		response = &ProxyServerMessage{
			Id:   msg.Id,
			Type: "command",
			Command: &CommandProxyServerMessage{
				Id:      pub.id,
				Bitrate: msg.Command.Bitrate,
			},
		}
		c.server.updateLoad(1)
	case "delete-publisher":
		if pub, found := c.server.deletePublisher(msg.Command.ClientId); !found {
			response = msg.NewWrappedErrorServerMessage(fmt.Errorf("publisher %s not found", msg.Command.ClientId))
		} else {
			response = &ProxyServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &CommandProxyServerMessage{
					Id: pub.id,
				},
			}
			c.server.updateLoad(-1)
		}
	case "create-subscriber":
		var pub *testProxyServerPublisher
		if msg.Command.RemoteUrl != "" {
			for _, server := range c.server.servers {
				if server.URL != msg.Command.RemoteUrl {
					continue
				}

				token, err := jwt.ParseWithClaims(msg.Command.RemoteToken, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
					claims, ok := token.Claims.(*TokenClaims)
					if !assert.True(c.t, ok, "unsupported claims type: %+v", token.Claims) {
						return nil, errors.New("unsupported claims type")
					}

					key, found := server.tokens[claims.Issuer]
					if !assert.True(c.t, found) {
						return nil, fmt.Errorf("no key found for issuer")
					}

					return key, nil
				})
				if assert.NoError(c.t, err) {
					if claims, ok := token.Claims.(*TokenClaims); assert.True(c.t, token.Valid) && assert.True(c.t, ok) {
						assert.Equal(c.t, msg.Command.PublisherId, claims.Subject)
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
			sub := c.server.createSubscriber(pub)
			response = &ProxyServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &CommandProxyServerMessage{
					Id:  sub.id,
					Sid: sub.sid,
				},
			}
			c.server.updateLoad(1)
		}
	case "delete-subscriber":
		if sub, found := c.server.deleteSubscriber(msg.Command.ClientId); !found {
			response = msg.NewWrappedErrorServerMessage(fmt.Errorf("subscriber %s not found", msg.Command.ClientId))
		} else {
			if msg.Command.RemoteUrl != sub.remoteUrl {
				response = msg.NewWrappedErrorServerMessage(fmt.Errorf("remote subscriber %s not found", msg.Command.ClientId))
				return response, nil
			}

			response = &ProxyServerMessage{
				Id:   msg.Id,
				Type: "command",
				Command: &CommandProxyServerMessage{
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

func (c *testProxyServerClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ws.Close()
	c.ws = nil
}

func (c *testProxyServerClient) handleSendMessageError(fmt string, msg *ProxyServerMessage, err error) {
	c.t.Helper()

	if !errors.Is(err, websocket.ErrCloseSent) || msg.Type != "event" || msg.Event.Type != "update-load" {
		assert.Fail(c.t, "error while sending message", fmt, msg, err)
	}
}

func (c *testProxyServerClient) sendMessage(msg *ProxyServerMessage) {
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
}

func (c *testProxyServerClient) run() {
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		c.server.removeClient(c)
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

		var msg ProxyClientMessage
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

		c.sendMessage(response)
		if response.Type == "hello" {
			c.server.sendLoad(c)
		}
	}
}

type TestProxyServerHandler struct {
	t *testing.T

	URL      string
	server   *httptest.Server
	servers  []*TestProxyServerHandler
	tokens   map[string]*rsa.PublicKey
	upgrader *websocket.Upgrader
	country  string

	mu          sync.Mutex
	load        atomic.Int64
	incoming    atomic.Pointer[float64]
	outgoing    atomic.Pointer[float64]
	clients     map[string]*testProxyServerClient
	publishers  map[string]*testProxyServerPublisher
	subscribers map[string]*testProxyServerSubscriber
}

func (h *TestProxyServerHandler) createPublisher() *testProxyServerPublisher {
	h.mu.Lock()
	defer h.mu.Unlock()
	pub := &testProxyServerPublisher{
		id: newRandomString(32),
	}

	for {
		if _, found := h.publishers[pub.id]; !found {
			break
		}

		pub.id = newRandomString(32)
	}
	h.publishers[pub.id] = pub
	return pub
}

func (h *TestProxyServerHandler) getPublisher(id string) *testProxyServerPublisher {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.publishers[id]
}

func (h *TestProxyServerHandler) deletePublisher(id string) (*testProxyServerPublisher, bool) {
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
		id:  newRandomString(32),
		sid: newRandomString(8),
		pub: pub,
	}

	for {
		if _, found := h.subscribers[sub.id]; !found {
			break
		}

		sub.id = newRandomString(32)
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
		c.sendMessage(msg)
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
		c.sendMessage(msg)
	}
}

func (h *TestProxyServerHandler) getLoadMessage(load int64) *ProxyServerMessage {
	msg := &ProxyServerMessage{
		Type: "event",
		Event: &EventProxyServerMessage{
			Type: "update-load",
			Load: load,
		},
	}

	incoming := h.incoming.Load()
	outgoing := h.outgoing.Load()
	if incoming != nil || outgoing != nil {
		msg.Event.Bandwidth = &EventProxyServerBandwidth{
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

	load := h.load.Add(delta)

	h.mu.Lock()
	defer h.mu.Unlock()

	msg := h.getLoadMessage(load)
	for _, c := range h.clients {
		go c.sendMessage(msg)
	}
}

func (h *TestProxyServerHandler) sendLoad(c *testProxyServerClient) {
	msg := h.getLoadMessage(h.load.Load())
	c.sendMessage(msg)
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
		sessionId: newRandomString(32),
	}

	h.mu.Lock()
	h.clients[client.sessionId] = client
	h.mu.Unlock()

	go client.run()
}

func NewProxyServerForTest(t *testing.T, country string) *TestProxyServerHandler {
	t.Helper()

	upgrader := websocket.Upgrader{}
	proxyHandler := &TestProxyServerHandler{
		t:           t,
		tokens:      make(map[string]*rsa.PublicKey),
		upgrader:    &upgrader,
		country:     country,
		clients:     make(map[string]*testProxyServerClient),
		publishers:  make(map[string]*testProxyServerPublisher),
		subscribers: make(map[string]*testProxyServerSubscriber),
	}
	server := httptest.NewServer(proxyHandler)
	proxyHandler.server = server
	proxyHandler.URL = server.URL
	t.Cleanup(func() {
		server.Close()
		proxyHandler.mu.Lock()
		defer proxyHandler.mu.Unlock()
		for _, c := range proxyHandler.clients {
			c.close()
		}
	})

	return proxyHandler
}

type proxyTestOptions struct {
	etcd    *embed.Etcd
	servers []*TestProxyServerHandler
}

func newMcuProxyForTestWithOptions(t *testing.T, options proxyTestOptions, idx int) *mcuProxy {
	t.Helper()
	require := require.New(t)
	if options.etcd == nil {
		options.etcd = NewEtcdForTest(t)
	}
	grpcClients, dnsMonitor := NewGrpcClientsWithEtcdForTest(t, options.etcd)

	tokenKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(err)
	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	WritePrivateKey(tokenKey, privkeyFile)          // nolint
	WritePublicKey(&tokenKey.PublicKey, pubkeyFile) // nolint

	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "urltype", "static")
	cfg.AddOption("mcu", "proxytimeout", fmt.Sprintf("%d", int(testTimeout.Seconds())))
	var urls []string
	waitingMap := make(map[string]bool)
	if len(options.servers) == 0 {
		options.servers = []*TestProxyServerHandler{
			NewProxyServerForTest(t, "DE"),
		}
	}
	tokenId := fmt.Sprintf("test-token-%d", idx)
	for _, s := range options.servers {
		s.servers = options.servers
		s.tokens[tokenId] = &tokenKey.PublicKey
		urls = append(urls, s.URL)
		waitingMap[s.URL] = true
	}
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	cfg.AddOption("mcu", "token_id", tokenId)
	cfg.AddOption("mcu", "token_key", privkeyFile)

	etcdConfig := goconf.NewConfigFile()
	etcdConfig.AddOption("etcd", "endpoints", options.etcd.Config().ListenClientUrls[0].String())
	etcdConfig.AddOption("etcd", "loglevel", "error")

	etcdClient, err := NewEtcdClient(etcdConfig, "")
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(t, etcdClient.Close())
	})

	mcu, err := NewMcuProxy(cfg, etcdClient, grpcClients, dnsMonitor)
	require.NoError(err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(mcu.Start(ctx))

	proxy := mcu.(*mcuProxy)

	require.NoError(proxy.WaitForConnections(ctx))

	for len(waitingMap) > 0 {
		require.NoError(ctx.Err())

		for u := range waitingMap {
			proxy.connectionsMu.RLock()
			connections := proxy.connections
			proxy.connectionsMu.RUnlock()
			for _, c := range connections {
				if c.rawUrl == u && c.IsConnected() && c.SessionId() != "" {
					delete(waitingMap, u)
					break
				}
			}
		}

		time.Sleep(time.Millisecond)
	}

	return proxy
}

func newMcuProxyForTestWithServers(t *testing.T, servers []*TestProxyServerHandler, idx int) *mcuProxy {
	t.Helper()

	return newMcuProxyForTestWithOptions(t, proxyTestOptions{
		servers: servers,
	}, idx)
}

func newMcuProxyForTest(t *testing.T, idx int) *mcuProxy {
	t.Helper()
	server := NewProxyServerForTest(t, "DE")

	return newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{server}, idx)
}

func Test_ProxyPublisherSubscriber(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyPublisherCodecs(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
		AudioCodec: "opus,g722",
		VideoCodec: "vp9,vp8,av1",
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())
}

func Test_ProxyWaitForPublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	mcu := newMcuProxyForTest(t, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
		if !assert.NoError(t, err) {
			return
		}

		defer sub.Close(context.Background())
	}()

	// Give subscriber goroutine some time to start
	time.Sleep(100 * time.Millisecond)

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	select {
	case <-done:
	case <-ctx.Done():
		assert.NoError(t, ctx.Err())
	}
	defer pub.Close(context.Background())
}

func Test_ProxyPublisherBandwidth(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		server1,
		server2,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pub1Id := "the-publisher-1"
	pub1Sid := "1234567890"
	pub1Listener := &MockMcuListener{
		publicId: pub1Id + "-public",
	}
	pub1Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	if pub1.(*mcuProxyPublisher).conn.rawUrl == server1.URL {
		server1.UpdateBandwidth(100, 0)
	} else {
		server2.UpdateBandwidth(100, 0)
	}

	// Wait until proxy has been updated
	for ctx.Err() == nil {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := true
		for _, c := range connections {
			if c.Bandwidth() != nil {
				missing = false
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	pub2Id := "the-publisher-2"
	pub2id := "1234567890"
	pub2Listener := &MockMcuListener{
		publicId: pub2Id + "-public",
	}
	pub2Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.NotEqual(t, pub1.(*mcuProxyPublisher).conn.rawUrl, pub2.(*mcuProxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherBandwidthOverload(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		server1,
		server2,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pub1Id := "the-publisher-1"
	pub1Sid := "1234567890"
	pub1Listener := &MockMcuListener{
		publicId: pub1Id + "-public",
	}
	pub1Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	// If all servers are bandwidth loaded, select the one with the least usage.
	if pub1.(*mcuProxyPublisher).conn.rawUrl == server1.URL {
		server1.UpdateBandwidth(100, 0)
		server2.UpdateBandwidth(102, 0)
	} else {
		server1.UpdateBandwidth(102, 0)
		server2.UpdateBandwidth(100, 0)
	}

	// Wait until proxy has been updated
	for ctx.Err() == nil {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := false
		for _, c := range connections {
			if c.Bandwidth() == nil {
				missing = true
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	pub2Id := "the-publisher-2"
	pub2id := "1234567890"
	pub2Listener := &MockMcuListener{
		publicId: pub2Id + "-public",
	}
	pub2Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.Equal(t, pub1.(*mcuProxyPublisher).conn.rawUrl, pub2.(*mcuProxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherLoad(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		server1,
		server2,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pub1Id := "the-publisher-1"
	pub1Sid := "1234567890"
	pub1Listener := &MockMcuListener{
		publicId: pub1Id + "-public",
	}
	pub1Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub1Initiator)
	require.NoError(t, err)

	defer pub1.Close(context.Background())

	// Make sure connections are re-sorted.
	mcu.nextSort.Store(0)
	time.Sleep(100 * time.Millisecond)

	pub2Id := "the-publisher-2"
	pub2id := "1234567890"
	pub2Listener := &MockMcuListener{
		publicId: pub2Id + "-public",
	}
	pub2Initiator := &MockMcuInitiator{
		country: "DE",
	}
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pub2Initiator)
	require.NoError(t, err)

	defer pub2.Close(context.Background())

	assert.NotEqual(t, pub1.(*mcuProxyPublisher).conn.rawUrl, pub2.(*mcuProxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherCountry(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubDEId := "the-publisher-de"
	pubDESid := "1234567890"
	pubDEListener := &MockMcuListener{
		publicId: pubDEId + "-public",
	}
	pubDEInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubDEInitiator)
	require.NoError(t, err)

	defer pubDE.Close(context.Background())

	assert.Equal(t, serverDE.URL, pubDE.(*mcuProxyPublisher).conn.rawUrl)

	pubUSId := "the-publisher-us"
	pubUSSid := "1234567890"
	pubUSListener := &MockMcuListener{
		publicId: pubUSId + "-public",
	}
	pubUSInitiator := &MockMcuInitiator{
		country: "US",
	}
	pubUS, err := mcu.NewPublisher(ctx, pubUSListener, pubUSId, pubUSSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubUSInitiator)
	require.NoError(t, err)

	defer pubUS.Close(context.Background())

	assert.Equal(t, serverUS.URL, pubUS.(*mcuProxyPublisher).conn.rawUrl)
}

func Test_ProxyPublisherContinent(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubDEId := "the-publisher-de"
	pubDESid := "1234567890"
	pubDEListener := &MockMcuListener{
		publicId: pubDEId + "-public",
	}
	pubDEInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubDEInitiator)
	require.NoError(t, err)

	defer pubDE.Close(context.Background())

	assert.Equal(t, serverDE.URL, pubDE.(*mcuProxyPublisher).conn.rawUrl)

	pubFRId := "the-publisher-fr"
	pubFRSid := "1234567890"
	pubFRListener := &MockMcuListener{
		publicId: pubFRId + "-public",
	}
	pubFRInitiator := &MockMcuInitiator{
		country: "FR",
	}
	pubFR, err := mcu.NewPublisher(ctx, pubFRListener, pubFRId, pubFRSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubFRInitiator)
	require.NoError(t, err)

	defer pubFR.Close(context.Background())

	assert.Equal(t, serverDE.URL, pubFR.(*mcuProxyPublisher).conn.rawUrl)
}

func Test_ProxySubscriberCountry(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL, pub.(*mcuProxyPublisher).conn.rawUrl)

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "US",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverUS.URL, sub.(*mcuProxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberContinent(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL, pub.(*mcuProxyPublisher).conn.rawUrl)

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "FR",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverDE.URL, sub.(*mcuProxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberBandwidth(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL, pub.(*mcuProxyPublisher).conn.rawUrl)

	serverDE.UpdateBandwidth(0, 100)

	// Wait until proxy has been updated
	for ctx.Err() == nil {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := true
		for _, c := range connections {
			if c.Bandwidth() != nil {
				missing = false
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "US",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverUS.URL, sub.(*mcuProxySubscriber).conn.rawUrl)
}

func Test_ProxySubscriberBandwidthOverload(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*TestProxyServerHandler{
		serverDE,
		serverUS,
	}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}
	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	assert.Equal(t, serverDE.URL, pub.(*mcuProxyPublisher).conn.rawUrl)

	serverDE.UpdateBandwidth(0, 100)
	serverUS.UpdateBandwidth(0, 102)

	// Wait until proxy has been updated
	for ctx.Err() == nil {
		mcu.connectionsMu.RLock()
		connections := mcu.connections
		mcu.connectionsMu.RUnlock()
		missing := false
		for _, c := range connections {
			if c.Bandwidth() == nil {
				missing = true
				break
			}
		}
		if !missing {
			break
		}
		time.Sleep(time.Millisecond)
	}

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "US",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, serverDE.URL, sub.(*mcuProxySubscriber).conn.rawUrl)
}

type mockGrpcServerHub struct {
	proxy             atomic.Pointer[mcuProxy]
	sessionsLock      sync.Mutex
	sessionByPublicId map[string]Session
}

func (h *mockGrpcServerHub) addSession(session *ClientSession) {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()
	if h.sessionByPublicId == nil {
		h.sessionByPublicId = make(map[string]Session)
	}
	h.sessionByPublicId[session.PublicId()] = session
}

func (h *mockGrpcServerHub) removeSession(session *ClientSession) {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()
	delete(h.sessionByPublicId, session.PublicId())
}

func (h *mockGrpcServerHub) GetSessionByResumeId(resumeId string) Session {
	return nil
}

func (h *mockGrpcServerHub) GetSessionByPublicId(sessionId string) Session {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()
	return h.sessionByPublicId[sessionId]
}

func (h *mockGrpcServerHub) GetSessionIdByRoomSessionId(roomSessionId string) (string, error) {
	return "", nil
}

func (h *mockGrpcServerHub) GetBackend(u *url.URL) *Backend {
	return nil
}

func (h *mockGrpcServerHub) GetRoomForBackend(roomId string, backend *Backend) *Room {
	return nil
}

func (h *mockGrpcServerHub) CreateProxyToken(publisherId string) (string, error) {
	proxy := h.proxy.Load()
	if proxy == nil {
		return "", errors.New("not a proxy mcu")
	}

	return proxy.createToken(publisherId)
}

func Test_ProxyRemotePublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")

	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
		},
	}, 2)
	hub2.proxy.Store(mcu2)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyMultipleRemotePublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)
	grpcServer3, addr3 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	hub3 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2
	grpcServer3.hub = hub3

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/three", []byte("{\"address\":\""+addr3+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "US")
	server3 := NewProxyServerForTest(t, "US")

	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
			server3,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
			server3,
		},
	}, 2)
	hub2.proxy.Store(mcu2)
	mcu3 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
			server3,
		},
	}, 3)
	hub3.proxy.Store(mcu3)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	sub1Listener := &MockMcuListener{
		publicId: "subscriber-public-1",
	}
	sub1Initiator := &MockMcuInitiator{
		country: "US",
	}
	sub1, err := mcu2.NewSubscriber(ctx, sub1Listener, pubId, StreamTypeVideo, sub1Initiator)
	require.NoError(t, err)

	defer sub1.Close(context.Background())

	sub2Listener := &MockMcuListener{
		publicId: "subscriber-public-2",
	}
	sub2Initiator := &MockMcuInitiator{
		country: "US",
	}
	sub2, err := mcu3.NewSubscriber(ctx, sub2Listener, pubId, StreamTypeVideo, sub2Initiator)
	require.NoError(t, err)

	defer sub2.Close(context.Background())
}

func Test_ProxyRemotePublisherWait(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")

	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
			server2,
		},
	}, 2)
	hub2.proxy.Store(mcu2)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
		if !assert.NoError(t, err) {
			return
		}

		defer sub.Close(context.Background())
	}()

	// Give subscriber goroutine some time to start
	time.Sleep(100 * time.Millisecond)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		assert.NoError(t, ctx.Err())
	}
}

func Test_ProxyRemotePublisherTemporary(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")

	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server2,
		},
	}, 2)
	hub2.proxy.Store(mcu2)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	mcu2.connectionsMu.RLock()
	count := len(mcu2.connections)
	mcu2.connectionsMu.RUnlock()
	assert.Equal(t, 1, count)

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	assert.Equal(t, server1.URL, sub.(*mcuProxySubscriber).conn.rawUrl)

	// The temporary connection has been added
	mcu2.connectionsMu.RLock()
	count = len(mcu2.connections)
	mcu2.connectionsMu.RUnlock()
	assert.Equal(t, 2, count)

	sub.Close(context.Background())

	// Wait for temporary connection to be removed.
loop:
	for {
		select {
		case <-ctx.Done():
			assert.NoError(t, ctx.Err())
		default:
			mcu2.connectionsMu.RLock()
			count = len(mcu2.connections)
			mcu2.connectionsMu.RUnlock()
			if count == 1 {
				break loop
			}
		}
	}
}

func Test_ProxyConnectToken(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")

	// Signaling server instances are in a cluster but don't share their proxies,
	// i.e. they are only known to their local proxy, not the one of the other
	// signaling server - so the connection token must be passed between them.
	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server2,
		},
	}, 2)
	hub2.proxy.Store(mcu2)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "DE",
	}
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyPublisherToken(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()

	etcd := NewEtcdForTest(t)

	grpcServer1, addr1 := NewGrpcServerForTest(t)
	grpcServer2, addr2 := NewGrpcServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.hub = hub1
	grpcServer2.hub = hub2

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "US")

	// Signaling server instances are in a cluster but don't share their proxies,
	// i.e. they are only known to their local proxy, not the one of the other
	// signaling server - so the connection token must be passed between them.
	// Also the subscriber is connecting from a different country, so a remote
	// stream will be created that needs a valid token from the remote proxy.
	mcu1 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server1,
		},
	}, 1)
	hub1.proxy.Store(mcu1)
	mcu2 := newMcuProxyForTestWithOptions(t, proxyTestOptions{
		etcd: etcd,
		servers: []*TestProxyServerHandler{
			server2,
		},
	}, 2)
	hub2.proxy.Store(mcu2)
	// Support remote subscribers for the tests.
	server1.servers = append(server1.servers, server2)
	server2.servers = append(server2.servers, server1)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "the-publisher"
	pubSid := "1234567890"
	pubListener := &MockMcuListener{
		publicId: pubId + "-public",
	}
	pubInitiator := &MockMcuInitiator{
		country: "DE",
	}

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[StreamType]McuPublisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, NewPublisherSettings{
		MediaTypes: MediaTypeVideo | MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	subInitiator := &MockMcuInitiator{
		country: "US",
	}
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}
