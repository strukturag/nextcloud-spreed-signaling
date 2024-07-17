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
package signaling

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	easyjson "github.com/mailru/easyjson"
)

var (
	ErrFederationNotSupported = NewError("federation_unsupported", "The target server does not support federation.")
)

type FederationClient struct {
	session *ClientSession
	message atomic.Pointer[ClientMessage]

	roomId        string
	roomSessionId string
	federation    *RoomFederationMessage

	mu     sync.Mutex
	conn   *websocket.Conn
	closer *Closer

	helloMu    sync.Mutex
	helloMsgId string
	helloAuth  *FederationAuthParams
	hello      atomic.Pointer[HelloServerMessage]
}

func NewFederationClient(ctx context.Context, hub *Hub, session *ClientSession, message *ClientMessage) (*FederationClient, error) {
	if message.Type != "room" || message.Room == nil {
		return nil, fmt.Errorf("expected room message, got %+v", message)
	}

	var dialer websocket.Dialer
	dialer.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}

	room := message.Room
	u := *room.Federation.parsedSignalingUrl
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	conn, response, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, err
	}

	features := strings.Split(response.Header.Get("X-Spreed-Signaling-Features"), ",")
	supportsFederation := false
	for _, f := range features {
		f = strings.TrimSpace(f)
		if f == ServerFeatureFederation {
			supportsFederation = true
			break
		}
	}
	if !supportsFederation {
		if err := conn.Close(); err != nil {
			log.Printf("Error closing federation connection to %s: %s", room.Federation.parsedSignalingUrl.String(), err)
		}

		return nil, ErrFederationNotSupported
	}

	result := &FederationClient{
		session: session,

		roomId:        room.RoomId,
		roomSessionId: room.SessionId,
		federation:    room.Federation,

		conn:   conn,
		closer: NewCloser(),
	}
	result.message.Store(message)
	log.Printf("Creating federation connection to %s for %s", result.URL(), result.session.PublicId())

	go func() {
		hub.readPumpActive.Add(1)
		defer hub.readPumpActive.Add(-1)

		result.readPump()
	}()

	go func() {
		hub.writePumpActive.Add(1)
		defer hub.writePumpActive.Add(-1)

		result.writePump()
	}()

	return result, nil
}

func (c *FederationClient) URL() string {
	return c.federation.parsedSignalingUrl.String()
}

func (c *FederationClient) Close() {
	c.closer.Close()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}

	if err := c.sendMessageLocked(&ClientMessage{
		Type: "bye",
	}); err != nil {
		log.Printf("Error sending bye on federation connection to %s: %s", c.URL(), err)
	}

	if err := c.conn.Close(); err != nil {
		log.Printf("Error closing federation connection to %s: %s", c.URL(), err)
	}

	c.conn = nil
}

func (c *FederationClient) readPump() {
	defer func() {
		c.Close()
	}()

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		log.Printf("Connection to %s closed while starting readPump", c.URL())
		return
	}

	conn.SetReadLimit(maxMessageSize)
	conn.SetPongHandler(func(msg string) error {
		now := time.Now()
		conn.SetReadDeadline(now.Add(pongWait)) // nolint
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Error reading: %s", err)
			break
		}

		if msgType != websocket.TextMessage {
			continue
		}

		var msg ServerMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Error unmarshalling %s from %s: %s", string(data), c.URL(), err)
			continue
		}

		if c.hello.Load() == nil {
			switch msg.Type {
			case "welcome":
				c.processWelcome(&msg)
			default:
				c.processHello(&msg)
			}
			continue
		}

		c.processMessage(&msg)
	}
}

func (c *FederationClient) sendPing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}

	now := time.Now().UnixNano()
	msg := strconv.FormatInt(now, 10)
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		log.Printf("Could not send ping to federated client %s: %v", c.session.PublicId(), err)
		return false
	}

	return true
}

func (c *FederationClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

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

func (c *FederationClient) closeWithError(err error) {
	c.Close()
	var e *Error
	if !errors.As(err, &e) {
		e = NewError("federation_error", err.Error())
	}

	var id string
	if message := c.message.Swap(nil); message != nil {
		id = message.Id
	}

	c.session.SendMessage(&ServerMessage{
		Id:    id,
		Type:  "error",
		Error: e,
	})
}

func (c *FederationClient) sendHello(auth *FederationAuthParams) error {
	c.helloMu.Lock()
	defer c.helloMu.Unlock()

	return c.sendHelloLocked(auth)
}

func (c *FederationClient) sendHelloLocked(auth *FederationAuthParams) error {
	c.helloMsgId = newRandomString(8)

	authData, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("Error marshalling hello auth message %+v for %s: %s", auth, c.session.PublicId(), err)
	}

	c.helloAuth = auth
	return c.SendMessage(&ClientMessage{
		Id:   c.helloMsgId,
		Type: "hello",
		Hello: &HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Type:   HelloClientTypeFederation,
				Url:    c.federation.NextcloudUrl,
				Params: authData,
			},
		},
	})
}

func (c *FederationClient) processWelcome(msg *ServerMessage) {
	if !msg.Welcome.HasFeature(ServerFeatureFederation) {
		c.closeWithError(ErrFederationNotSupported)
		return
	}

	federationParams := &FederationAuthParams{
		Token: c.federation.Token,
	}
	if err := c.sendHello(federationParams); err != nil {
		log.Printf("Error sending hello message to %s for %s: %s", c.URL(), c.session.PublicId(), err)
		c.closeWithError(err)
	}
}

func (c *FederationClient) processHello(msg *ServerMessage) {
	c.helloMu.Lock()
	defer c.helloMu.Unlock()

	if msg.Id != c.helloMsgId {
		log.Printf("Received hello response %+v for unknown request, expected %s", msg, c.helloMsgId)
		c.sendHelloLocked(c.helloAuth)
		return
	}

	c.helloMsgId = ""
	if msg.Type == "error" {
		c.closeWithError(msg.Error)
		return
	} else if msg.Type != "hello" {
		log.Printf("Received unknown hello response %+v", msg)
		c.sendHelloLocked(c.helloAuth)
		return
	}

	c.hello.Store(msg.Hello)
	if err := c.joinRoom(); err != nil {
		c.closeWithError(err)
	}
}

func (c *FederationClient) joinRoom() error {
	var id string
	if message := c.message.Swap(nil); message != nil {
		id = message.Id
	}
	return c.SendMessage(&ClientMessage{
		Id:   id,
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    c.roomId,
			SessionId: c.roomSessionId,
		},
	})
}

func (c *FederationClient) updateEventUsers(users []map[string]interface{}, localSessionId string, remoteSessionId string) {
	for _, u := range users {
		key := "sessionId"
		sid, found := u[key]
		if !found {
			key := "sessionid"
			sid, found = u[key]
		}
		if found {
			if sid, ok := sid.(string); ok && sid == remoteSessionId {
				u[key] = localSessionId
				break
			}
		}
	}
}

func (c *FederationClient) updateRecipient(recipient *MessageClientMessageRecipient, localSessionId string, remoteSessionId string) {
	if recipient != nil && recipient.Type == RecipientTypeSession && remoteSessionId != "" && recipient.SessionId == remoteSessionId {
		recipient.SessionId = localSessionId
	}
}

func (c *FederationClient) updateSender(sender *MessageServerMessageSender, localSessionId string, remoteSessionId string) {
	if sender != nil && sender.Type == RecipientTypeSession && remoteSessionId != "" && sender.SessionId == remoteSessionId {
		sender.SessionId = localSessionId
	}
}

func (c *FederationClient) processMessage(msg *ServerMessage) {
	localSessionId := c.session.PublicId()
	var remoteSessionId string
	if hello := c.hello.Load(); hello != nil {
		remoteSessionId = hello.SessionId
	}
	switch msg.Type {
	case "control":
		c.updateRecipient(msg.Control.Recipient, localSessionId, remoteSessionId)
		c.updateSender(msg.Control.Sender, localSessionId, remoteSessionId)
	case "event":
		switch msg.Event.Target {
		case "participants":
			switch msg.Event.Type {
			case "update":
				if remoteSessionId != "" {
					c.updateEventUsers(msg.Event.Update.Changed, localSessionId, remoteSessionId)
					c.updateEventUsers(msg.Event.Update.Users, localSessionId, remoteSessionId)
				}
			case "flags":
				if remoteSessionId != "" && msg.Event.Flags.SessionId == remoteSessionId {
					msg.Event.Flags.SessionId = localSessionId
				}
			}
		case "room":
			switch msg.Event.Type {
			case "join":
				if remoteSessionId != "" {
					for _, j := range msg.Event.Join {
						if j.SessionId == remoteSessionId {
							j.SessionId = localSessionId
							break
						}
					}
				}
			case "leave":
				if remoteSessionId != "" {
					for idx, j := range msg.Event.Leave {
						if j == remoteSessionId {
							msg.Event.Leave[idx] = localSessionId
							break
						}
					}
				}
			}
		}
	case "message":
		c.updateRecipient(msg.Message.Recipient, localSessionId, remoteSessionId)
		c.updateSender(msg.Message.Sender, localSessionId, remoteSessionId)
		if remoteSessionId != "" && len(msg.Message.Data) > 0 {
			var ao AnswerOfferMessage
			if json.Unmarshal(msg.Message.Data, &ao) == nil && (ao.Type == "offer" || ao.Type == "answer") {
				changed := false
				if ao.From == remoteSessionId {
					ao.From = localSessionId
					changed = true
				}
				if ao.To == remoteSessionId {
					ao.To = localSessionId
					changed = true
				}

				if changed {
					if data, err := json.Marshal(ao); err == nil {
						msg.Message.Data = data
					}
				}
			}
		}
	}
	c.session.SendMessage(msg)
}

func (c *FederationClient) ProxyMessage(message *ClientMessage) error {
	switch message.Type {
	case "message":
		if r := message.Message.Recipient; r.Type == RecipientTypeSession && r.SessionId == c.session.PublicId() {
			message.Message.Recipient.SessionId = c.hello.Load().SessionId
		}
	}

	return c.SendMessage(message)
}

func (c *FederationClient) SendMessage(message *ClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sendMessageLocked(message)
}

func (c *FederationClient) sendMessageLocked(message *ClientMessage) error {
	if c.conn == nil {
		return ErrNotConnected
	}

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
			return err
		}

		log.Printf("Could not send message %+v for %s to federated client %s: %v", message, c.session.PublicId(), c.URL(), err)
		closeData := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "")
		c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
		if err := c.conn.WriteMessage(websocket.CloseMessage, closeData); err != nil {
			log.Printf("Could not send close message for %s to federated client %s: %v", c.session.PublicId(), c.URL(), err)
		}
		return err
	}

	return nil
}
