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
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

var (
	ErrNotConnected = errors.New("not connected")
)

type RemoteConnection struct {
	mu   sync.Mutex
	url  *url.URL
	conn *websocket.Conn

	tokenId   string
	tokenKey  *rsa.PrivateKey
	tlsConfig *tls.Config

	msgId      atomic.Int64
	helloMsgId string
	sessionId  string

	messageCallbacks map[string]chan *signaling.ProxyServerMessage
}

func NewRemoteConnection(proxyUrl string, tokenId string, tokenKey *rsa.PrivateKey, tlsConfig *tls.Config) (*RemoteConnection, error) {
	u, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}

	result := &RemoteConnection{
		url: u,

		tokenId:   tokenId,
		tokenKey:  tokenKey,
		tlsConfig: tlsConfig,

		messageCallbacks: make(map[string]chan *signaling.ProxyServerMessage),
	}
	return result, nil
}

func (c *RemoteConnection) String() string {
	return c.url.String()
}

func (c *RemoteConnection) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	u, err := c.url.Parse("proxy")
	if err != nil {
		return err
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	dialer := websocket.Dialer{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: c.tlsConfig,
	}

	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return err
	}

	c.conn = conn
	go c.readPump()

	return c.sendHello()
}

func (c *RemoteConnection) sendHello() error {
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

	return c.sendMessageLocked(msg)
}

func (c *RemoteConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}

	err1 := c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{})
	err2 := c.conn.Close()
	c.conn = nil
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

	return c.sendMessageLocked(msg)
}

func (c *RemoteConnection) sendMessageLocked(msg *signaling.ProxyClientMessage) error {
	if c.conn == nil {
		return ErrNotConnected
	}

	return c.conn.WriteJSON(msg)
}

func (c *RemoteConnection) readPump() {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}

		msgType, reader, err := conn.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				log.Printf("error reading: %s", err)
			}
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			return
		}

		body, err := io.ReadAll(reader)
		if err != nil {
			log.Printf("error reading message: %s", err)
			continue
		}

		if msgType != websocket.TextMessage {
			log.Printf("unexpected message type %q (%s)", msgType, string(body))
			continue
		}

		var msg signaling.ProxyServerMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			log.Printf("could not decode message %s: %s", string(body), err)
			continue
		}

		c.mu.Lock()
		helloMsgId := c.helloMsgId
		c.mu.Unlock()

		if helloMsgId != "" && msg.Id == helloMsgId {
			c.processHello(&msg)
		} else {
			c.processMessage(&msg)
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
			if err := c.sendHello(); err != nil {
				log.Printf("Could not send hello request to %s: %s", c, err)
				// TODO: c.scheduleReconnect()
			}
			return
		}

		log.Printf("Hello connection to %s failed with %+v, reconnecting", c, msg.Error)
		// TODO: c.scheduleReconnect()
	case "hello":
		resumed := c.sessionId == msg.Hello.SessionId
		c.sessionId = msg.Hello.SessionId
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
	default:
		log.Printf("Received unsupported hello response %+v from %s, reconnecting", msg, c)
		// TODO: c.scheduleReconnect()
	}
}

func (c *RemoteConnection) processMessage(msg *signaling.ProxyServerMessage) {
	if msg.Id != "" {
		c.mu.Lock()
		ch, found := c.messageCallbacks[msg.Id]
		if found {
			delete(c.messageCallbacks, msg.Id)
			c.mu.Unlock()
			ch <- msg
			return
		}
		c.mu.Unlock()
	}

	switch msg.Type {
	case "event":
		c.processEvent(msg)
	default:
		log.Printf("Received unsupported message %+v from %s", msg, c)
	}
}

func (c *RemoteConnection) processEvent(msg *signaling.ProxyServerMessage) {
	switch msg.Event.Type {
	case "update-load":
	default:
		log.Printf("Received unsupported event %+v from %s", msg, c)
	}
}

func (c *RemoteConnection) RequestMessage(ctx context.Context, msg *signaling.ProxyClientMessage) (*signaling.ProxyServerMessage, error) {
	msg.Id = strconv.FormatInt(c.msgId.Add(1), 10)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.sendMessageLocked(msg); err != nil {
		return nil, err
	}
	ch := make(chan *signaling.ProxyServerMessage, 1)
	c.messageCallbacks[msg.Id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.messageCallbacks, msg.Id)
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
