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
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/websocket"

	"golang.org/x/net/context"

	"gopkg.in/dgrijalva/jwt-go.v3"
)

const (
	closeTimeout = time.Second

	proxyDebugMessages = false

	// Very high value so the connections get sorted at the end.
	loadNotConnected = 1000000

	// Sort connections by load every 10 publishing requests or once per second.
	connectionSortRequests = 10
	connectionSortInterval = time.Second
)

type mcuProxyPubSubCommon struct {
	streamType string
	proxyId    string
	conn       *mcuProxyConnection
	listener   McuListener
}

func (c *mcuProxyPubSubCommon) Id() string {
	return c.proxyId
}

func (c *mcuProxyPubSubCommon) StreamType() string {
	return c.streamType
}

func (c *mcuProxyPubSubCommon) doSendMessage(ctx context.Context, msg *ProxyClientMessage, callback func(error, map[string]interface{})) {
	c.conn.performAsyncRequest(ctx, msg, func(err error, response *ProxyServerMessage) {
		if err != nil {
			callback(err, nil)
			return
		}

		if proxyDebugMessages {
			log.Printf("Response from %s: %+v", c.conn.url, response)
		}
		if response.Type == "error" {
			callback(response.Error, nil)
		} else if response.Payload != nil {
			callback(nil, response.Payload.Payload)
		} else {
			callback(nil, nil)
		}
	})
}

func (c *mcuProxyPubSubCommon) doProcessPayload(client McuClient, msg *PayloadProxyServerMessage) {
	switch msg.Type {
	case "candidate":
		c.listener.OnIceCandidate(client, msg.Payload["candidate"])
	default:
		log.Printf("Unsupported payload from %s: %+v", c.conn.url, msg)
	}
}

type mcuProxyPublisher struct {
	mcuProxyPubSubCommon

	id string
}

func newMcuProxyPublisher(id string, streamType string, proxyId string, conn *mcuProxyConnection, listener McuListener) *mcuProxyPublisher {
	return &mcuProxyPublisher{
		mcuProxyPubSubCommon: mcuProxyPubSubCommon{
			streamType: streamType,
			proxyId:    proxyId,
			conn:       conn,
			listener:   listener,
		},
		id: id,
	}
}

func (p *mcuProxyPublisher) NotifyClosed() {
	p.listener.PublisherClosed(p)
	p.conn.removePublisher(p)
}

func (p *mcuProxyPublisher) Close(ctx context.Context) {
	p.NotifyClosed()

	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:     "delete-publisher",
			ClientId: p.proxyId,
		},
	}

	if _, err := p.conn.performSyncRequest(ctx, msg); err != nil {
		log.Printf("Could not delete publisher %s at %s: %s", p.proxyId, p.conn.url, err)
		return
	}
}

func (p *mcuProxyPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	msg := &ProxyClientMessage{
		Type: "payload",
		Payload: &PayloadProxyClientMessage{
			Type:     data.Type,
			ClientId: p.proxyId,
			Payload:  data.Payload,
		},
	}

	p.doSendMessage(ctx, msg, callback)
}

func (p *mcuProxyPublisher) ProcessPayload(msg *PayloadProxyServerMessage) {
	p.doProcessPayload(p, msg)
}

func (p *mcuProxyPublisher) ProcessEvent(msg *EventProxyServerMessage) {
	switch msg.Type {
	case "ice-completed":
		p.listener.OnIceCompleted(p)
	case "publisher-closed":
		p.NotifyClosed()
	default:
		log.Printf("Unsupported event from %s: %+v", p.conn.url, msg)
	}
}

type mcuProxySubscriber struct {
	mcuProxyPubSubCommon

	publisherId string
}

func newMcuProxySubscriber(publisherId string, streamType string, proxyId string, conn *mcuProxyConnection, listener McuListener) *mcuProxySubscriber {
	return &mcuProxySubscriber{
		mcuProxyPubSubCommon: mcuProxyPubSubCommon{
			streamType: streamType,
			proxyId:    proxyId,
			conn:       conn,
			listener:   listener,
		},

		publisherId: publisherId,
	}
}

func (s *mcuProxySubscriber) Publisher() string {
	return s.publisherId
}

func (s *mcuProxySubscriber) NotifyClosed() {
	s.listener.SubscriberClosed(s)
	s.conn.removeSubscriber(s)
}

func (s *mcuProxySubscriber) Close(ctx context.Context) {
	s.NotifyClosed()

	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:     "delete-subscriber",
			ClientId: s.proxyId,
		},
	}

	if _, err := s.conn.performSyncRequest(ctx, msg); err != nil {
		log.Printf("Could not delete subscriber %s at %s: %s", s.proxyId, s.conn.url, err)
		return
	}
}

func (s *mcuProxySubscriber) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	msg := &ProxyClientMessage{
		Type: "payload",
		Payload: &PayloadProxyClientMessage{
			Type:     data.Type,
			ClientId: s.proxyId,
			Payload:  data.Payload,
		},
	}

	s.doSendMessage(ctx, msg, callback)
}

func (s *mcuProxySubscriber) ProcessPayload(msg *PayloadProxyServerMessage) {
	s.doProcessPayload(s, msg)
}

func (s *mcuProxySubscriber) ProcessEvent(msg *EventProxyServerMessage) {
	switch msg.Type {
	case "ice-completed":
		s.listener.OnIceCompleted(s)
	case "subscriber-closed":
		s.NotifyClosed()
	default:
		log.Printf("Unsupported event from %s: %+v", s.conn.url, msg)
	}
}

type mcuProxyConnection struct {
	proxy *mcuProxy
	url   *url.URL

	mu         sync.Mutex
	closeChan  chan bool
	closedChan chan bool
	closed     uint32
	conn       *websocket.Conn

	connectedSince    time.Time
	reconnectInterval int64
	reconnectTimer    *time.Timer
	shutdownScheduled uint32

	msgId      int64
	helloMsgId string
	sessionId  string
	load       int64

	callbacks map[string]func(*ProxyServerMessage)

	publishersLock sync.RWMutex
	publishers     map[string]*mcuProxyPublisher
	publisherIds   map[string]string

	subscribersLock sync.RWMutex
	subscribers     map[string]*mcuProxySubscriber
}

func newMcuProxyConnection(proxy *mcuProxy, baseUrl string) (*mcuProxyConnection, error) {
	parsed, err := url.Parse(baseUrl)
	if err != nil {
		return nil, err
	}

	conn := &mcuProxyConnection{
		proxy:             proxy,
		url:               parsed,
		closeChan:         make(chan bool, 1),
		closedChan:        make(chan bool, 1),
		reconnectInterval: int64(initialReconnectInterval),
		load:              loadNotConnected,
		callbacks:         make(map[string]func(*ProxyServerMessage)),
		publishers:        make(map[string]*mcuProxyPublisher),
		publisherIds:      make(map[string]string),
		subscribers:       make(map[string]*mcuProxySubscriber),
	}
	return conn, nil
}

type mcuProxyConnectionStats struct {
	Url        string     `json:"url"`
	Connected  bool       `json:"connected"`
	Publishers int64      `json:"publishers"`
	Clients    int64      `json:"clients"`
	Uptime     *time.Time `json:"uptime,omitempty"`
}

func (c *mcuProxyConnection) GetStats() *mcuProxyConnectionStats {
	result := &mcuProxyConnectionStats{
		Url: c.url.String(),
	}
	c.mu.Lock()
	if c.conn != nil {
		result.Connected = true
		result.Uptime = &c.connectedSince
	}
	c.mu.Unlock()
	c.publishersLock.RLock()
	result.Publishers = int64(len(c.publishers))
	c.publishersLock.RUnlock()
	c.subscribersLock.RLock()
	result.Clients = int64(len(c.subscribers))
	c.subscribersLock.RUnlock()
	result.Clients += result.Publishers
	return result
}

func (c *mcuProxyConnection) Load() int64 {
	return atomic.LoadInt64(&c.load)
}

func (c *mcuProxyConnection) IsShutdownScheduled() bool {
	return atomic.LoadUint32(&c.shutdownScheduled) != 0
}

func (c *mcuProxyConnection) readPump() {
	defer func() {
		if atomic.LoadUint32(&c.closed) == 0 {
			c.scheduleReconnect()
		} else {
			c.closedChan <- true
		}
	}()
	defer c.close()
	defer atomic.StoreInt64(&c.load, loadNotConnected)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				log.Printf("Error reading from %s: %v", c.url, err)
			}
			break
		}

		var msg ProxyServerMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error unmarshaling %s from %s: %s", string(message), c.url, err)
			continue
		}

		c.processMessage(&msg)
	}
}

func (c *mcuProxyConnection) writePump() {
	c.reconnectTimer = time.NewTimer(0)
	for {
		select {
		case <-c.reconnectTimer.C:
			c.reconnect()
		case <-c.closeChan:
			return
		}
	}
}

func (c *mcuProxyConnection) start() error {
	go c.writePump()
	return nil
}

func (c *mcuProxyConnection) sendClose() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}

	return c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func (c *mcuProxyConnection) stop(ctx context.Context) {
	if !atomic.CompareAndSwapUint32(&c.closed, 0, 1) {
		return
	}

	c.closeChan <- true
	if err := c.sendClose(); err != nil {
		if err != ErrNotConnected {
			log.Printf("Could not send close message to %s: %s", c.url, err)
		}
		c.close()
		return
	}

	select {
	case <-c.closedChan:
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			log.Printf("Error waiting for connection to %s get closed: %s", c.url, err)
			c.close()
		}
	}
}

func (c *mcuProxyConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *mcuProxyConnection) scheduleReconnect() {
	if err := c.sendClose(); err != nil && err != ErrNotConnected {
		log.Printf("Could not send close message to %s: %s", c.url, err)
		c.close()
	}

	interval := atomic.LoadInt64(&c.reconnectInterval)
	c.reconnectTimer.Reset(time.Duration(interval))

	interval = interval * 2
	if interval > int64(maxReconnectInterval) {
		interval = int64(maxReconnectInterval)
	}
	atomic.StoreInt64(&c.reconnectInterval, interval)
}

func (c *mcuProxyConnection) reconnect() {
	u, err := c.url.Parse("proxy")
	if err != nil {
		log.Printf("Could not resolve url to proxy at %s: %s", c.url, err)
		c.scheduleReconnect()
		return
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("Could not connect to %s: %s", u, err)
		c.scheduleReconnect()
		return
	}

	log.Printf("Connected to %s", u)
	atomic.StoreUint32(&c.closed, 0)

	c.mu.Lock()
	c.connectedSince = time.Now()
	c.conn = conn
	c.mu.Unlock()

	atomic.StoreInt64(&c.reconnectInterval, int64(initialReconnectInterval))
	atomic.StoreUint32(&c.shutdownScheduled, 0)
	if err := c.sendHello(); err != nil {
		log.Printf("Could not send hello request to %s: %s", c.url, err)
		c.scheduleReconnect()
		return
	}

	go c.readPump()
}

func (c *mcuProxyConnection) removePublisher(publisher *mcuProxyPublisher) {
	c.proxy.removePublisher(publisher)

	c.publishersLock.Lock()
	defer c.publishersLock.Unlock()

	delete(c.publishers, publisher.proxyId)
	delete(c.publisherIds, publisher.id+"|"+publisher.StreamType())
}

func (c *mcuProxyConnection) clearPublishers() {
	c.publishersLock.Lock()
	defer c.publishersLock.Unlock()

	go func(publishers map[string]*mcuProxyPublisher) {
		for _, publisher := range publishers {
			publisher.NotifyClosed()
		}
	}(c.publishers)
	c.publishers = make(map[string]*mcuProxyPublisher)
	c.publisherIds = make(map[string]string)
}

func (c *mcuProxyConnection) removeSubscriber(subscriber *mcuProxySubscriber) {
	c.subscribersLock.Lock()
	defer c.subscribersLock.Unlock()

	delete(c.subscribers, subscriber.proxyId)
}

func (c *mcuProxyConnection) clearSubscribers() {
	c.subscribersLock.Lock()
	defer c.subscribersLock.Unlock()

	go func(subscribers map[string]*mcuProxySubscriber) {
		for _, subscriber := range subscribers {
			subscriber.NotifyClosed()
		}
	}(c.subscribers)
	c.subscribers = make(map[string]*mcuProxySubscriber)
}

func (c *mcuProxyConnection) clearCallbacks() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.callbacks = make(map[string]func(*ProxyServerMessage))
}

func (c *mcuProxyConnection) getCallback(id string) func(*ProxyServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	callback, found := c.callbacks[id]
	if found {
		delete(c.callbacks, id)
	}
	return callback
}

func (c *mcuProxyConnection) processMessage(msg *ProxyServerMessage) {
	if c.helloMsgId != "" && msg.Id == c.helloMsgId {
		c.helloMsgId = ""
		switch msg.Type {
		case "error":
			if msg.Error.Code == "no_such_session" {
				log.Printf("Session %s could not be resumed on %s, registering new", c.sessionId, c.url)
				c.clearPublishers()
				c.clearSubscribers()
				c.clearCallbacks()
				c.sessionId = ""
				if err := c.sendHello(); err != nil {
					log.Printf("Could not send hello request to %s: %s", c.url, err)
					c.scheduleReconnect()
				}
				return
			}

			log.Printf("Hello connection to %s failed with %+v, reconnecting", c.url, msg.Error)
			c.scheduleReconnect()
		case "hello":
			c.sessionId = msg.Hello.SessionId
			log.Printf("Received session %s from %s", c.sessionId, c.url)
		default:
			log.Printf("Received unsupported hello response %+v from %s, reconnecting", msg, c.url)
			c.scheduleReconnect()
		}
		return
	}

	if proxyDebugMessages {
		log.Printf("Received from %s: %+v", c.url, msg)
	}
	callback := c.getCallback(msg.Id)
	if callback != nil {
		callback(msg)
		return
	}

	switch msg.Type {
	case "payload":
		c.processPayload(msg)
	case "event":
		c.processEvent(msg)
	default:
		log.Printf("Unsupported message received from %s: %+v", c.url, msg)
	}
}

func (c *mcuProxyConnection) processPayload(msg *ProxyServerMessage) {
	payload := msg.Payload
	c.publishersLock.RLock()
	publisher, found := c.publishers[payload.ClientId]
	c.publishersLock.RUnlock()
	if found {
		publisher.ProcessPayload(payload)
		return
	}

	c.subscribersLock.RLock()
	subscriber, found := c.subscribers[payload.ClientId]
	c.subscribersLock.RUnlock()
	if found {
		subscriber.ProcessPayload(payload)
		return
	}

	log.Printf("Received payload for unknown client %+v from %s", payload, c.url)
}

func (c *mcuProxyConnection) processEvent(msg *ProxyServerMessage) {
	event := msg.Event
	switch event.Type {
	case "backend-disconnected":
		log.Printf("Upstream backend at %s got disconnected, reset MCU objects", c.url)
		c.clearPublishers()
		c.clearSubscribers()
		c.clearCallbacks()
		// TODO: Should we also reconnect?
		return
	case "backend-connected":
		log.Printf("Upstream backend at %s is connected", c.url)
		return
	case "update-load":
		if proxyDebugMessages {
			log.Printf("Load of %s now at %d", c.url, event.Load)
		}
		atomic.StoreInt64(&c.load, event.Load)
		return
	case "shutdown-scheduled":
		log.Printf("Proxy %s is scheduled to shutdown", c.url)
		atomic.StoreUint32(&c.shutdownScheduled, 1)
		return
	}

	if proxyDebugMessages {
		log.Printf("Process event from %s: %+v", c.url, event)
	}
	c.publishersLock.RLock()
	publisher, found := c.publishers[event.ClientId]
	c.publishersLock.RUnlock()
	if found {
		publisher.ProcessEvent(event)
		return
	}

	c.subscribersLock.RLock()
	subscriber, found := c.subscribers[event.ClientId]
	c.subscribersLock.RUnlock()
	if found {
		subscriber.ProcessEvent(event)
		return
	}

	log.Printf("Received event for unknown client %+v from %s", event, c.url)
}

func (c *mcuProxyConnection) sendHello() error {
	c.helloMsgId = strconv.FormatInt(atomic.AddInt64(&c.msgId, 1), 10)
	msg := &ProxyClientMessage{
		Id:   c.helloMsgId,
		Type: "hello",
		Hello: &HelloProxyClientMessage{
			Version: "1.0",
		},
	}
	if c.sessionId != "" {
		msg.Hello.ResumeId = c.sessionId
	} else {
		claims := &TokenClaims{
			jwt.StandardClaims{
				IssuedAt: time.Now().Unix(),
				Issuer:   c.proxy.tokenId,
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(c.proxy.tokenKey)
		if err != nil {
			return err
		}

		msg.Hello.Token = tokenString
	}
	return c.sendMessage(msg)
}

func (c *mcuProxyConnection) sendMessage(msg *ProxyClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sendMessageLocked(msg)
}

func (c *mcuProxyConnection) sendMessageLocked(msg *ProxyClientMessage) error {
	if proxyDebugMessages {
		log.Printf("Send message to %s: %+v", c.url, msg)
	}
	if c.conn == nil {
		return ErrNotConnected
	}
	return c.conn.WriteJSON(msg)
}

func (c *mcuProxyConnection) performAsyncRequest(ctx context.Context, msg *ProxyClientMessage, callback func(err error, response *ProxyServerMessage)) {
	msgId := strconv.FormatInt(atomic.AddInt64(&c.msgId, 1), 10)
	msg.Id = msgId

	c.mu.Lock()
	defer c.mu.Unlock()
	c.callbacks[msgId] = func(msg *ProxyServerMessage) {
		callback(nil, msg)
	}
	if err := c.sendMessageLocked(msg); err != nil {
		delete(c.callbacks, msgId)
		go callback(err, nil)
		return
	}
}

func (c *mcuProxyConnection) performSyncRequest(ctx context.Context, msg *ProxyClientMessage) (*ProxyServerMessage, error) {
	errChan := make(chan error, 1)
	responseChan := make(chan *ProxyServerMessage, 1)
	c.performAsyncRequest(ctx, msg, func(err error, response *ProxyServerMessage) {
		if err != nil {
			errChan <- err
		} else {
			responseChan <- response
		}
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, err
	case response := <-responseChan:
		return response, nil
	}
}

func (c *mcuProxyConnection) newPublisher(ctx context.Context, listener McuListener, id string, streamType string) (McuPublisher, error) {
	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:       "create-publisher",
			StreamType: streamType,
		},
	}

	response, err := c.performSyncRequest(ctx, msg)
	if err != nil {
		// TODO: Cancel request
		return nil, err
	}

	proxyId := response.Command.Id
	log.Printf("Created %s publisher %s on %s for %s", streamType, proxyId, c.url, id)
	publisher := newMcuProxyPublisher(id, streamType, proxyId, c, listener)
	c.publishersLock.Lock()
	c.publishers[proxyId] = publisher
	c.publisherIds[id+"|"+streamType] = proxyId
	c.publishersLock.Unlock()
	return publisher, nil
}

func (c *mcuProxyConnection) newSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	c.publishersLock.Lock()
	id, found := c.publisherIds[publisher+"|"+streamType]
	c.publishersLock.Unlock()
	if !found {
		return nil, fmt.Errorf("Unknown publisher %s", publisher)
	}

	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:        "create-subscriber",
			StreamType:  streamType,
			PublisherId: id,
		},
	}

	response, err := c.performSyncRequest(ctx, msg)
	if err != nil {
		// TODO: Cancel request
		return nil, err
	}

	proxyId := response.Command.Id
	log.Printf("Created %s subscriber %s on %s for %s", streamType, proxyId, c.url, publisher)
	subscriber := newMcuProxySubscriber(publisher, streamType, proxyId, c, listener)
	c.subscribersLock.Lock()
	c.subscribers[proxyId] = subscriber
	c.subscribersLock.Unlock()
	return subscriber, nil
}

type mcuProxy struct {
	tokenId  string
	tokenKey *rsa.PrivateKey

	connections  atomic.Value
	connRequests int64
	nextSort     int64

	mu         sync.RWMutex
	publishers map[string]*mcuProxyConnection

	publisherWaitersId uint64
	publisherWaiters   map[uint64]chan bool
}

func NewMcuProxy(baseUrl string, config *goconf.ConfigFile) (Mcu, error) {
	var connections []*mcuProxyConnection

	tokenId, _ := config.GetString("mcu", "token_id")
	if tokenId == "" {
		return nil, fmt.Errorf("No token id configured")
	}
	tokenKeyFilename, _ := config.GetString("mcu", "token_key")
	if tokenKeyFilename == "" {
		return nil, fmt.Errorf("No token key configured")
	}
	tokenKeyData, err := ioutil.ReadFile(tokenKeyFilename)
	if err != nil {
		return nil, fmt.Errorf("Could not read private key from %s: %s", tokenKeyFilename, err)
	}
	tokenKey, err := jwt.ParseRSAPrivateKeyFromPEM(tokenKeyData)
	if err != nil {
		return nil, fmt.Errorf("Could not parse private key from %s: %s", tokenKeyFilename, err)
	}

	mcu := &mcuProxy{
		tokenId:  tokenId,
		tokenKey: tokenKey,

		publishers: make(map[string]*mcuProxyConnection),

		publisherWaiters: make(map[uint64]chan bool),
	}

	for _, u := range strings.Split(baseUrl, " ") {
		conn, err := newMcuProxyConnection(mcu, u)
		if err != nil {
			return nil, err
		}

		connections = append(connections, conn)
	}
	if len(connections) == 0 {
		return nil, fmt.Errorf("No MCU proxy connections configured")
	}

	mcu.setConnections(connections)
	return mcu, nil
}

func (m *mcuProxy) setConnections(connections []*mcuProxyConnection) {
	m.connections.Store(connections)
}

func (m *mcuProxy) getConnections() []*mcuProxyConnection {
	return m.connections.Load().([]*mcuProxyConnection)
}

func (m *mcuProxy) Start() error {
	for _, c := range m.getConnections() {
		if err := c.start(); err != nil {
			return err
		}
	}
	return nil
}

func (m *mcuProxy) Stop() {
	for _, c := range m.getConnections() {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		c.stop(ctx)
	}
}

func (m *mcuProxy) SetOnConnected(f func()) {
	// Not supported.
}

func (m *mcuProxy) SetOnDisconnected(f func()) {
	// Not supported.
}

type mcuProxyStats struct {
	Publishers int64                               `json:"publishers"`
	Clients    int64                               `json:"clients"`
	Details    map[string]*mcuProxyConnectionStats `json:"details"`
}

func (m *mcuProxy) GetStats() interface{} {
	details := make(map[string]*mcuProxyConnectionStats)
	result := &mcuProxyStats{
		Details: details,
	}
	for _, conn := range m.getConnections() {
		stats := conn.GetStats()
		result.Publishers += stats.Publishers
		result.Clients += stats.Clients
		details[stats.Url] = stats
	}
	return result
}

type mcuProxyConnectionsList []*mcuProxyConnection

func (l mcuProxyConnectionsList) Len() int {
	return len(l)
}

func (l mcuProxyConnectionsList) Less(i, j int) bool {
	return l[i].Load() < l[j].Load()
}

func (l mcuProxyConnectionsList) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

func (l mcuProxyConnectionsList) Sort() {
	sort.Sort(l)
}

func (m *mcuProxy) getSortedConnections() []*mcuProxyConnection {
	connections := m.getConnections()
	if len(connections) < 2 {
		return connections
	}

	// Connections are re-sorted every <connectionSortRequests> requests or
	// every <connectionSortInterval>.
	now := time.Now().UnixNano()
	if atomic.AddInt64(&m.connRequests, 1)%connectionSortRequests == 0 || atomic.LoadInt64(&m.nextSort) <= now {
		atomic.StoreInt64(&m.nextSort, now+int64(connectionSortInterval))

		sorted := make(mcuProxyConnectionsList, len(connections))
		copy(sorted, connections)

		sorted.Sort()

		m.setConnections(sorted)
		connections = sorted
	}

	return connections
}

func (m *mcuProxy) removePublisher(publisher *mcuProxyPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.publishers, publisher.id+"|"+publisher.StreamType())
}

func (m *mcuProxy) wakeupWaiters() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.publisherWaiters {
		ch <- true
	}
}

func (m *mcuProxy) addWaiter(ch chan bool) uint64 {
	id := m.publisherWaitersId + 1
	m.publisherWaitersId = id
	m.publisherWaiters[id] = ch
	return id
}

func (m *mcuProxy) removeWaiter(id uint64) {
	delete(m.publisherWaiters, id)
}

func (m *mcuProxy) NewPublisher(ctx context.Context, listener McuListener, id string, streamType string) (McuPublisher, error) {
	connections := m.getSortedConnections()
	for _, conn := range connections {
		if conn.IsShutdownScheduled() {
			continue
		}

		publisher, err := conn.newPublisher(ctx, listener, id, streamType)
		if err != nil {
			log.Printf("Could not create %s publisher for %s on %s: %s", streamType, id, conn.url, err)
			continue
		}

		m.mu.Lock()
		m.publishers[id+"|"+streamType] = conn
		m.mu.Unlock()
		m.wakeupWaiters()
		return publisher, nil
	}

	return nil, fmt.Errorf("No MCU connection available")
}

func (m *mcuProxy) getPublisherConnection(ctx context.Context, publisher string, streamType string) *mcuProxyConnection {
	m.mu.RLock()
	conn := m.publishers[publisher+"|"+streamType]
	m.mu.RUnlock()
	if conn != nil {
		return conn
	}

	log.Printf("No %s publisher %s found yet, deferring", streamType, publisher)
	m.mu.Lock()
	defer m.mu.Unlock()

	conn = m.publishers[publisher+"|"+streamType]
	if conn != nil {
		return conn
	}

	ch := make(chan bool, 1)
	id := m.addWaiter(ch)
	defer m.removeWaiter(id)

	for {
		m.mu.Unlock()
		select {
		case <-ch:
			m.mu.Lock()
			conn = m.publishers[publisher+"|"+streamType]
			if conn != nil {
				return conn
			}
		case <-ctx.Done():
			m.mu.Lock()
			return nil
		}
	}
}

func (m *mcuProxy) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	conn := m.getPublisherConnection(ctx, publisher, streamType)
	if conn == nil {
		return nil, fmt.Errorf("No %s publisher %s found", streamType, publisher)
	}

	return conn.newSubscriber(ctx, listener, publisher, streamType)
}
