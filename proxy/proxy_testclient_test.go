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
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

var (
	ErrNoMessageReceived = errors.New("no message was received by the server")
)

type ProxyTestClient struct {
	t       *testing.T
	assert  *assert.Assertions
	require *require.Assertions

	mu            sync.Mutex
	conn          *websocket.Conn
	messageChan   chan []byte
	readErrorChan chan error

	sessionId string
}

func NewProxyTestClient(ctx context.Context, t *testing.T, url string) *ProxyTestClient {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, getWebsocketUrl(url), nil)
	require.NoError(t, err)

	messageChan := make(chan []byte)
	readErrorChan := make(chan error, 1)

	go func() {
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				readErrorChan <- err
				return
			} else if !assert.Equal(t, websocket.TextMessage, messageType) {
				return
			}

			messageChan <- data
		}
	}()

	client := &ProxyTestClient{
		t:       t,
		assert:  assert.New(t),
		require: require.New(t),

		conn:          conn,
		messageChan:   messageChan,
		readErrorChan: readErrorChan,
	}
	return client
}

func (c *ProxyTestClient) CloseWithBye() {
	c.SendBye() // nolint
	c.Close()
}

func (c *ProxyTestClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err == websocket.ErrCloseSent {
		// Already closed
		return
	}

	// Wait a bit for close message to be processed.
	time.Sleep(100 * time.Millisecond)
	c.assert.NoError(c.conn.Close())

	// Drain any entries in the channels to terminate the read goroutine.
loop:
	for {
		select {
		case <-c.readErrorChan:
		case <-c.messageChan:
		default:
			break loop
		}
	}
}

func (c *ProxyTestClient) SendBye() error {
	hello := &signaling.ProxyClientMessage{
		Id:   "9876",
		Type: "bye",
		Bye:  &signaling.ByeProxyClientMessage{},
	}
	return c.WriteJSON(hello)
}

func (c *ProxyTestClient) WriteJSON(data any) error {
	if msg, ok := data.(*signaling.ProxyClientMessage); ok {
		if err := msg.CheckValid(); err != nil {
			return err
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(data)
}

func (c *ProxyTestClient) RunUntilMessage(ctx context.Context) (message *signaling.ProxyServerMessage, err error) {
	select {
	case err = <-c.readErrorChan:
	case msg := <-c.messageChan:
		var m signaling.ProxyServerMessage
		if err = json.Unmarshal(msg, &m); err == nil {
			message = &m
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	return
}

func checkUnexpectedClose(err error) error {
	if err != nil && websocket.IsUnexpectedCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived) {
		return fmt.Errorf("Connection was closed with unexpected error: %s", err)
	}

	return nil
}

func checkMessageType(message *signaling.ProxyServerMessage, expectedType string) error {
	if message == nil {
		return ErrNoMessageReceived
	}

	if message.Type != expectedType {
		return fmt.Errorf("Expected \"%s\" message, got %+v", expectedType, message)
	}
	switch message.Type {
	case "hello":
		if message.Hello == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v", expectedType, message)
		}
	case "command":
		if message.Command == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v", expectedType, message)
		}
	case "event":
		if message.Event == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v", expectedType, message)
		}
	}

	return nil
}

func (c *ProxyTestClient) SendHello(key any) error {
	claims := &signaling.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-maxTokenAge / 2)),
			Issuer:   TokenIdForTest,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(key)
	c.require.NoError(err)

	hello := &signaling.ProxyClientMessage{
		Id:   "1234",
		Type: "hello",
		Hello: &signaling.HelloProxyClientMessage{
			Version:  "1.0",
			Features: []string{},
			Token:    tokenString,
		},
	}
	return c.WriteJSON(hello)
}

func (c *ProxyTestClient) RunUntilHello(ctx context.Context) (message *signaling.ProxyServerMessage, err error) {
	if message, err = c.RunUntilMessage(ctx); err != nil {
		return nil, err
	}
	if err := checkUnexpectedClose(err); err != nil {
		return nil, err
	}
	if err := checkMessageType(message, "hello"); err != nil {
		return nil, err
	}
	c.sessionId = message.Hello.SessionId
	return message, nil
}

func (c *ProxyTestClient) RunUntilLoad(ctx context.Context, load int64) (message *signaling.ProxyServerMessage, err error) {
	if message, err = c.RunUntilMessage(ctx); err != nil {
		return nil, err
	}
	if err := checkUnexpectedClose(err); err != nil {
		return nil, err
	}
	if err := checkMessageType(message, "event"); err != nil {
		return nil, err
	}
	if expectedType := "update-load"; message.Event.Type != expectedType {
		return nil, fmt.Errorf("Expected \"%s\" event message, got %+v", expectedType, message)
	}
	if load != message.Event.Load {
		return nil, fmt.Errorf("Expected load %d, got %+v", load, message)
	}
	return message, nil
}

func (c *ProxyTestClient) SendCommand(command *signaling.CommandProxyClientMessage) error {
	message := &signaling.ProxyClientMessage{
		Id:      "2345",
		Type:    "command",
		Command: command,
	}
	return c.WriteJSON(message)
}
