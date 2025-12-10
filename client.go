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
package signaling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mailru/easyjson"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 64 * 1024
)

var (
	noCountry = "no-country"

	loopback = "loopback"

	unknownCountry = "unknown-country"
)

func init() {
	RegisterClientStats()
}

func IsValidCountry(country string) bool {
	switch country {
	case "":
		fallthrough
	case noCountry:
		fallthrough
	case loopback:
		fallthrough
	case unknownCountry:
		return false
	default:
		return true
	}
}

var (
	InvalidFormat = NewError("invalid_format", "Invalid data format.")

	bufferPool BufferPool
)

type WritableClientMessage interface {
	json.Marshaler

	CloseAfterSend(session Session) bool
}

type HandlerClient interface {
	Context() context.Context
	RemoteAddr() string
	Country() string
	UserAgent() string
	IsConnected() bool
	IsAuthenticated() bool

	GetSession() Session
	SetSession(session Session)

	SendError(e *Error) bool
	SendByeResponse(message *ClientMessage) bool
	SendByeResponseWithReason(message *ClientMessage, reason string) bool
	SendMessage(message WritableClientMessage) bool

	Close()
}

type ClientHandler interface {
	OnClosed(HandlerClient)
	OnMessageReceived(HandlerClient, []byte)
	OnRTTReceived(HandlerClient, time.Duration)
}

type ClientGeoIpHandler interface {
	OnLookupCountry(HandlerClient) string
}

type Client struct {
	logger  log.Logger
	ctx     context.Context
	conn    *websocket.Conn
	addr    string
	agent   string
	closed  atomic.Int32
	country *string
	logRTT  bool

	handlerMu sync.RWMutex
	// +checklocks:handlerMu
	handler ClientHandler

	session   atomic.Pointer[Session]
	sessionId atomic.Pointer[PublicSessionId]

	mu sync.Mutex

	closer       *Closer
	closeOnce    sync.Once
	messagesDone chan struct{}
	messageChan  chan *bytes.Buffer
}

func NewClient(ctx context.Context, conn *websocket.Conn, remoteAddress string, agent string, handler ClientHandler) (*Client, error) {
	remoteAddress = strings.TrimSpace(remoteAddress)
	if remoteAddress == "" {
		remoteAddress = "unknown remote address"
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "unknown user agent"
	}

	client := &Client{
		agent:  agent,
		logRTT: true,
	}
	client.SetConn(ctx, conn, remoteAddress, handler)
	return client, nil
}

func (c *Client) SetConn(ctx context.Context, conn *websocket.Conn, remoteAddress string, handler ClientHandler) {
	c.logger = log.LoggerFromContext(ctx)
	c.ctx = ctx
	c.conn = conn
	c.addr = remoteAddress
	c.SetHandler(handler)
	c.closer = NewCloser()
	c.messageChan = make(chan *bytes.Buffer, 16)
	c.messagesDone = make(chan struct{})
}

func (c *Client) SetHandler(handler ClientHandler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handler = handler
}

func (c *Client) getHandler() ClientHandler {
	c.handlerMu.RLock()
	defer c.handlerMu.RUnlock()
	return c.handler
}

func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) IsConnected() bool {
	return c.closed.Load() == 0
}

func (c *Client) IsAuthenticated() bool {
	return c.GetSession() != nil
}

func (c *Client) GetSession() Session {
	session := c.session.Load()
	if session == nil {
		return nil
	}

	return *session
}

func (c *Client) SetSession(session Session) {
	if session == nil {
		c.session.Store(nil)
	} else {
		c.session.Store(&session)
	}
}

func (c *Client) SetSessionId(sessionId PublicSessionId) {
	c.sessionId.Store(&sessionId)
}

func (c *Client) GetSessionId() PublicSessionId {
	sessionId := c.sessionId.Load()
	if sessionId == nil {
		session := c.GetSession()
		if session == nil {
			return ""
		}

		return session.PublicId()
	}

	return *sessionId
}

func (c *Client) RemoteAddr() string {
	return c.addr
}

func (c *Client) UserAgent() string {
	return c.agent
}

func (c *Client) Country() string {
	if c.country == nil {
		var country string
		if handler, ok := c.getHandler().(ClientGeoIpHandler); ok {
			country = handler.OnLookupCountry(c)
		} else {
			country = unknownCountry
		}
		c.country = &country
	}

	return *c.country
}

func (c *Client) Close() {
	if c.closed.Load() >= 2 {
		// Prevent reentrant call in case this was the second closing
		// step. Would otherwise deadlock in the "Once.Do" call path
		// through "Hub.processUnregister" (which calls "Close" again).
		return
	}

	c.closeOnce.Do(func() {
		c.doClose()
	})
}

func (c *Client) doClose() {
	closed := c.closed.Add(1)
	switch closed {
	case 1:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.conn != nil {
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // nolint
			c.conn.Close()
			c.conn = nil
		}
	case 2:
		// Both the read pump and message processing must be finished before closing.
		c.closer.Close()
		<-c.messagesDone

		c.getHandler().OnClosed(c)
		c.SetSession(nil)
	}
}

func (c *Client) SendError(e *Error) bool {
	message := &ServerMessage{
		Type:  "error",
		Error: e,
	}
	return c.SendMessage(message)
}

func (c *Client) SendByeResponse(message *ClientMessage) bool {
	return c.SendByeResponseWithReason(message, "")
}

func (c *Client) SendByeResponseWithReason(message *ClientMessage, reason string) bool {
	response := &ServerMessage{
		Type: "bye",
	}
	if message != nil {
		response.Id = message.Id
	}
	if reason != "" {
		if response.Bye == nil {
			response.Bye = &ByeServerMessage{}
		}
		response.Bye.Reason = reason
	}
	return c.SendMessage(response)
}

func (c *Client) SendMessage(message WritableClientMessage) bool {
	return c.writeMessage(message)
}

func (c *Client) ReadPump() {
	defer func() {
		close(c.messageChan)
		c.Close()
	}()

	go c.processMessages()

	addr := c.RemoteAddr()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		c.logger.Printf("Connection from %s closed while starting readPump", addr)
		return
	}

	conn.SetReadLimit(maxMessageSize)
	conn.SetPongHandler(func(msg string) error {
		now := time.Now()
		conn.SetReadDeadline(now.Add(pongWait)) // nolint
		if msg == "" {
			return nil
		}
		statsClientBytesTotal.WithLabelValues("incoming").Add(float64(len(msg)))
		if ts, err := strconv.ParseInt(msg, 10, 64); err == nil {
			rtt := now.Sub(time.Unix(0, ts))
			if c.logRTT {
				rtt_ms := rtt.Nanoseconds() / time.Millisecond.Nanoseconds()
				if sessionId := c.GetSessionId(); sessionId != "" {
					c.logger.Printf("Client %s has RTT of %d ms (%s)", sessionId, rtt_ms, rtt)
				} else {
					c.logger.Printf("Client from %s has RTT of %d ms (%s)", addr, rtt_ms, rtt)
				}
			}
			statsClientRTT.Observe(float64(rtt.Milliseconds()))
			c.getHandler().OnRTTReceived(c, rtt)
		}
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		messageType, reader, err := conn.NextReader()
		if err != nil {
			// Gorilla websocket hides the original net.Error, so also compare error messages
			if errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) || strings.Contains(err.Error(), net.ErrClosed.Error()) {
				break
			} else if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				if sessionId := c.GetSessionId(); sessionId != "" {
					c.logger.Printf("Error reading from client %s: %v", sessionId, err)
				} else {
					c.logger.Printf("Error reading from %s: %v", addr, err)
				}
			}
			break
		}

		if messageType != websocket.TextMessage {
			if sessionId := c.GetSessionId(); sessionId != "" {
				c.logger.Printf("Unsupported message type %v from client %s", messageType, sessionId)
			} else {
				c.logger.Printf("Unsupported message type %v from %s", messageType, addr)
			}
			c.SendError(InvalidFormat)
			continue
		}

		decodeBuffer, err := bufferPool.ReadAll(reader)
		if err != nil {
			if sessionId := c.GetSessionId(); sessionId != "" {
				c.logger.Printf("Error reading message from client %s: %v", sessionId, err)
			} else {
				c.logger.Printf("Error reading message from %s: %v", addr, err)
			}
			break
		}

		// Stop processing if the client was closed.
		if !c.IsConnected() {
			bufferPool.Put(decodeBuffer)
			break
		}

		statsClientBytesTotal.WithLabelValues("incoming").Add(float64(decodeBuffer.Len()))
		statsClientMessagesTotal.WithLabelValues("incoming").Inc()
		c.messageChan <- decodeBuffer
	}
}

func (c *Client) processMessages() {
	for {
		buffer := <-c.messageChan
		if buffer == nil {
			break
		}

		c.getHandler().OnMessageReceived(c, buffer.Bytes())
		bufferPool.Put(buffer)
	}

	close(c.messagesDone)
	c.doClose()
}

type counterWriter struct {
	w       io.Writer
	counter *int
}

func (w *counterWriter) Write(p []byte) (int, error) {
	written, err := w.w.Write(p)
	if written > 0 {
		*w.counter += written
	}
	return written, err
}

func (c *Client) writeInternal(message json.Marshaler) bool {
	var closeData []byte

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	writer, err := c.conn.NextWriter(websocket.TextMessage)
	var written int
	if err == nil {
		if m, ok := (any(message)).(easyjson.Marshaler); ok {
			written, err = easyjson.MarshalToWriter(m, writer)
		} else {
			err = json.NewEncoder(&counterWriter{
				w:       writer,
				counter: &written,
			}).Encode(message)
		}
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		if err == websocket.ErrCloseSent {
			// Already sent a "close", won't be able to send anything else.
			return false
		}

		if sessionId := c.GetSessionId(); sessionId != "" {
			c.logger.Printf("Could not send message %+v to client %s: %v", message, sessionId, err)
		} else {
			c.logger.Printf("Could not send message %+v to %s: %v", message, c.RemoteAddr(), err)
		}
		closeData = websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "")
		goto close
	}

	statsClientBytesTotal.WithLabelValues("outgoing").Add(float64(written))
	statsClientMessagesTotal.WithLabelValues("outgoing").Inc()
	return true

close:
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.CloseMessage, closeData); err != nil {
		if sessionId := c.GetSessionId(); sessionId != "" {
			c.logger.Printf("Could not send close message to client %s: %v", sessionId, err)
		} else {
			c.logger.Printf("Could not send close message to %s: %v", c.RemoteAddr(), err)
		}
	}
	return false
}

func (c *Client) writeError(e error) bool { // nolint
	message := &ServerMessage{
		Type:  "error",
		Error: NewError("internal_error", e.Error()),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}

	if !c.writeMessageLocked(message) {
		return false
	}

	closeData := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, e.Error())
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.CloseMessage, closeData); err != nil {
		if sessionId := c.GetSessionId(); sessionId != "" {
			c.logger.Printf("Could not send close message to client %s: %v", sessionId, err)
		} else {
			c.logger.Printf("Could not send close message to %s: %v", c.RemoteAddr(), err)
		}
	}
	return false
}

func (c *Client) writeMessage(message WritableClientMessage) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}

	return c.writeMessageLocked(message)
}

func (c *Client) writeMessageLocked(message WritableClientMessage) bool {
	if !c.writeInternal(message) {
		return false
	}

	session := c.GetSession()
	if message.CloseAfterSend(session) {
		c.conn.SetWriteDeadline(time.Now().Add(writeWait))    // nolint
		c.conn.WriteMessage(websocket.CloseMessage, []byte{}) // nolint
		if session != nil {
			go session.Close()
		}
		go c.Close()
	}

	return true
}

func (c *Client) sendPing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}

	now := time.Now().UnixNano()
	msg := strconv.FormatInt(now, 10)
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		if sessionId := c.GetSessionId(); sessionId != "" {
			c.logger.Printf("Could not send ping to client %s: %v", sessionId, err)
		} else {
			c.logger.Printf("Could not send ping to %s: %v", c.RemoteAddr(), err)
		}
		return false
	}

	statsClientBytesTotal.WithLabelValues("outgoing").Add(float64(len(msg)))
	return true
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()

	// Fetch initial RTT before any messages have been sent to the client.
	c.sendPing()
	for {
		select {
		case <-ticker.C:
			if !c.sendPing() {
				return
			}
		case <-c.closer.C:
			return
		}
	}
}
