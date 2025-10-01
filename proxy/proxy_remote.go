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
package main

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

const (
	initialReconnectInterval = 1 * time.Second
	maxReconnectInterval     = 16 * time.Second

	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

var (
	ErrNotConnected = errors.New("not connected") // +checklocksignore: Global readonly variable.
)

type RemoteConnection struct {
	mu  sync.Mutex
	p   *ProxyServer
	url *url.URL
	// +checklocks:mu
	conn      *websocket.Conn
	closeCtx  context.Context
	closeFunc context.CancelFunc // +checklocksignore: Only written to from constructor.

	tokenId   string
	tokenKey  *rsa.PrivateKey
	tlsConfig *tls.Config

	// +checklocks:mu
	connectedSince    time.Time
	reconnectTimer    *time.Timer
	reconnectInterval atomic.Int64

	msgId atomic.Int64
	// +checklocks:mu
	helloMsgId string
	// +checklocks:mu
	sessionId signaling.PublicSessionId
	// +checklocks:mu
	helloReceived bool

	// +checklocks:mu
	pendingMessages []*signaling.ProxyClientMessage
	// +checklocks:mu
	messageCallbacks map[string]chan *signaling.ProxyServerMessage
}

func NewRemoteConnection(p *ProxyServer, proxyUrl string, tokenId string, tokenKey *rsa.PrivateKey, tlsConfig *tls.Config) (*RemoteConnection, error) {
	u, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}

	closeCtx, closeFunc := context.WithCancel(context.Background())

	result := &RemoteConnection{
		p:         p,
		url:       u,
		closeCtx:  closeCtx,
		closeFunc: closeFunc,

		tokenId:   tokenId,
		tokenKey:  tokenKey,
		tlsConfig: tlsConfig,

		reconnectTimer: time.NewTimer(0),

		messageCallbacks: make(map[string]chan *signaling.ProxyServerMessage),
	}
	result.reconnectInterval.Store(int64(initialReconnectInterval))

	go result.writePump()

	return result, nil
}

func (c *RemoteConnection) String() string {
	return c.url.String()
}

func (c *RemoteConnection) SessionId() signaling.PublicSessionId {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionId
}

func (c *RemoteConnection) reconnect() {
	u, err := c.url.Parse("proxy")
	if err != nil {
		log.Printf("Could not resolve url to proxy at %s: %s", c, err)
		c.scheduleReconnect()
		return
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}

	dialer := websocket.Dialer{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: c.tlsConfig,
	}

	conn, _, err := dialer.DialContext(context.TODO(), u.String(), nil)
	if err != nil {
		log.Printf("Error connecting to proxy at %s: %s", c, err)
		c.scheduleReconnect()
		return
	}

	log.Printf("Connected to %s", c)

	c.mu.Lock()
	c.connectedSince = time.Now()
	c.conn = conn
	c.mu.Unlock()

	c.reconnectInterval.Store(int64(initialReconnectInterval))

	if !c.sendReconnectHello() || !c.sendPing() {
		c.scheduleReconnect()
		return
	}

	go c.readPump(conn)
}

func (c *RemoteConnection) sendReconnectHello() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.sendHello(c.closeCtx); err != nil {
		log.Printf("Error sending hello request to proxy at %s: %s", c, err)
		return false
	}

	return true
}

func (c *RemoteConnection) scheduleReconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.scheduleReconnectLocked()
}

// +checklocks:c.mu
func (c *RemoteConnection) scheduleReconnectLocked() {
	if err := c.sendCloseLocked(); err != nil && err != ErrNotConnected {
		log.Printf("Could not send close message to %s: %s", c, err)
	}
	c.closeLocked()

	interval := c.reconnectInterval.Load()
	// Prevent all servers from reconnecting at the same time in case of an
	// interrupted connection to the proxy or a restart.
	jitter := rand.Int64N(interval) - (interval / 2)
	c.reconnectTimer.Reset(time.Duration(interval + jitter))

	interval = min(interval*2, int64(maxReconnectInterval))
	c.reconnectInterval.Store(interval)
}

// +checklocks:c.mu
func (c *RemoteConnection) sendHello(ctx context.Context) error {
	c.helloMsgId = strconv.FormatInt(c.msgId.Add(1), 10)
	msg := &signaling.ProxyClientMessage{
		Id:   c.helloMsgId,
		Type: "hello",
		Hello: &signaling.HelloProxyClientMessage{
			Version: "1.0",
		},
	}
	if sessionId := c.sessionId; sessionId != "" {
		msg.Hello.ResumeId = sessionId
	} else {
		tokenString, err := c.createToken("")
		if err != nil {
			return err
		}

		msg.Hello.Token = tokenString
	}

	return c.sendMessageLocked(ctx, msg)
}

// +checklocks:c.mu
func (c *RemoteConnection) sendCloseLocked() error {
	if c.conn == nil {
		return ErrNotConnected
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	return c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func (c *RemoteConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeLocked()
}

// +checklocks:c.mu
func (c *RemoteConnection) closeLocked() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connectedSince = time.Time{}
	c.helloReceived = false
}

func (c *RemoteConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnectTimer.Stop()
	if c.conn == nil {
		return nil
	}

	if c.closeCtx.Err() != nil {
		// Already closed
		return nil
	}

	c.closeFunc()
	err1 := c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{})
	err2 := c.conn.Close()
	c.conn = nil
	c.connectedSince = time.Time{}
	c.helloReceived = false
	if err1 != nil {
		return err1
	}
	return err2
}

func (c *RemoteConnection) createToken(subject string) (string, error) {
	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now()),
			Issuer:   c.tokenId,
			Subject:  subject,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(c.tokenKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

func (c *RemoteConnection) SendMessage(msg *signaling.ProxyClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sendMessageLocked(c.closeCtx, msg)
}

// +checklocks:c.mu
func (c *RemoteConnection) deferMessage(ctx context.Context, msg *signaling.ProxyClientMessage) {
	c.pendingMessages = append(c.pendingMessages, msg)
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()

			c.mu.Lock()
			defer c.mu.Unlock()
			for idx, m := range c.pendingMessages {
				if m == msg {
					c.pendingMessages[idx] = nil
					break
				}
			}
		}()
	}
}

// +checklocks:c.mu
func (c *RemoteConnection) sendMessageLocked(ctx context.Context, msg *signaling.ProxyClientMessage) error {
	if c.conn == nil {
		// Defer until connected.
		c.deferMessage(ctx, msg)
		return nil
	}

	if c.helloMsgId != "" && c.helloMsgId != msg.Id {
		// Hello request is still inflight, defer.
		c.deferMessage(ctx, msg)
		return nil
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	return c.conn.WriteJSON(msg)
}

func (c *RemoteConnection) readPump(conn *websocket.Conn) {
	defer func() {
		if c.closeCtx.Err() == nil {
			c.scheduleReconnect()
		}
	}()
	defer c.close()

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrCloseSent) {
				break
			} else if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				if !errors.Is(err, net.ErrClosed) || c.closeCtx.Err() == nil {
					log.Printf("Error reading from %s: %v", c, err)
				}
			}
			break
		}

		if msgType != websocket.TextMessage {
			log.Printf("unexpected message type %q (%s)", msgType, string(msg))
			continue
		}

		var message signaling.ProxyServerMessage
		if err := json.Unmarshal(msg, &message); err != nil {
			log.Printf("could not decode message %s: %s", string(msg), err)
			continue
		}

		c.mu.Lock()
		helloMsgId := c.helloMsgId
		c.mu.Unlock()

		if helloMsgId != "" && message.Id == helloMsgId {
			c.processHello(&message)
		} else {
			c.processMessage(&message)
		}
	}
}

func (c *RemoteConnection) sendPing() bool {
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

func (c *RemoteConnection) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()

	defer c.reconnectTimer.Stop()
	for {
		select {
		case <-c.reconnectTimer.C:
			c.reconnect()
		case <-ticker.C:
			c.sendPing()
		case <-c.closeCtx.Done():
			return
		}
	}
}

func (c *RemoteConnection) processHello(msg *signaling.ProxyServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.helloMsgId = ""
	switch msg.Type {
	case "error":
		if msg.Error.Code == "no_such_session" {
			log.Printf("Session %s could not be resumed on %s, registering new", c.sessionId, c)
			c.sessionId = ""
			if err := c.sendHello(c.closeCtx); err != nil {
				log.Printf("Could not send hello request to %s: %s", c, err)
				c.scheduleReconnectLocked()
			}
			return
		}

		log.Printf("Hello connection to %s failed with %+v, reconnecting", c, msg.Error)
		c.scheduleReconnectLocked()
	case "hello":
		resumed := c.sessionId == msg.Hello.SessionId
		c.sessionId = msg.Hello.SessionId
		c.helloReceived = true
		country := ""
		if msg.Hello.Server != nil {
			if country = msg.Hello.Server.Country; country != "" && !signaling.IsValidCountry(country) {
				log.Printf("Proxy %s sent invalid country %s in hello response", c, country)
				country = ""
			}
		}
		if resumed {
			log.Printf("Resumed session %s on %s", c.sessionId, c)
		} else if country != "" {
			log.Printf("Received session %s from %s (in %s)", c.sessionId, c, country)
		} else {
			log.Printf("Received session %s from %s", c.sessionId, c)
		}

		pending := c.pendingMessages
		c.pendingMessages = nil
		for _, m := range pending {
			if m == nil {
				continue
			}

			if err := c.sendMessageLocked(c.closeCtx, m); err != nil {
				log.Printf("Could not send pending message %+v to %s: %s", m, c, err)
			}
		}
	default:
		log.Printf("Received unsupported hello response %+v from %s, reconnecting", msg, c)
		c.scheduleReconnectLocked()
	}
}

func (c *RemoteConnection) handleCallback(msg *signaling.ProxyServerMessage) bool {
	if msg.Id == "" {
		return false
	}

	c.mu.Lock()
	ch, found := c.messageCallbacks[msg.Id]
	if !found {
		c.mu.Unlock()
		return false
	}

	delete(c.messageCallbacks, msg.Id)
	c.mu.Unlock()

	ch <- msg
	return true
}

func (c *RemoteConnection) processMessage(msg *signaling.ProxyServerMessage) {
	if c.handleCallback(msg) {
		return
	}

	switch msg.Type {
	case "event":
		c.processEvent(msg)
	case "bye":
		log.Printf("Connection to %s was closed: %s", c, msg.Bye.Reason)
		if msg.Bye.Reason == "session_expired" {
			// Don't try to resume expired session.
			c.mu.Lock()
			c.sessionId = ""
			c.mu.Unlock()
		}
		c.scheduleReconnect()
	default:
		log.Printf("Received unsupported message %+v from %s", msg, c)
	}
}

func (c *RemoteConnection) processEvent(msg *signaling.ProxyServerMessage) {
	switch msg.Event.Type {
	case "update-load":
		// Ignore
	case "publisher-closed":
		log.Printf("Remote publisher %s was closed on %s", msg.Event.ClientId, c)
		c.p.RemotePublisherDeleted(signaling.PublicSessionId(msg.Event.ClientId))
	default:
		log.Printf("Received unsupported event %+v from %s", msg, c)
	}
}

func (c *RemoteConnection) sendMessageWithCallbackLocked(ctx context.Context, msg *signaling.ProxyClientMessage) (string, <-chan *signaling.ProxyServerMessage, error) {
	msg.Id = strconv.FormatInt(c.msgId.Add(1), 10)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.sendMessageLocked(ctx, msg); err != nil {
		msg.Id = ""
		return "", nil, err
	}

	ch := make(chan *signaling.ProxyServerMessage, 1)
	c.messageCallbacks[msg.Id] = ch
	return msg.Id, ch, nil
}

func (c *RemoteConnection) RequestMessage(ctx context.Context, msg *signaling.ProxyClientMessage) (*signaling.ProxyServerMessage, error) {
	id, ch, err := c.sendMessageWithCallbackLocked(ctx, msg)
	if err != nil {
		return nil, err
	}

	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.messageCallbacks, id)
	}()

	select {
	case <-ctx.Done():
		// TODO: Cancel request.
		return nil, ctx.Err()
	case response := <-ch:
		if response.Type == "error" {
			return nil, response.Error
		}
		return response, nil
	}
}

func (c *RemoteConnection) SendBye() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}

	return c.sendMessageLocked(c.closeCtx, &signaling.ProxyClientMessage{
		Type: "bye",
	})
}
