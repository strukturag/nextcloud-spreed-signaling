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
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"
)

const (
	closeTimeout = time.Second

	proxyDebugMessages = false

	// Very high value so the connections get sorted at the end.
	loadNotConnected = 1000000

	// Sort connections by load every 10 publishing requests or once per second.
	connectionSortRequests = 10
	connectionSortInterval = time.Second

	proxyUrlTypeStatic = "static"
	proxyUrlTypeEtcd   = "etcd"

	initialWaitDelay = time.Second
	maxWaitDelay     = 8 * time.Second

	defaultProxyTimeoutSeconds = 2

	rttLogDuration = 500 * time.Millisecond

	// Update service IP addresses every 10 seconds.
	updateDnsInterval = 10 * time.Second
)

type McuProxy interface {
	AddConnection(ignoreErrors bool, url string, ips ...net.IP) error
	KeepConnection(url string, ips ...net.IP)
	RemoveConnection(url string, ips ...net.IP)
}

type mcuProxyPubSubCommon struct {
	sid        string
	streamType string
	proxyId    string
	conn       *mcuProxyConnection
	listener   McuListener
}

func (c *mcuProxyPubSubCommon) Id() string {
	return c.proxyId
}

func (c *mcuProxyPubSubCommon) Sid() string {
	return c.sid
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
			log.Printf("Response from %s: %+v", c.conn, response)
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
	case "offer":
		c.listener.OnUpdateOffer(client, msg.Payload["offer"].(map[string]interface{}))
	case "candidate":
		c.listener.OnIceCandidate(client, msg.Payload["candidate"])
	default:
		log.Printf("Unsupported payload from %s: %+v", c.conn, msg)
	}
}

type mcuProxyPublisher struct {
	mcuProxyPubSubCommon

	id         string
	mediaTypes MediaType
}

func newMcuProxyPublisher(id string, sid string, streamType string, mediaTypes MediaType, proxyId string, conn *mcuProxyConnection, listener McuListener) *mcuProxyPublisher {
	return &mcuProxyPublisher{
		mcuProxyPubSubCommon: mcuProxyPubSubCommon{
			sid:        sid,
			streamType: streamType,
			proxyId:    proxyId,
			conn:       conn,
			listener:   listener,
		},
		id:         id,
		mediaTypes: mediaTypes,
	}
}

func (p *mcuProxyPublisher) HasMedia(mt MediaType) bool {
	return (p.mediaTypes & mt) == mt
}

func (p *mcuProxyPublisher) SetMedia(mt MediaType) {
	// TODO: Also update mediaTypes on proxy.
	p.mediaTypes = mt
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

	if response, err := p.conn.performSyncRequest(ctx, msg); err != nil {
		log.Printf("Could not delete publisher %s at %s: %s", p.proxyId, p.conn, err)
		return
	} else if response.Type == "error" {
		log.Printf("Could not delete publisher %s at %s: %s", p.proxyId, p.conn, response.Error)
		return
	}

	log.Printf("Delete publisher %s at %s", p.proxyId, p.conn)
}

func (p *mcuProxyPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	msg := &ProxyClientMessage{
		Type: "payload",
		Payload: &PayloadProxyClientMessage{
			Type:     data.Type,
			ClientId: p.proxyId,
			Sid:      data.Sid,
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
		log.Printf("Unsupported event from %s: %+v", p.conn, msg)
	}
}

type mcuProxySubscriber struct {
	mcuProxyPubSubCommon

	publisherId string
}

func newMcuProxySubscriber(publisherId string, sid string, streamType string, proxyId string, conn *mcuProxyConnection, listener McuListener) *mcuProxySubscriber {
	return &mcuProxySubscriber{
		mcuProxyPubSubCommon: mcuProxyPubSubCommon{
			sid:        sid,
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

	if response, err := s.conn.performSyncRequest(ctx, msg); err != nil {
		log.Printf("Could not delete subscriber %s at %s: %s", s.proxyId, s.conn, err)
		return
	} else if response.Type == "error" {
		log.Printf("Could not delete subscriber %s at %s: %s", s.proxyId, s.conn, response.Error)
		return
	}

	log.Printf("Delete subscriber %s at %s", s.proxyId, s.conn)
}

func (s *mcuProxySubscriber) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	msg := &ProxyClientMessage{
		Type: "payload",
		Payload: &PayloadProxyClientMessage{
			Type:     data.Type,
			ClientId: s.proxyId,
			Sid:      data.Sid,
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
	case "subscriber-sid-updated":
		s.sid = msg.Sid
		s.listener.SubscriberSidUpdated(s)
	case "subscriber-closed":
		s.NotifyClosed()
	default:
		log.Printf("Unsupported event from %s: %+v", s.conn, msg)
	}
}

type mcuProxyConnection struct {
	proxy  *mcuProxy
	rawUrl string
	url    *url.URL
	ip     net.IP

	load       atomic.Int64
	mu         sync.Mutex
	closer     *Closer
	closedDone *Closer
	closed     atomic.Bool
	conn       *websocket.Conn

	connectedSince    time.Time
	reconnectTimer    *time.Timer
	reconnectInterval atomic.Int64
	shutdownScheduled atomic.Bool
	closeScheduled    atomic.Bool
	trackClose        atomic.Bool
	temporary         atomic.Bool

	connectedNotifier SingleNotifier

	msgId      atomic.Int64
	helloMsgId string
	sessionId  string
	country    atomic.Value

	callbacks map[string]func(*ProxyServerMessage)

	publishersLock sync.RWMutex
	publishers     map[string]*mcuProxyPublisher
	publisherIds   map[string]string

	subscribersLock sync.RWMutex
	subscribers     map[string]*mcuProxySubscriber
}

func newMcuProxyConnection(proxy *mcuProxy, baseUrl string, ip net.IP) (*mcuProxyConnection, error) {
	parsed, err := url.Parse(baseUrl)
	if err != nil {
		return nil, err
	}

	conn := &mcuProxyConnection{
		proxy:        proxy,
		rawUrl:       baseUrl,
		url:          parsed,
		ip:           ip,
		closer:       NewCloser(),
		closedDone:   NewCloser(),
		callbacks:    make(map[string]func(*ProxyServerMessage)),
		publishers:   make(map[string]*mcuProxyPublisher),
		publisherIds: make(map[string]string),
		subscribers:  make(map[string]*mcuProxySubscriber),
	}
	conn.reconnectInterval.Store(int64(initialReconnectInterval))
	conn.load.Store(loadNotConnected)
	conn.country.Store("")
	return conn, nil
}

func (c *mcuProxyConnection) String() string {
	if c.ip != nil {
		return fmt.Sprintf("%s (%s)", c.rawUrl, c.ip)
	}

	return c.rawUrl
}

type mcuProxyConnectionStats struct {
	Url        string     `json:"url"`
	IP         net.IP     `json:"ip,omitempty"`
	Connected  bool       `json:"connected"`
	Publishers int64      `json:"publishers"`
	Clients    int64      `json:"clients"`
	Load       *int64     `json:"load,omitempty"`
	Shutdown   *bool      `json:"shutdown,omitempty"`
	Temporary  *bool      `json:"temporary,omitempty"`
	Uptime     *time.Time `json:"uptime,omitempty"`
}

func (c *mcuProxyConnection) GetStats() *mcuProxyConnectionStats {
	result := &mcuProxyConnectionStats{
		Url: c.url.String(),
		IP:  c.ip,
	}
	c.mu.Lock()
	if c.conn != nil {
		result.Connected = true
		result.Uptime = &c.connectedSince
		load := c.Load()
		result.Load = &load
		shutdown := c.IsShutdownScheduled()
		result.Shutdown = &shutdown
		temporary := c.IsTemporary()
		result.Temporary = &temporary
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
	return c.load.Load()
}

func (c *mcuProxyConnection) Country() string {
	return c.country.Load().(string)
}

func (c *mcuProxyConnection) IsTemporary() bool {
	return c.temporary.Load()
}

func (c *mcuProxyConnection) setTemporary() {
	c.temporary.Store(true)
}

func (c *mcuProxyConnection) clearTemporary() {
	c.temporary.Store(false)
}

func (c *mcuProxyConnection) IsShutdownScheduled() bool {
	return c.shutdownScheduled.Load() || c.closeScheduled.Load()
}

func (c *mcuProxyConnection) readPump() {
	defer func() {
		if !c.closed.Load() {
			c.scheduleReconnect()
		} else {
			c.closedDone.Close()
		}
	}()
	defer c.close()
	defer c.load.Store(loadNotConnected)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	conn.SetPongHandler(func(msg string) error {
		now := time.Now()
		conn.SetReadDeadline(now.Add(pongWait)) // nolint
		if msg == "" {
			return nil
		}
		if ts, err := strconv.ParseInt(msg, 10, 64); err == nil {
			rtt := now.Sub(time.Unix(0, ts))
			if rtt >= rttLogDuration {
				rtt_ms := rtt.Nanoseconds() / time.Millisecond.Nanoseconds()
				log.Printf("Proxy at %s has RTT of %d ms (%s)", c, rtt_ms, rtt)
			}
		}
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		_, message, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrCloseSent) {
				break
			} else if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				log.Printf("Error reading from %s: %v", c, err)
			}
			break
		}

		var msg ProxyServerMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error unmarshaling %s from %s: %s", string(message), c, err)
			continue
		}

		c.processMessage(&msg)
	}
}

func (c *mcuProxyConnection) sendPing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}

	now := time.Now()
	msg := strconv.FormatInt(now.UnixNano(), 10)
	c.conn.SetWriteDeadline(now.Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		log.Printf("Could not send ping to proxy at %s: %v", c, err)
		go c.scheduleReconnect()
		return false
	}

	return true
}

func (c *mcuProxyConnection) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()

	c.reconnectTimer = time.NewTimer(0)
	defer c.reconnectTimer.Stop()
	for {
		select {
		case <-c.reconnectTimer.C:
			c.reconnect()
		case <-ticker.C:
			c.sendPing()
		case <-c.closer.C:
			return
		}
	}
}

func (c *mcuProxyConnection) start() {
	go c.writePump()
}

func (c *mcuProxyConnection) sendClose() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	return c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func (c *mcuProxyConnection) stop(ctx context.Context) {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}

	c.closer.Close()
	if err := c.sendClose(); err != nil {
		if err != ErrNotConnected {
			log.Printf("Could not send close message to %s: %s", c, err)
		}
		c.close()
		return
	}

	select {
	case <-c.closedDone.C:
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			log.Printf("Error waiting for connection to %s get closed: %s", c, err)
			c.close()
		}
	}
}

func (c *mcuProxyConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connectedNotifier.Reset()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		if c.trackClose.CompareAndSwap(true, false) {
			statsConnectedProxyBackendsCurrent.WithLabelValues(c.Country()).Dec()
		}
	}
}

func (c *mcuProxyConnection) stopCloseIfEmpty() {
	c.closeScheduled.Store(false)
}

func (c *mcuProxyConnection) closeIfEmpty() bool {
	c.closeScheduled.Store(true)

	var total int64
	c.publishersLock.RLock()
	total += int64(len(c.publishers))
	c.publishersLock.RUnlock()
	c.subscribersLock.RLock()
	total += int64(len(c.subscribers))
	c.subscribersLock.RUnlock()
	if total > 0 {
		// Connection will be closed once all clients have disconnected.
		log.Printf("Connection to %s is still used by %d clients, defer closing", c, total)
		return false
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()

		log.Printf("All clients disconnected, closing connection to %s", c)
		c.stop(ctx)

		c.proxy.removeConnection(c)
	}()
	return true
}

func (c *mcuProxyConnection) scheduleReconnect() {
	if err := c.sendClose(); err != nil && err != ErrNotConnected {
		log.Printf("Could not send close message to %s: %s", c, err)
	}
	c.close()

	if c.IsShutdownScheduled() {
		c.proxy.removeConnection(c)
		return
	}

	interval := c.reconnectInterval.Load()
	c.reconnectTimer.Reset(time.Duration(interval))

	interval = interval * 2
	if interval > int64(maxReconnectInterval) {
		interval = int64(maxReconnectInterval)
	}
	c.reconnectInterval.Store(interval)
}

func (c *mcuProxyConnection) reconnect() {
	u, err := c.url.Parse("proxy")
	if err != nil {
		log.Printf("Could not resolve url to proxy at %s: %s", c, err)
		c.scheduleReconnect()
		return
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	dialer := c.proxy.dialer
	if c.ip != nil {
		dialer = &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: c.proxy.dialer.HandshakeTimeout,
			TLSClientConfig:  c.proxy.dialer.TLSClientConfig,

			// Override DNS lookup and connect to custom IP address.
			NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if _, port, err := net.SplitHostPort(addr); err == nil {
					addr = net.JoinHostPort(c.ip.String(), port)
				}

				return net.Dial(network, addr)
			},
		}
	}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("Could not connect to %s: %s", c, err)
		c.scheduleReconnect()
		return
	}

	if c.IsShutdownScheduled() {
		c.proxy.removeConnection(c)
		return
	}

	log.Printf("Connected to %s", c)
	c.closed.Store(false)

	c.mu.Lock()
	c.connectedSince = time.Now()
	c.conn = conn
	c.mu.Unlock()

	c.reconnectInterval.Store(int64(initialReconnectInterval))
	c.shutdownScheduled.Store(false)
	if err := c.sendHello(); err != nil {
		log.Printf("Could not send hello request to %s: %s", c, err)
		c.scheduleReconnect()
		return
	}

	if !c.sendPing() {
		return
	}

	go c.readPump()
}

func (c *mcuProxyConnection) waitUntilConnected(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	waiter := c.connectedNotifier.NewWaiter()
	defer c.connectedNotifier.Release(waiter)

	c.mu.Unlock()
	defer c.mu.Lock()
	return waiter.Wait(ctx)
}

func (c *mcuProxyConnection) removePublisher(publisher *mcuProxyPublisher) {
	c.proxy.removePublisher(publisher)

	c.publishersLock.Lock()
	defer c.publishersLock.Unlock()

	if _, found := c.publishers[publisher.proxyId]; found {
		delete(c.publishers, publisher.proxyId)
		statsPublishersCurrent.WithLabelValues(publisher.StreamType()).Dec()
	}
	delete(c.publisherIds, publisher.id+"|"+publisher.StreamType())

	if len(c.publishers) == 0 && (c.closeScheduled.Load() || c.IsTemporary()) {
		go c.closeIfEmpty()
	}
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

	if c.closeScheduled.Load() || c.IsTemporary() {
		go c.closeIfEmpty()
	}
}

func (c *mcuProxyConnection) removeSubscriber(subscriber *mcuProxySubscriber) {
	c.subscribersLock.Lock()
	defer c.subscribersLock.Unlock()

	if _, found := c.subscribers[subscriber.proxyId]; found {
		delete(c.subscribers, subscriber.proxyId)
		statsSubscribersCurrent.WithLabelValues(subscriber.StreamType()).Dec()
	}

	if len(c.subscribers) == 0 && (c.closeScheduled.Load() || c.IsTemporary()) {
		go c.closeIfEmpty()
	}
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

	if c.closeScheduled.Load() || c.IsTemporary() {
		go c.closeIfEmpty()
	}
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
				log.Printf("Session %s could not be resumed on %s, registering new", c.sessionId, c)
				c.clearPublishers()
				c.clearSubscribers()
				c.clearCallbacks()
				c.sessionId = ""
				if err := c.sendHello(); err != nil {
					log.Printf("Could not send hello request to %s: %s", c, err)
					c.scheduleReconnect()
				}
				return
			}

			log.Printf("Hello connection to %s failed with %+v, reconnecting", c, msg.Error)
			c.scheduleReconnect()
		case "hello":
			resumed := c.sessionId == msg.Hello.SessionId
			c.sessionId = msg.Hello.SessionId
			country := ""
			if msg.Hello.Server != nil {
				if country = msg.Hello.Server.Country; country != "" && !IsValidCountry(country) {
					log.Printf("Proxy %s sent invalid country %s in hello response", c, country)
					country = ""
				}
			}
			c.country.Store(country)
			if resumed {
				log.Printf("Resumed session %s on %s", c.sessionId, c)
			} else if country != "" {
				log.Printf("Received session %s from %s (in %s)", c.sessionId, c, country)
			} else {
				log.Printf("Received session %s from %s", c.sessionId, c)
			}
			if c.trackClose.CompareAndSwap(false, true) {
				statsConnectedProxyBackendsCurrent.WithLabelValues(c.Country()).Inc()
			}

			c.connectedNotifier.Notify()
		default:
			log.Printf("Received unsupported hello response %+v from %s, reconnecting", msg, c)
			c.scheduleReconnect()
		}
		return
	}

	if proxyDebugMessages {
		log.Printf("Received from %s: %+v", c, msg)
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
	case "bye":
		c.processBye(msg)
	default:
		log.Printf("Unsupported message received from %s: %+v", c, msg)
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

	log.Printf("Received payload for unknown client %+v from %s", payload, c)
}

func (c *mcuProxyConnection) processEvent(msg *ProxyServerMessage) {
	event := msg.Event
	switch event.Type {
	case "backend-disconnected":
		log.Printf("Upstream backend at %s got disconnected, reset MCU objects", c)
		c.clearPublishers()
		c.clearSubscribers()
		c.clearCallbacks()
		// TODO: Should we also reconnect?
		return
	case "backend-connected":
		log.Printf("Upstream backend at %s is connected", c)
		return
	case "update-load":
		if proxyDebugMessages {
			log.Printf("Load of %s now at %d", c, event.Load)
		}
		c.load.Store(event.Load)
		statsProxyBackendLoadCurrent.WithLabelValues(c.url.String()).Set(float64(event.Load))
		return
	case "shutdown-scheduled":
		log.Printf("Proxy %s is scheduled to shutdown", c)
		c.shutdownScheduled.Store(true)
		return
	}

	if proxyDebugMessages {
		log.Printf("Process event from %s: %+v", c, event)
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

	log.Printf("Received event for unknown client %+v from %s", event, c)
}

func (c *mcuProxyConnection) processBye(msg *ProxyServerMessage) {
	bye := msg.Bye
	switch bye.Reason {
	case "session_resumed":
		log.Printf("Session %s on %s was resumed by other client, resetting", c.sessionId, c)
		c.sessionId = ""
	default:
		log.Printf("Received bye with unsupported reason from %s %+v", c, bye)
	}
}

func (c *mcuProxyConnection) sendHello() error {
	c.helloMsgId = strconv.FormatInt(c.msgId.Add(1), 10)
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
			jwt.RegisteredClaims{
				IssuedAt: jwt.NewNumericDate(time.Now()),
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
		log.Printf("Send message to %s: %+v", c, msg)
	}
	if c.conn == nil {
		return ErrNotConnected
	}
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	return c.conn.WriteJSON(msg)
}

func (c *mcuProxyConnection) performAsyncRequest(ctx context.Context, msg *ProxyClientMessage, callback func(err error, response *ProxyServerMessage)) {
	msgId := strconv.FormatInt(c.msgId.Add(1), 10)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}

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

func (c *mcuProxyConnection) newPublisher(ctx context.Context, listener McuListener, id string, sid string, streamType string, bitrate int, mediaTypes MediaType) (McuPublisher, error) {
	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:       "create-publisher",
			Sid:        sid,
			StreamType: streamType,
			Bitrate:    bitrate,
			MediaTypes: mediaTypes,
		},
	}

	response, err := c.performSyncRequest(ctx, msg)
	if err != nil {
		// TODO: Cancel request
		return nil, err
	} else if response.Type == "error" {
		return nil, fmt.Errorf("Error creating %s publisher for %s on %s: %+v", streamType, id, c, response.Error)
	}

	proxyId := response.Command.Id
	log.Printf("Created %s publisher %s on %s for %s", streamType, proxyId, c, id)
	publisher := newMcuProxyPublisher(id, sid, streamType, mediaTypes, proxyId, c, listener)
	c.publishersLock.Lock()
	c.publishers[proxyId] = publisher
	c.publisherIds[id+"|"+streamType] = proxyId
	c.publishersLock.Unlock()
	statsPublishersCurrent.WithLabelValues(streamType).Inc()
	statsPublishersTotal.WithLabelValues(streamType).Inc()
	return publisher, nil
}

func (c *mcuProxyConnection) newSubscriber(ctx context.Context, listener McuListener, publisherId string, publisherSessionId string, streamType string) (McuSubscriber, error) {
	msg := &ProxyClientMessage{
		Type: "command",
		Command: &CommandProxyClientMessage{
			Type:        "create-subscriber",
			StreamType:  streamType,
			PublisherId: publisherId,
		},
	}

	response, err := c.performSyncRequest(ctx, msg)
	if err != nil {
		// TODO: Cancel request
		return nil, err
	} else if response.Type == "error" {
		return nil, fmt.Errorf("Error creating %s subscriber for %s on %s: %+v", streamType, publisherSessionId, c, response.Error)
	}

	proxyId := response.Command.Id
	log.Printf("Created %s subscriber %s on %s for %s", streamType, proxyId, c, publisherSessionId)
	subscriber := newMcuProxySubscriber(publisherSessionId, response.Command.Sid, streamType, proxyId, c, listener)
	c.subscribersLock.Lock()
	c.subscribers[proxyId] = subscriber
	c.subscribersLock.Unlock()
	statsSubscribersCurrent.WithLabelValues(streamType).Inc()
	statsSubscribersTotal.WithLabelValues(streamType).Inc()
	return subscriber, nil
}

type mcuProxy struct {
	urlType  string
	tokenId  string
	tokenKey *rsa.PrivateKey
	config   ProxyConfig

	dialer         *websocket.Dialer
	connections    []*mcuProxyConnection
	connectionsMap map[string][]*mcuProxyConnection
	connectionsMu  sync.RWMutex
	proxyTimeout   time.Duration
	connRequests   atomic.Int64
	nextSort       atomic.Int64

	maxStreamBitrate int
	maxScreenBitrate int

	mu         sync.RWMutex
	publishers map[string]*mcuProxyConnection

	publisherWaiters ChannelWaiters

	continentsMap atomic.Value

	rpcClients *GrpcClients
}

func NewMcuProxy(config *goconf.ConfigFile, etcdClient *EtcdClient, rpcClients *GrpcClients, dnsMonitor *DnsMonitor) (Mcu, error) {
	urlType, _ := config.GetString("mcu", "urltype")
	if urlType == "" {
		urlType = proxyUrlTypeStatic
	}

	tokenId, _ := config.GetString("mcu", "token_id")
	if tokenId == "" {
		return nil, fmt.Errorf("No token id configured")
	}
	tokenKeyFilename, _ := config.GetString("mcu", "token_key")
	if tokenKeyFilename == "" {
		return nil, fmt.Errorf("No token key configured")
	}
	tokenKeyData, err := os.ReadFile(tokenKeyFilename)
	if err != nil {
		return nil, fmt.Errorf("Could not read private key from %s: %s", tokenKeyFilename, err)
	}
	tokenKey, err := jwt.ParseRSAPrivateKeyFromPEM(tokenKeyData)
	if err != nil {
		return nil, fmt.Errorf("Could not parse private key from %s: %s", tokenKeyFilename, err)
	}

	proxyTimeoutSeconds, _ := config.GetInt("mcu", "proxytimeout")
	if proxyTimeoutSeconds <= 0 {
		proxyTimeoutSeconds = defaultProxyTimeoutSeconds
	}
	proxyTimeout := time.Duration(proxyTimeoutSeconds) * time.Second
	log.Printf("Using a timeout of %s for proxy requests", proxyTimeout)

	maxStreamBitrate, _ := config.GetInt("mcu", "maxstreambitrate")
	if maxStreamBitrate <= 0 {
		maxStreamBitrate = defaultMaxStreamBitrate
	}
	maxScreenBitrate, _ := config.GetInt("mcu", "maxscreenbitrate")
	if maxScreenBitrate <= 0 {
		maxScreenBitrate = defaultMaxScreenBitrate
	}

	mcu := &mcuProxy{
		urlType:  urlType,
		tokenId:  tokenId,
		tokenKey: tokenKey,

		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: proxyTimeout,
		},
		connectionsMap: make(map[string][]*mcuProxyConnection),
		proxyTimeout:   proxyTimeout,

		maxStreamBitrate: maxStreamBitrate,
		maxScreenBitrate: maxScreenBitrate,

		publishers: make(map[string]*mcuProxyConnection),

		rpcClients: rpcClients,
	}

	if err := mcu.loadContinentsMap(config); err != nil {
		return nil, err
	}

	skipverify, _ := config.GetBool("mcu", "skipverify")
	if skipverify {
		log.Println("WARNING: MCU verification is disabled!")
		mcu.dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: skipverify,
		}
	}

	switch urlType {
	case proxyUrlTypeStatic:
		mcu.config, err = NewProxyConfigStatic(config, mcu, dnsMonitor)
	case proxyUrlTypeEtcd:
		mcu.config, err = NewProxyConfigEtcd(config, etcdClient, mcu)
	default:
		err = fmt.Errorf("Unsupported proxy URL type %s", urlType)
	}
	if err != nil {
		return nil, err
	}

	return mcu, nil
}

func (m *mcuProxy) loadContinentsMap(config *goconf.ConfigFile) error {
	options, err := GetStringOptions(config, "continent-overrides", false)
	if err != nil {
		return err
	}

	if len(options) == 0 {
		m.setContinentsMap(nil)
		return nil
	}

	continentsMap := make(map[string][]string)
	for option, value := range options {
		option = strings.ToUpper(strings.TrimSpace(option))
		if !IsValidContinent(option) {
			log.Printf("Ignore unknown continent %s", option)
			continue
		}

		var values []string
		for _, v := range strings.Split(value, ",") {
			v = strings.ToUpper(strings.TrimSpace(v))
			if !IsValidContinent(v) {
				log.Printf("Ignore unknown continent %s for override %s", v, option)
				continue
			}
			values = append(values, v)
		}
		if len(values) == 0 {
			log.Printf("No valid values found for continent override %s, ignoring", option)
			continue
		}

		continentsMap[option] = values
		log.Printf("Mapping users on continent %s to %s", option, values)
	}

	m.setContinentsMap(continentsMap)
	return nil
}

func (m *mcuProxy) Start() error {
	log.Printf("Maximum bandwidth %d bits/sec per publishing stream", m.maxStreamBitrate)
	log.Printf("Maximum bandwidth %d bits/sec per screensharing stream", m.maxScreenBitrate)

	return m.config.Start()
}

func (m *mcuProxy) Stop() {
	m.connectionsMu.RLock()
	defer m.connectionsMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	for _, c := range m.connections {
		c.stop(ctx)
	}

	m.config.Stop()
}

func (m *mcuProxy) AddConnection(ignoreErrors bool, url string, ips ...net.IP) error {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	var conns []*mcuProxyConnection
	if len(ips) == 0 {
		conn, err := newMcuProxyConnection(m, url, nil)
		if err != nil {
			if ignoreErrors {
				log.Printf("Could not create proxy connection to %s: %s", url, err)
				return nil
			}

			return err
		}

		conns = append(conns, conn)
	} else {
		for _, ip := range ips {
			conn, err := newMcuProxyConnection(m, url, ip)
			if err != nil {
				if ignoreErrors {
					log.Printf("Could not create proxy connection to %s (%s): %s", url, ip, err)
					continue
				}

				return err
			}

			conns = append(conns, conn)
		}
	}

	for _, conn := range conns {
		log.Printf("Adding new connection to %s", conn)
		conn.start()

		m.connections = append(m.connections, conn)
		if existing, found := m.connectionsMap[url]; found {
			m.connectionsMap[url] = append(existing, conn)
		} else {
			m.connectionsMap[url] = []*mcuProxyConnection{conn}
		}
	}

	m.nextSort.Store(0)
	return nil
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, i := range ips {
		if i.Equal(ip) {
			return true
		}
	}

	return false
}

func (m *mcuProxy) iterateConnections(url string, ips []net.IP, f func(conn *mcuProxyConnection)) {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	conns, found := m.connectionsMap[url]
	if !found {
		return
	}

	var toRemove []*mcuProxyConnection
	if len(ips) == 0 {
		toRemove = conns
	} else {
		for _, conn := range conns {
			if containsIP(ips, conn.ip) {
				toRemove = append(toRemove, conn)
			}
		}
	}

	for _, conn := range toRemove {
		f(conn)
	}
}

func (m *mcuProxy) RemoveConnection(url string, ips ...net.IP) {
	m.iterateConnections(url, ips, func(conn *mcuProxyConnection) {
		log.Printf("Removing connection to %s", conn)
		conn.closeIfEmpty()
	})
}

func (m *mcuProxy) KeepConnection(url string, ips ...net.IP) {
	m.iterateConnections(url, ips, func(conn *mcuProxyConnection) {
		conn.stopCloseIfEmpty()
		conn.clearTemporary()
	})
}

func (m *mcuProxy) Reload(config *goconf.ConfigFile) {
	if err := m.loadContinentsMap(config); err != nil {
		log.Printf("Error loading continents map: %s", err)
	}

	if err := m.config.Reload(config); err != nil {
		log.Printf("could not reload proxy configuration: %s", err)
	}
}

func (m *mcuProxy) removeConnection(c *mcuProxyConnection) {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	if conns, found := m.connectionsMap[c.rawUrl]; found {
		for idx, conn := range conns {
			if conn == c {
				conns = append(conns[:idx], conns[idx+1:]...)
				break
			}
		}
		if len(conns) == 0 {
			delete(m.connectionsMap, c.rawUrl)
			m.connections = nil
			for _, conns := range m.connectionsMap {
				m.connections = append(m.connections, conns...)
			}
		} else {
			m.connectionsMap[c.rawUrl] = conns
		}

		m.nextSort.Store(0)
	}
}

func (m *mcuProxy) SetOnConnected(f func()) {
	// Not supported.
}

func (m *mcuProxy) SetOnDisconnected(f func()) {
	// Not supported.
}

type mcuProxyStats struct {
	Publishers int64                      `json:"publishers"`
	Clients    int64                      `json:"clients"`
	Details    []*mcuProxyConnectionStats `json:"details"`
}

func (m *mcuProxy) GetStats() interface{} {
	result := &mcuProxyStats{}

	m.connectionsMu.RLock()
	defer m.connectionsMu.RUnlock()

	for _, conn := range m.connections {
		stats := conn.GetStats()
		result.Publishers += stats.Publishers
		result.Clients += stats.Clients
		result.Details = append(result.Details, stats)
	}
	return result
}

func (m *mcuProxy) getContinentsMap() map[string][]string {
	continentsMap := m.continentsMap.Load()
	if continentsMap == nil {
		return nil
	}
	return continentsMap.(map[string][]string)
}

func (m *mcuProxy) setContinentsMap(continentsMap map[string][]string) {
	if continentsMap == nil {
		continentsMap = make(map[string][]string)
	}
	m.continentsMap.Store(continentsMap)
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

func ContinentsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	for _, checkA := range a {
		for _, checkB := range b {
			if checkA == checkB {
				return true
			}
		}
	}
	return false
}

func sortConnectionsForCountry(connections []*mcuProxyConnection, country string, continentMap map[string][]string) []*mcuProxyConnection {
	// Move connections in the same country to the start of the list.
	sorted := make(mcuProxyConnectionsList, 0, len(connections))
	unprocessed := make(mcuProxyConnectionsList, 0, len(connections))
	for _, conn := range connections {
		if country == conn.Country() {
			sorted = append(sorted, conn)
		} else {
			unprocessed = append(unprocessed, conn)
		}
	}
	if continents, found := ContinentMap[country]; found && len(unprocessed) > 1 {
		remaining := make(mcuProxyConnectionsList, 0, len(unprocessed))
		// Map continents to other continents (e.g. use Europe for Africa).
		for _, continent := range continents {
			if toAdd, found := continentMap[continent]; found {
				continents = append(continents, toAdd...)
			}
		}

		// Next up are connections on the same or mapped continent.
		for _, conn := range unprocessed {
			connCountry := conn.Country()
			if IsValidCountry(connCountry) {
				connContinents := ContinentMap[connCountry]
				if ContinentsOverlap(continents, connContinents) {
					sorted = append(sorted, conn)
				} else {
					remaining = append(remaining, conn)
				}
			} else {
				remaining = append(remaining, conn)
			}
		}
		unprocessed = remaining
	}
	// Add all other connections by load.
	sorted = append(sorted, unprocessed...)
	return sorted
}

func (m *mcuProxy) getSortedConnections(initiator McuInitiator) []*mcuProxyConnection {
	m.connectionsMu.RLock()
	connections := m.connections
	m.connectionsMu.RUnlock()
	if len(connections) < 2 {
		return connections
	}

	// Connections are re-sorted every <connectionSortRequests> requests or
	// every <connectionSortInterval>.
	now := time.Now().UnixNano()
	if m.connRequests.Add(1)%connectionSortRequests == 0 || m.nextSort.Load() <= now {
		m.nextSort.Store(now + int64(connectionSortInterval))

		sorted := make(mcuProxyConnectionsList, len(connections))
		copy(sorted, connections)

		sorted.Sort()

		m.connectionsMu.Lock()
		m.connections = sorted
		m.connectionsMu.Unlock()
		connections = sorted
	}

	if initiator != nil {
		if country := initiator.Country(); IsValidCountry(country) {
			connections = sortConnectionsForCountry(connections, country, m.getContinentsMap())
		}
	}
	return connections
}

func (m *mcuProxy) removePublisher(publisher *mcuProxyPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.publishers, publisher.id+"|"+publisher.StreamType())
}

func (m *mcuProxy) NewPublisher(ctx context.Context, listener McuListener, id string, sid string, streamType string, bitrate int, mediaTypes MediaType, initiator McuInitiator) (McuPublisher, error) {
	connections := m.getSortedConnections(initiator)
	for _, conn := range connections {
		if conn.IsShutdownScheduled() || conn.IsTemporary() {
			continue
		}

		subctx, cancel := context.WithTimeout(ctx, m.proxyTimeout)
		defer cancel()

		var maxBitrate int
		if streamType == streamTypeScreen {
			maxBitrate = m.maxScreenBitrate
		} else {
			maxBitrate = m.maxStreamBitrate
		}
		if bitrate <= 0 {
			bitrate = maxBitrate
		} else {
			bitrate = min(bitrate, maxBitrate)
		}
		publisher, err := conn.newPublisher(subctx, listener, id, sid, streamType, bitrate, mediaTypes)
		if err != nil {
			log.Printf("Could not create %s publisher for %s on %s: %s", streamType, id, conn, err)
			continue
		}

		m.mu.Lock()
		m.publishers[id+"|"+streamType] = conn
		m.mu.Unlock()
		m.publisherWaiters.Wakeup()
		return publisher, nil
	}

	statsProxyNobackendAvailableTotal.WithLabelValues(streamType).Inc()
	return nil, fmt.Errorf("No MCU connection available")
}

func (m *mcuProxy) getPublisherConnection(publisher string, streamType string) *mcuProxyConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.publishers[publisher+"|"+streamType]
}

func (m *mcuProxy) waitForPublisherConnection(ctx context.Context, publisher string, streamType string) *mcuProxyConnection {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn := m.publishers[publisher+"|"+streamType]
	if conn != nil {
		// Publisher was created while waiting for lock.
		return conn
	}

	ch := make(chan struct{}, 1)
	id := m.publisherWaiters.Add(ch)
	defer m.publisherWaiters.Remove(id)

	statsWaitingForPublisherTotal.WithLabelValues(streamType).Inc()
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
	if conn := m.getPublisherConnection(publisher, streamType); conn != nil {
		// Fast common path: publisher is available locally.
		conn.publishersLock.Lock()
		id, found := conn.publisherIds[publisher+"|"+streamType]
		conn.publishersLock.Unlock()
		if !found {
			return nil, fmt.Errorf("Unknown publisher %s", publisher)
		}

		return conn.newSubscriber(ctx, listener, id, publisher, streamType)
	}

	log.Printf("No %s publisher %s found yet, deferring", streamType, publisher)
	ch := make(chan McuSubscriber)
	getctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Wait for publisher to be created locally.
	go func() {
		if conn := m.waitForPublisherConnection(getctx, publisher, streamType); conn != nil {
			cancel() // Cancel pending RPC calls.

			conn.publishersLock.Lock()
			id, found := conn.publisherIds[publisher+"|"+streamType]
			conn.publishersLock.Unlock()
			if !found {
				log.Printf("Unknown id for local %s publisher %s", streamType, publisher)
				return
			}

			subscriber, err := conn.newSubscriber(ctx, listener, id, publisher, streamType)
			if subscriber != nil {
				ch <- subscriber
			} else if err != nil {
				log.Printf("Error creating local subscriber for %s publisher %s: %s", streamType, publisher, err)
			}
		}
	}()

	// Wait for publisher to be created on one of the other servers in the cluster.
	if clients := m.rpcClients.GetClients(); len(clients) > 0 {
		for _, client := range clients {
			go func(client *GrpcClient) {
				id, url, ip, err := client.GetPublisherId(getctx, publisher, streamType)
				if errors.Is(err, context.Canceled) {
					return
				} else if err != nil {
					log.Printf("Error getting %s publisher id %s from %s: %s", streamType, publisher, client.Target(), err)
					return
				} else if id == "" {
					// Publisher not found on other server
					return
				}

				cancel() // Cancel pending RPC calls.
				log.Printf("Found publisher id %s through %s on proxy %s", id, client.Target(), url)

				m.connectionsMu.RLock()
				connections := m.connections
				m.connectionsMu.RUnlock()
				var publisherConn *mcuProxyConnection
				for _, conn := range connections {
					if conn.rawUrl != url || !ip.Equal(conn.ip) {
						continue
					}

					// Simple case, signaling server has a connection to the same endpoint
					publisherConn = conn
					break
				}

				if publisherConn == nil {
					publisherConn, err = newMcuProxyConnection(m, url, ip)
					if err != nil {
						log.Printf("Could not create temporary connection to %s for %s publisher %s: %s", url, streamType, publisher, err)
						return
					}

					publisherConn.setTemporary()
					publisherConn.start()
					if err := publisherConn.waitUntilConnected(ctx); err != nil {
						log.Printf("Could not establish new connection to %s: %s", publisherConn, err)
						publisherConn.closeIfEmpty()
						return
					}

					m.connectionsMu.Lock()
					m.connections = append(m.connections, publisherConn)
					conns, found := m.connectionsMap[url]
					if found {
						conns = append(conns, publisherConn)
					} else {
						conns = []*mcuProxyConnection{publisherConn}
					}
					m.connectionsMap[url] = conns
					m.connectionsMu.Unlock()
				}

				subscriber, err := publisherConn.newSubscriber(ctx, listener, id, publisher, streamType)
				if err != nil {
					if publisherConn.IsTemporary() {
						publisherConn.closeIfEmpty()
					}
					log.Printf("Could not create subscriber for %s publisher %s: %s", streamType, publisher, err)
					return
				}

				ch <- subscriber
			}(client)
		}
	}

	select {
	case subscriber := <-ch:
		return subscriber, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("No %s publisher %s found", streamType, publisher)
	}
}
