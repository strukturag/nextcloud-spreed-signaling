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
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	"github.com/mailru/easyjson"
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
	noCountry string = "no-country"

	loopback string = "loopback"

	unknownCountry string = "unknown-country"
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

	bufferPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

type WritableClientMessage interface {
	json.Marshaler

	CloseAfterSend(session Session) bool
}

type Client struct {
	conn    *websocket.Conn
	addr    string
	agent   string
	closed  uint32
	country *string

	session unsafe.Pointer

	mu sync.Mutex

	closeChan         chan bool
	messagesDone      sync.WaitGroup
	messageChan       chan *bytes.Buffer
	messageProcessing uint32

	OnLookupCountry   func(*Client) string
	OnClosed          func(*Client)
	OnMessageReceived func(*Client, []byte)
	OnRTTReceived     func(*Client, time.Duration)
}

func NewClient(conn *websocket.Conn, remoteAddress string, agent string) (*Client, error) {
	remoteAddress = strings.TrimSpace(remoteAddress)
	if remoteAddress == "" {
		remoteAddress = "unknown remote address"
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "unknown user agent"
	}
	client := &Client{
		conn:  conn,
		addr:  remoteAddress,
		agent: agent,

		closeChan:   make(chan bool, 1),
		messageChan: make(chan *bytes.Buffer, 16),

		OnLookupCountry:   func(client *Client) string { return unknownCountry },
		OnClosed:          func(client *Client) {},
		OnMessageReceived: func(client *Client, data []byte) {},
		OnRTTReceived:     func(client *Client, rtt time.Duration) {},
	}
	return client, nil
}

func (c *Client) SetConn(conn *websocket.Conn, remoteAddress string) {
	c.conn = conn
	c.addr = remoteAddress
	c.closeChan = make(chan bool, 1)
	c.messageChan = make(chan *bytes.Buffer, 16)
	c.OnLookupCountry = func(client *Client) string { return unknownCountry }
	c.OnClosed = func(client *Client) {}
	c.OnMessageReceived = func(client *Client, data []byte) {}
}

func (c *Client) IsConnected() bool {
	return atomic.LoadUint32(&c.closed) == 0
}

func (c *Client) IsAuthenticated() bool {
	return c.GetSession() != nil
}

func (c *Client) GetSession() *ClientSession {
	return (*ClientSession)(atomic.LoadPointer(&c.session))
}

func (c *Client) SetSession(session *ClientSession) {
	atomic.StorePointer(&c.session, unsafe.Pointer(session))
}

func (c *Client) RemoteAddr() string {
	return c.addr
}

func (c *Client) UserAgent() string {
	return c.agent
}

func (c *Client) Country() string {
	if c.country == nil {
		country := c.OnLookupCountry(c)
		c.country = &country
	}

	return *c.country
}

func (c *Client) Close() {
	if !atomic.CompareAndSwapUint32(&c.closed, 0, 1) {
		return
	}

	c.mu.Lock()
	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // nolint
	}
	c.mu.Unlock()

	if atomic.LoadUint32(&c.messageProcessing) == 1 {
		// Defer closing
		atomic.StoreUint32(&c.closed, 2)
		return
	}

	c.doClose()
}

func (c *Client) doClose() {
	c.closeChan <- true
	c.messagesDone.Wait()

	c.OnClosed(c)
	c.SetSession(nil)

	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
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
		Bye:  &ByeServerMessage{},
	}
	if message != nil {
		response.Id = message.Id
	}
	if reason != "" {
		response.Bye.Reason = reason
	}
	return c.SendMessage(response)
}

func (c *Client) SendMessage(message WritableClientMessage) bool {
	return c.writeMessage(message)
}

func (c *Client) ReadPump() {
	defer func() {
		c.Close()
		close(c.messageChan)
	}()

	addr := c.RemoteAddr()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		log.Printf("Connection from %s closed while starting readPump", addr)
		return
	}

	conn.SetReadLimit(maxMessageSize)
	conn.SetPongHandler(func(msg string) error {
		now := time.Now()
		conn.SetReadDeadline(now.Add(pongWait)) // nolint
		if msg == "" {
			return nil
		}
		if ts, err := strconv.ParseInt(msg, 10, 64); err == nil {
			rtt := now.Sub(time.Unix(0, ts))
			rtt_ms := rtt.Nanoseconds() / time.Millisecond.Nanoseconds()
			if session := c.GetSession(); session != nil {
				log.Printf("Client %s has RTT of %d ms (%s)", session.PublicId(), rtt_ms, rtt)
			} else {
				log.Printf("Client from %s has RTT of %d ms (%s)", addr, rtt_ms, rtt)
			}
			c.OnRTTReceived(c, rtt)
		}
		return nil
	})

	go c.processMessages()

	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		messageType, reader, err := conn.NextReader()
		if err != nil {
			if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				if session := c.GetSession(); session != nil {
					log.Printf("Error reading from client %s: %v", session.PublicId(), err)
				} else {
					log.Printf("Error reading from %s: %v", addr, err)
				}
			}
			break
		}

		if messageType != websocket.TextMessage {
			if session := c.GetSession(); session != nil {
				log.Printf("Unsupported message type %v from client %s", messageType, session.PublicId())
			} else {
				log.Printf("Unsupported message type %v from %s", messageType, addr)
			}
			c.SendError(InvalidFormat)
			continue
		}

		decodeBuffer := bufferPool.Get().(*bytes.Buffer)
		decodeBuffer.Reset()
		if _, err := decodeBuffer.ReadFrom(reader); err != nil {
			bufferPool.Put(decodeBuffer)
			if session := c.GetSession(); session != nil {
				log.Printf("Error reading message from client %s: %v", session.PublicId(), err)
			} else {
				log.Printf("Error reading message from %s: %v", addr, err)
			}
			break
		}

		// Stop processing if the client was closed.
		if atomic.LoadUint32(&c.closed) != 0 {
			bufferPool.Put(decodeBuffer)
			break
		}

		c.messagesDone.Add(1)
		c.messageChan <- decodeBuffer
	}
}

func (c *Client) processMessages() {
	for {
		buffer := <-c.messageChan
		if buffer == nil {
			break
		}

		atomic.StoreUint32(&c.messageProcessing, 1)
		c.OnMessageReceived(c, buffer.Bytes())
		atomic.StoreUint32(&c.messageProcessing, 0)
		c.messagesDone.Done()
		bufferPool.Put(buffer)
	}

	if atomic.LoadUint32(&c.closed) == 2 {
		c.doClose()
	}
}

func (c *Client) writeInternal(message json.Marshaler) bool {
	var closeData []byte

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	writer, err := c.conn.NextWriter(websocket.TextMessage)
	if err == nil {
		if m, ok := (interface{}(message)).(easyjson.Marshaler); ok {
			_, err = easyjson.MarshalToWriter(m, writer)
		} else {
			err = json.NewEncoder(writer).Encode(message)
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

		if session := c.GetSession(); session != nil {
			log.Printf("Could not send message %+v to client %s: %v", message, session.PublicId(), err)
		} else {
			log.Printf("Could not send message %+v to %s: %v", message, c.RemoteAddr(), err)
		}
		closeData = websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "")
		goto close
	}
	return true

close:
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.CloseMessage, closeData); err != nil {
		if session := c.GetSession(); session != nil {
			log.Printf("Could not send close message to client %s: %v", session.PublicId(), err)
		} else {
			log.Printf("Could not send close message to %s: %v", c.RemoteAddr(), err)
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
		if session := c.GetSession(); session != nil {
			log.Printf("Could not send close message to client %s: %v", session.PublicId(), err)
		} else {
			log.Printf("Could not send close message to %s: %v", c.RemoteAddr(), err)
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
		return false
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
		if session := c.GetSession(); session != nil {
			log.Printf("Could not send ping to client %s: %v", session.PublicId(), err)
		} else {
			log.Printf("Could not send ping to %s: %v", c.RemoteAddr(), err)
		}
		return false
	}

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
		case <-c.closeChan:
			return
		}
	}
}
