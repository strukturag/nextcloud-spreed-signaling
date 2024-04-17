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
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/websocket"
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
				if test[1][idx] != conn {
					t.Errorf("Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
				}
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
				if test[1][idx] != conn {
					t.Errorf("Index %d for %s: expected %s, got %s", idx, country, test[1][idx].Country(), conn.Country())
				}
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
}

type testProxyServerClient struct {
	t *testing.T

	server         *testProxyServerHandler
	ws             *websocket.Conn
	processMessage proxyServerClientHandler

	mu        sync.Mutex
	sessionId string
}

func (c *testProxyServerClient) processHello(msg *ProxyClientMessage) (*ProxyServerMessage, error) {
	if msg.Type != "hello" {
		return nil, fmt.Errorf("expected hello, got %+v", msg)
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
		pub := c.server.getPublisher(msg.Command.PublisherId)
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
		c.t.Errorf(fmt, msg, err)
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
				c.t.Error(err)
			}
			return
		}

		body, err := io.ReadAll(reader)
		if err != nil {
			c.t.Error(err)
			continue
		}

		if msgType != websocket.TextMessage {
			c.t.Errorf("unexpected message type %q (%s)", msgType, string(body))
			continue
		}

		var msg ProxyClientMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Errorf("could not decode message %s: %s", string(body), err)
			continue
		}

		if err := msg.CheckValid(); err != nil {
			c.t.Errorf("invalid message %s: %s", string(body), err)
			continue
		}

		response, err := c.processMessage(&msg)
		if err != nil {
			c.t.Error(err)
			continue
		}

		c.sendMessage(response)
		if response.Type == "hello" {
			c.server.sendLoad(c)
		}
	}
}

type testProxyServerHandler struct {
	t *testing.T

	upgrader *websocket.Upgrader
	country  string

	mu          sync.Mutex
	load        atomic.Int64
	clients     map[string]*testProxyServerClient
	publishers  map[string]*testProxyServerPublisher
	subscribers map[string]*testProxyServerSubscriber
}

func (h *testProxyServerHandler) createPublisher() *testProxyServerPublisher {
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

func (h *testProxyServerHandler) getPublisher(id string) *testProxyServerPublisher {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.publishers[id]
}

func (h *testProxyServerHandler) deletePublisher(id string) (*testProxyServerPublisher, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	pub, found := h.publishers[id]
	if !found {
		return nil, false
	}

	delete(h.publishers, id)
	return pub, true
}

func (h *testProxyServerHandler) createSubscriber(pub *testProxyServerPublisher) *testProxyServerSubscriber {
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

func (h *testProxyServerHandler) deleteSubscriber(id string) (*testProxyServerSubscriber, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sub, found := h.subscribers[id]
	if !found {
		return nil, false
	}

	delete(h.subscribers, id)
	return sub, true
}

func (h *testProxyServerHandler) updateLoad(delta int64) {
	if delta == 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	load := h.load.Add(delta)
	for _, c := range h.clients {
		go func(c *testProxyServerClient, load int64) {
			c.sendMessage(&ProxyServerMessage{
				Type: "event",
				Event: &EventProxyServerMessage{
					Type: "update-load",
					Load: load,
				},
			})
		}(c, load)
	}
}

func (h *testProxyServerHandler) sendLoad(c *testProxyServerClient) {
	c.sendMessage(&ProxyServerMessage{
		Type: "event",
		Event: &EventProxyServerMessage{
			Type: "update-load",
			Load: h.load.Load(),
		},
	})
}

func (h *testProxyServerHandler) removeClient(client *testProxyServerClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, client.sessionId)
}

func (h *testProxyServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.t.Error(err)
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

func NewProxyServerForTest(t *testing.T, country string) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	proxyHandler := &testProxyServerHandler{
		t:           t,
		upgrader:    &upgrader,
		country:     country,
		clients:     make(map[string]*testProxyServerClient),
		publishers:  make(map[string]*testProxyServerPublisher),
		subscribers: make(map[string]*testProxyServerSubscriber),
	}
	server := httptest.NewServer(proxyHandler)
	t.Cleanup(func() {
		server.Close()
		proxyHandler.mu.Lock()
		defer proxyHandler.mu.Unlock()
		for _, c := range proxyHandler.clients {
			c.close()
		}
	})

	return server
}

type proxyTestOptions struct {
	etcd    *embed.Etcd
	servers []*httptest.Server
}

func newMcuProxyForTestWithOptions(t *testing.T, options proxyTestOptions) *mcuProxy {
	t.Helper()
	if options.etcd == nil {
		options.etcd = NewEtcdForTest(t)
	}
	grpcClients, dnsMonitor := NewGrpcClientsWithEtcdForTest(t, options.etcd)

	tokenKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	privkeyFile := path.Join(dir, "privkey.pem")
	pubkeyFile := path.Join(dir, "pubkey.pem")
	WritePrivateKey(tokenKey, privkeyFile)          // nolint
	WritePublicKey(&tokenKey.PublicKey, pubkeyFile) // nolint

	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "urltype", "static")
	var urls []string
	waitingMap := make(map[string]bool)
	if len(options.servers) == 0 {
		options.servers = []*httptest.Server{
			NewProxyServerForTest(t, "DE"),
		}
	}
	for _, s := range options.servers {
		urls = append(urls, s.URL)
		waitingMap[s.URL] = true
	}
	cfg.AddOption("mcu", "url", strings.Join(urls, " "))
	cfg.AddOption("mcu", "token_id", "test-token")
	cfg.AddOption("mcu", "token_key", privkeyFile)

	etcdConfig := goconf.NewConfigFile()
	etcdConfig.AddOption("etcd", "endpoints", options.etcd.Config().ListenClientUrls[0].String())
	etcdConfig.AddOption("etcd", "loglevel", "error")

	etcdClient, err := NewEtcdClient(etcdConfig, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := etcdClient.Close(); err != nil {
			t.Error(err)
		}
	})

	mcu, err := NewMcuProxy(cfg, etcdClient, grpcClients, dnsMonitor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mcu.Stop()
	})

	if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}

	proxy := mcu.(*mcuProxy)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := proxy.WaitForConnections(ctx); err != nil {
		t.Fatal(err)
	}

	for len(waitingMap) > 0 {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}

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

func newMcuProxyForTestWithServers(t *testing.T, servers []*httptest.Server) *mcuProxy {
	t.Helper()

	return newMcuProxyForTestWithOptions(t, proxyTestOptions{
		servers: servers,
	})
}

func newMcuProxyForTest(t *testing.T) *mcuProxy {
	t.Helper()
	server := NewProxyServerForTest(t, "DE")

	return newMcuProxyForTestWithServers(t, []*httptest.Server{server})
}

func Test_ProxyPublisherSubscriber(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	mcu := newMcuProxyForTest(t)

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

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubInitiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pub.Close(context.Background())

	subListener := &MockMcuListener{
		publicId: "subscriber-public",
	}
	sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo)
	if err != nil {
		t.Fatal(err)
	}

	defer sub.Close(context.Background())
}

func Test_ProxyWaitForPublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	mcu := newMcuProxyForTest(t)

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
	done := make(chan struct{})
	go func() {
		defer close(done)
		sub, err := mcu.NewSubscriber(ctx, subListener, pubId, StreamTypeVideo)
		if err != nil {
			t.Error(err)
			return
		}

		defer sub.Close(context.Background())
	}()

	// Give subscriber goroutine some time to start
	time.Sleep(100 * time.Millisecond)

	pub, err := mcu.NewPublisher(ctx, pubListener, pubId, pubSid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubInitiator)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Error(ctx.Err())
	}
	defer pub.Close(context.Background())
}

func Test_ProxyPublisherLoad(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	server1 := NewProxyServerForTest(t, "DE")
	server2 := NewProxyServerForTest(t, "DE")
	mcu := newMcuProxyForTestWithServers(t, []*httptest.Server{
		server1,
		server2,
	})

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
	pub1, err := mcu.NewPublisher(ctx, pub1Listener, pub1Id, pub1Sid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pub1Initiator)
	if err != nil {
		t.Fatal(err)
	}

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
	pub2, err := mcu.NewPublisher(ctx, pub2Listener, pub2Id, pub2id, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pub2Initiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pub2.Close(context.Background())

	if pub1.(*mcuProxyPublisher).conn.rawUrl == pub2.(*mcuProxyPublisher).conn.rawUrl {
		t.Errorf("servers should be different, got %s", pub1.(*mcuProxyPublisher).conn.rawUrl)
	}
}

func Test_ProxyPublisherCountry(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*httptest.Server{
		serverDE,
		serverUS,
	})

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
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubDEInitiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pubDE.Close(context.Background())

	if pubDE.(*mcuProxyPublisher).conn.rawUrl != serverDE.URL {
		t.Errorf("expected server %s, go %s", serverDE.URL, pubDE.(*mcuProxyPublisher).conn.rawUrl)
	}

	pubUSId := "the-publisher-us"
	pubUSSid := "1234567890"
	pubUSListener := &MockMcuListener{
		publicId: pubUSId + "-public",
	}
	pubUSInitiator := &MockMcuInitiator{
		country: "US",
	}
	pubUS, err := mcu.NewPublisher(ctx, pubUSListener, pubUSId, pubUSSid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubUSInitiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pubUS.Close(context.Background())

	if pubUS.(*mcuProxyPublisher).conn.rawUrl != serverUS.URL {
		t.Errorf("expected server %s, go %s", serverUS.URL, pubUS.(*mcuProxyPublisher).conn.rawUrl)
	}
}

func Test_ProxyPublisherContinent(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	serverDE := NewProxyServerForTest(t, "DE")
	serverUS := NewProxyServerForTest(t, "US")
	mcu := newMcuProxyForTestWithServers(t, []*httptest.Server{
		serverDE,
		serverUS,
	})

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
	pubDE, err := mcu.NewPublisher(ctx, pubDEListener, pubDEId, pubDESid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubDEInitiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pubDE.Close(context.Background())

	if pubDE.(*mcuProxyPublisher).conn.rawUrl != serverDE.URL {
		t.Errorf("expected server %s, go %s", serverDE.URL, pubDE.(*mcuProxyPublisher).conn.rawUrl)
	}

	pubFRId := "the-publisher-fr"
	pubFRSid := "1234567890"
	pubFRListener := &MockMcuListener{
		publicId: pubFRId + "-public",
	}
	pubFRInitiator := &MockMcuInitiator{
		country: "FR",
	}
	pubFR, err := mcu.NewPublisher(ctx, pubFRListener, pubFRId, pubFRSid, StreamTypeVideo, 0, MediaTypeVideo|MediaTypeAudio, pubFRInitiator)
	if err != nil {
		t.Fatal(err)
	}

	defer pubFR.Close(context.Background())

	if pubFR.(*mcuProxyPublisher).conn.rawUrl != serverDE.URL {
		t.Errorf("expected server %s, go %s", serverDE.URL, pubFR.(*mcuProxyPublisher).conn.rawUrl)
	}
}
