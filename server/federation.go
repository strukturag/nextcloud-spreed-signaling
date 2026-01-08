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
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mailru/easyjson"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
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

	initialFederationReconnectInterval = 100 * time.Millisecond
	maxFederationReconnectInterval     = 8 * time.Second
)

var (
	ErrNotConnected           = errors.New("not connected")
	ErrFederationNotSupported = api.NewError("federation_unsupported", "The target server does not support federation.")

	federationWriteBufferPool = &sync.Pool{}
)

func isClosedError(err error) bool {
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, websocket.ErrCloseSent) ||
		// Gorilla websocket hides the original net.Error, so also compare error messages
		strings.Contains(err.Error(), net.ErrClosed.Error())
}

func getCloudUrlWithoutPath(s string) string {
	if pos := strings.Index(s, "/ocs/v"); pos != -1 {
		s = s[:pos]
	} else {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

func getCloudUrl(s string) string {
	var found bool
	if s, found = strings.CutPrefix(s, "https://"); !found {
		s = strings.TrimPrefix(s, "http://")
	}
	return getCloudUrlWithoutPath(s)
}

type FederationClient struct {
	logger  log.Logger
	hub     *Hub
	session *ClientSession
	message atomic.Pointer[api.ClientMessage]

	roomId       atomic.Value
	remoteRoomId atomic.Value
	changeRoomId atomic.Bool
	federation   atomic.Pointer[api.RoomFederationMessage]

	mu     sync.Mutex
	dialer *websocket.Dialer
	url    string
	// +checklocks:mu
	conn   *websocket.Conn
	closer *internal.Closer
	// +checklocks:mu
	reconnectDelay time.Duration
	reconnecting   atomic.Bool
	// +checklocks:mu
	reconnectFunc *time.Timer

	helloMu sync.Mutex
	// +checklocks:helloMu
	helloMsgId string
	// +checklocks:helloMu
	helloAuth *api.FederationAuthParams
	// +checklocks:helloMu
	resumeId api.PrivateSessionId
	hello    atomic.Pointer[api.HelloServerMessage]

	// +checklocks:helloMu
	pendingMessages []*api.ClientMessage

	closeOnLeave atomic.Bool
}

func NewFederationClient(ctx context.Context, hub *Hub, session *ClientSession, message *api.ClientMessage) (*FederationClient, error) {
	if message.Type != "room" || message.Room == nil || message.Room.Federation == nil {
		return nil, fmt.Errorf("expected federation room message, got %+v", message)
	}

	dialer := &websocket.Dialer{
		WriteBufferPool: federationWriteBufferPool,
	}
	if hub.skipFederationVerify {
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	room := message.Room
	u := *room.Federation.ParsedSignalingUrl
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	url := u.String() + "spreed"

	remoteRoomId := room.Federation.RoomId
	if remoteRoomId == "" {
		remoteRoomId = room.RoomId
	}

	result := &FederationClient{
		logger:  hub.logger,
		hub:     hub,
		session: session,

		reconnectDelay: initialFederationReconnectInterval,

		dialer: dialer,
		url:    url,
		closer: internal.NewCloser(),
	}
	result.roomId.Store(room.RoomId)
	result.remoteRoomId.Store(remoteRoomId)
	result.changeRoomId.Store(room.RoomId != remoteRoomId)
	result.federation.Store(room.Federation)
	result.message.Store(message)

	if err := result.connect(ctx); err != nil {
		return nil, err
	}

	go func() {
		hub.writePumpActive.Add(1)
		defer hub.writePumpActive.Add(-1)

		result.writePump()
	}()

	return result, nil
}

func (c *FederationClient) LocalAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	return c.conn.LocalAddr()
}

func (c *FederationClient) URL() string {
	return c.federation.Load().ParsedSignalingUrl.String()
}

func (c *FederationClient) RoomId() string {
	return c.roomId.Load().(string)
}

func (c *FederationClient) RemoteRoomId() string {
	return c.remoteRoomId.Load().(string)
}

func (c *FederationClient) CanReuse(federation *api.RoomFederationMessage) bool {
	fed := c.federation.Load()
	return fed.NextcloudUrl == federation.NextcloudUrl &&
		fed.SignalingUrl == federation.SignalingUrl
}

func (c *FederationClient) connect(ctx context.Context) error {
	c.logger.Printf("Creating federation connection to %s for %s", c.URL(), c.session.PublicId())
	conn, response, err := c.dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return err
	}

	features := strings.Split(response.Header.Get("X-Spreed-Signaling-Features"), ",")
	supportsFederation := false
	for _, f := range features {
		f = strings.TrimSpace(f)
		if f == api.ServerFeatureFederation {
			supportsFederation = true
			break
		}
	}
	if !supportsFederation {
		if err := conn.Close(); err != nil {
			c.logger.Printf("Error closing federation connection to %s: %s", c.URL(), err)
		}

		return ErrFederationNotSupported
	}

	c.logger.Printf("Federation connection established to %s for %s", c.URL(), c.session.PublicId())

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reconnectFunc != nil {
		c.reconnectFunc.Stop()
		c.reconnectFunc = nil
	}

	c.conn = conn

	go func() {
		c.hub.readPumpActive.Add(1)
		defer c.hub.readPumpActive.Add(-1)

		c.readPump(conn)
	}()

	return nil
}

func (c *FederationClient) ChangeRoom(message *api.ClientMessage) error {
	if message.Room == nil || message.Room.Federation == nil {
		return fmt.Errorf("expected federation room message, got %+v", message)
	} else if !c.CanReuse(message.Room.Federation) {
		return fmt.Errorf("can't reuse federation client to join room in %+v", message)
	}

	c.message.Swap(message)
	return c.joinRoom()
}

func (c *FederationClient) Leave(message *api.ClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if message == nil {
		message = &api.ClientMessage{
			Type: "room",
			Room: &api.RoomClientMessage{
				RoomId: "",
			},
		}
	}

	c.closeOnLeave.Store(true)
	if err := c.sendMessageLocked(message); err != nil {
		c.closeOnLeave.Store(false)
		if !errors.Is(err, websocket.ErrCloseSent) {
			return err
		}
	}

	return nil
}

func (c *FederationClient) Close() {
	c.closer.Close()
	c.hub.removeFederationClient(c)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeConnection(true)
}

// +checklocks:c.mu
func (c *FederationClient) closeConnection(withBye bool) {
	if c.conn == nil {
		return
	}

	if withBye {
		if err := c.sendMessageLocked(&api.ClientMessage{
			Type: "bye",
		}); err != nil && !isClosedError(err) {
			c.logger.Printf("Error sending bye on federation connection to %s: %s", c.URL(), err)
		}
	}

	closeMessage := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	deadline := time.Now().Add(writeWait)
	if err := c.conn.WriteControl(websocket.CloseMessage, closeMessage, deadline); err != nil && !isClosedError(err) {
		c.logger.Printf("Error sending close message on federation connection to %s: %s", c.URL(), err)
	}

	if err := c.conn.Close(); err != nil && !isClosedError(err) {
		c.logger.Printf("Error closing federation connection to %s: %s", c.URL(), err)
	}

	c.conn = nil
}

func (c *FederationClient) resetReconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnectDelay = initialFederationReconnectInterval
}

func (c *FederationClient) scheduleReconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.scheduleReconnectLocked()
}

// +checklocks:c.mu
func (c *FederationClient) scheduleReconnectLocked() {
	c.reconnecting.Store(true)
	if c.hello.Swap(nil) != nil {
		c.session.SendMessage(&api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "room",
				Type:   "federation_interrupted",
			},
		})
	}
	c.closeConnection(false)

	if c.reconnectFunc != nil {
		c.reconnectFunc.Stop()
	}
	c.reconnectFunc = time.AfterFunc(c.reconnectDelay, c.reconnect)
	c.reconnectDelay *= 2
	if c.reconnectDelay > maxFederationReconnectInterval {
		c.reconnectDelay = maxFederationReconnectInterval
	}
}

func (c *FederationClient) reconnect() {
	if c.closer.IsClosed() {
		return
	}

	ctx := log.NewLoggerContext(context.Background(), c.logger)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.hub.federationTimeout))
	defer cancel()

	if err := c.connect(ctx); err != nil {
		c.logger.Printf("Error connecting to federation server %s for %s: %s", c.URL(), c.session.PublicId(), err)
		c.scheduleReconnect()
		return
	}
}

func (c *FederationClient) readPump(conn *websocket.Conn) {
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
			if c.closer.IsClosed() && isClosedError(err) {
				// Connection closed locally, no need to reconnect.
				break
			}

			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				c.logger.Printf("Error reading from %s for %s: %s", c.URL(), c.session.PublicId(), err)
			}

			c.scheduleReconnect()
			break
		}

		if msgType != websocket.TextMessage {
			continue
		}

		var msg api.ServerMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Printf("Error unmarshalling %s from %s: %s", string(data), c.URL(), err)
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

func (c *FederationClient) sendPing() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}

	now := time.Now().UnixNano()
	msg := strconv.FormatInt(now, 10)
	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		c.logger.Printf("Could not send ping to federated client %s for %s: %v", c.URL(), c.session.PublicId(), err)
		c.scheduleReconnectLocked()
	}
}

func (c *FederationClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.sendPing()
		case <-c.closer.C:
			return
		}
	}
}

func (c *FederationClient) closeWithError(err error) {
	c.Close()
	var e *api.Error
	if !errors.As(err, &e) {
		e = api.NewError("federation_error", err.Error())
	}

	var id string
	if message := c.message.Swap(nil); message != nil {
		id = message.Id
	}

	c.session.SendMessage(&api.ServerMessage{
		Id:    id,
		Type:  "error",
		Error: e,
	})
}

func (c *FederationClient) sendHello(auth *api.FederationAuthParams) error {
	c.helloMu.Lock()
	defer c.helloMu.Unlock()

	return c.sendHelloLocked(auth)
}

// +checklocks:c.helloMu
func (c *FederationClient) sendHelloLocked(auth *api.FederationAuthParams) error {
	c.helloMsgId = internal.RandomString(8)

	authData, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("Error marshalling hello auth message %+v for %s: %s", auth, c.session.PublicId(), err)
	}

	c.helloAuth = auth
	msg := &api.ClientMessage{
		Id:   c.helloMsgId,
		Type: "hello",
		Hello: &api.HelloClientMessage{
			Version:  api.HelloVersionV2,
			Features: c.session.GetFeatures(),
		},
	}
	if resumeId := c.resumeId; resumeId != "" {
		msg.Hello.ResumeId = resumeId
	} else {
		msg.Hello.Auth = &api.HelloClientMessageAuth{
			Type:   api.HelloClientTypeFederation,
			Url:    c.federation.Load().NextcloudUrl,
			Params: authData,
		}
	}
	return c.SendMessage(msg)
}

func (c *FederationClient) processWelcome(msg *api.ServerMessage) {
	if !msg.Welcome.HasFeature(api.ServerFeatureFederation) {
		c.closeWithError(ErrFederationNotSupported)
		return
	}

	federationParams := &api.FederationAuthParams{
		Token: c.federation.Load().Token,
	}
	if err := c.sendHello(federationParams); err != nil {
		c.logger.Printf("Error sending hello message to %s for %s: %s", c.URL(), c.session.PublicId(), err)
		c.closeWithError(err)
	}
}

func (c *FederationClient) processHello(msg *api.ServerMessage) {
	c.resetReconnect()

	c.helloMu.Lock()
	defer c.helloMu.Unlock()

	if msg.Id != c.helloMsgId {
		c.logger.Printf("Received hello response %+v for unknown request, expected %s", msg, c.helloMsgId)
		if err := c.sendHelloLocked(c.helloAuth); err != nil {
			c.closeWithError(err)
		}
		return
	}

	c.helloMsgId = ""
	if msg.Type == "error" {
		switch msg.Error.Code {
		case "no_such_session":
			// Resume failed (e.g. remote has restarted), try to connect new session
			// which may fail if the auth token has expired in the meantime.
			c.resumeId = ""
			c.pendingMessages = nil
			if err := c.sendHelloLocked(c.helloAuth); err != nil {
				c.closeWithError(err)
			}
		default:
			c.logger.Printf("Received hello error from federated client for %s to %s: %+v", c.session.PublicId(), c.URL(), msg)
			c.closeWithError(msg.Error)
		}
		return
	} else if msg.Type != "hello" {
		c.logger.Printf("Received unknown hello response from federated client for %s to %s: %+v", c.session.PublicId(), c.URL(), msg)
		if err := c.sendHelloLocked(c.helloAuth); err != nil {
			c.closeWithError(err)
		}
		return
	}

	c.hello.Store(msg.Hello)
	if c.resumeId == "" {
		c.resumeId = msg.Hello.ResumeId
		if c.reconnecting.Load() {
			c.session.SendMessage(&api.ServerMessage{
				Type: "event",
				Event: &api.EventServerMessage{
					Target:  "room",
					Type:    "federation_resumed",
					Resumed: internal.MakePtr(false),
				},
			})
			// Setting the federation client will reset any information on previously
			// received "join" events.
			c.session.SetFederationClient(c)
		}

		if err := c.joinRoom(); err != nil {
			c.closeWithError(err)
		}
	} else {
		c.session.SendMessage(&api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target:  "room",
				Type:    "federation_resumed",
				Resumed: internal.MakePtr(true),
			},
		})

		if count := len(c.pendingMessages); count > 0 {
			messages := c.pendingMessages
			c.pendingMessages = nil

			c.logger.Printf("Sending %d pending messages to %s for %s", count, c.URL(), c.session.PublicId())

			c.helloMu.Unlock()
			defer c.helloMu.Lock()

			c.mu.Lock()
			defer c.mu.Unlock()
			for _, msg := range messages {
				if err := c.sendMessageLocked(msg); err != nil {
					c.logger.Printf("Error sending pending message %+v on federation connection to %s: %s", msg, c.URL(), err)
					break
				}
			}
		}
	}
}

func (c *FederationClient) joinRoom() error {
	message := c.message.Load()
	if message == nil {
		// Should not happen as the connection has been closed with an error already.
		return ErrNotConnected
	}

	room := message.Room
	remoteRoomId := room.Federation.RoomId
	if remoteRoomId == "" {
		remoteRoomId = room.RoomId
	}

	return c.SendMessage(&api.ClientMessage{
		Id:   message.Id,
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    remoteRoomId,
			SessionId: room.SessionId,
		},
	})
}

func (c *FederationClient) updateActor(u api.StringMap, actorIdKey, actorTypeKey string, localCloudUrl string, remoteCloudUrl string) (changed bool) {
	if actorType, found := api.GetStringMapEntry[string](u, actorTypeKey); found {
		if actorId, found := api.GetStringMapEntry[string](u, actorIdKey); found {
			switch actorType {
			case api.ActorTypeFederatedUsers:
				if strings.HasSuffix(actorId, localCloudUrl) {
					u[actorIdKey] = actorId[:len(actorId)-len(localCloudUrl)]
					u[actorTypeKey] = api.ActorTypeUsers
					changed = true
				}
			case api.ActorTypeUsers:
				u[actorIdKey] = actorId + remoteCloudUrl
				u[actorTypeKey] = api.ActorTypeFederatedUsers
				changed = true
			}
		}
	}
	return
}

func (c *FederationClient) updateComment(comment api.StringMap, localCloudUrl string, remoteCloudUrl string) bool {
	changed := c.updateActor(comment, "actorId", "actorType", localCloudUrl, remoteCloudUrl)
	if c.updateActor(comment, "lastEditActorId", "lastEditActorType", localCloudUrl, remoteCloudUrl) {
		changed = true
	}

	if token, found := api.GetStringMapString[string](comment, "token"); found && c.changeRoomId.Load() && token == c.RemoteRoomId() {
		comment["token"] = c.RoomId()
		changed = true
	}

	if params, found := api.GetStringMapEntry[map[string]any](comment, "messageParameters"); found {
		localUrl := getCloudUrlWithoutPath(c.session.BackendUrl())
		remoteUrl := getCloudUrlWithoutPath(c.federation.Load().NextcloudUrl)
		for key, paramOb := range params {
			if !strings.HasPrefix(key, "mention-") {
				// Only need to process mentions.
				continue
			}

			param, ok := api.ConvertStringMap(paramOb)
			if !ok {
				continue
			}

			if ptype, found := api.GetStringMapString[string](param, "type"); found {
				switch ptype {
				case "user":
					if server, found := api.GetStringMapString[string](param, "server"); found && server == localUrl {
						delete(param, "server")
						params[key] = param
						changed = true
						continue
					}

					if _, found := api.GetStringMapString[string](param, "mention-id"); !found {
						param["mention-id"] = param["id"]
						param["server"] = remoteUrl
						params[key] = param
						changed = true
						continue
					}
				case "call":
					roomId := c.RoomId()
					remoteRoomId := c.RemoteRoomId()
					// TODO: Should we also rewrite the room avatar url in "icon-url"?
					if c.changeRoomId.Load() && param["id"] == remoteRoomId {
						param["id"] = roomId
					}
				}
			}
		}
	}
	return changed
}

func (c *FederationClient) updateEventUsers(users []api.StringMap, localSessionId api.PublicSessionId, remoteSessionId api.PublicSessionId) {
	localCloudUrl := "@" + getCloudUrl(c.session.BackendUrl())
	remoteCloudUrl := "@" + getCloudUrl(c.federation.Load().NextcloudUrl)
	checkSessionId := true
	for _, u := range users {
		c.updateActor(u, "actorId", "actorType", localCloudUrl, remoteCloudUrl)

		if checkSessionId {
			key := "sessionId"
			sid, found := api.GetStringMapString[api.PublicSessionId](u, key)
			if !found {
				key := "sessionid"
				sid, found = api.GetStringMapString[api.PublicSessionId](u, key)
			}
			if found && sid == remoteSessionId {
				u[key] = localSessionId
				checkSessionId = false
			}
		}
	}
}

func (c *FederationClient) updateSessionRecipient(recipient *api.MessageClientMessageRecipient, localSessionId api.PublicSessionId, remoteSessionId api.PublicSessionId) {
	if recipient != nil && recipient.Type == api.RecipientTypeSession && remoteSessionId != "" && recipient.SessionId == remoteSessionId {
		recipient.SessionId = localSessionId
	}
}

func (c *FederationClient) updateSessionSender(sender *api.MessageServerMessageSender, localSessionId api.PublicSessionId, remoteSessionId api.PublicSessionId) {
	if sender != nil && sender.Type == api.RecipientTypeSession && remoteSessionId != "" && sender.SessionId == remoteSessionId {
		sender.SessionId = localSessionId
	}
}

func (c *FederationClient) processMessage(msg *api.ServerMessage) {
	localSessionId := c.session.PublicId()
	var remoteSessionId api.PublicSessionId
	if hello := c.hello.Load(); hello != nil {
		remoteSessionId = hello.SessionId
	}

	remoteRoomId := c.RemoteRoomId()
	roomId := c.RoomId()

	var doClose bool
	switch msg.Type {
	case "control":
		c.updateSessionRecipient(msg.Control.Recipient, localSessionId, remoteSessionId)
		c.updateSessionSender(msg.Control.Sender, localSessionId, remoteSessionId)
		// Special handling for "forceMute" event.
		if len(msg.Control.Data) > 0 && msg.Control.Data[0] == '{' {
			var data api.StringMap
			if err := json.Unmarshal(msg.Control.Data, &data); err == nil {
				if action, found := data["action"]; found && action == "forceMute" {
					if peerId, found := api.GetStringMapString[api.PublicSessionId](data, "peerId"); found && peerId == remoteSessionId {
						data["peerId"] = localSessionId
						if d, err := json.Marshal(data); err == nil {
							msg.Control.Data = d
						}
					}
				}
			}
		}
	case "event":
		switch msg.Event.Target {
		case "participants":
			switch msg.Event.Type {
			case "update":
				if c.changeRoomId.Load() && msg.Event.Update.RoomId == remoteRoomId {
					msg.Event.Update.RoomId = roomId
				}
				if remoteSessionId != "" {
					c.updateEventUsers(msg.Event.Update.Changed, localSessionId, remoteSessionId)
					c.updateEventUsers(msg.Event.Update.Users, localSessionId, remoteSessionId)
				}
			case "flags":
				if c.changeRoomId.Load() && msg.Event.Flags.RoomId == remoteRoomId {
					msg.Event.Flags.RoomId = roomId
				}
				if remoteSessionId != "" && msg.Event.Flags.SessionId == remoteSessionId {
					msg.Event.Flags.SessionId = localSessionId
				}
			case "message":
				if c.changeRoomId.Load() && msg.Event.Message.RoomId == remoteRoomId {
					msg.Event.Message.RoomId = roomId
				}
			}
		case "room":
			switch msg.Event.Type {
			case "join":
				if remoteSessionId != "" {
					for idx, j := range msg.Event.Join {
						if j.SessionId == remoteSessionId {
							j.SessionId = localSessionId
							msg.Event.Join[idx] = j
							break
						}
					}
				}
			case "leave":
				if remoteSessionId != "" {
					for idx, j := range msg.Event.Leave {
						if j == remoteSessionId {
							msg.Event.Leave[idx] = localSessionId
							if c.closeOnLeave.Load() {
								doClose = true
							}
							break
						}
					}
				}
			case "message":
				if c.changeRoomId.Load() && msg.Event.Message.RoomId == remoteRoomId {
					msg.Event.Message.RoomId = roomId
				}
				if msg.Event.Type == "message" && msg.Event.Message != nil {
					if data, err := msg.Event.Message.GetData(); err == nil {
						if data.Type == "chat" && data.Chat != nil && len(data.Chat.Comment) > 0 {
							var comment api.StringMap
							if err := json.Unmarshal(data.Chat.Comment, &comment); err == nil {
								localCloudUrl := "@" + getCloudUrl(c.session.BackendUrl())
								remoteCloudUrl := "@" + getCloudUrl(c.federation.Load().NextcloudUrl)
								changed := c.updateComment(comment, localCloudUrl, remoteCloudUrl)
								if parent, found := comment.GetStringMap("parent"); found {
									if c.updateComment(parent, localCloudUrl, remoteCloudUrl) {
										comment["parent"] = parent
										changed = true
									}
								}
								if changed {
									if encoded, err := json.Marshal(comment); err == nil {
										data.Chat.Comment = encoded
										if encoded, err = json.Marshal(data); err == nil {
											msg.Event.Message.Data = encoded
										}
									}
								}
							}
						}
					}
				}
			}
		case "roomlist":
			switch msg.Event.Type {
			case "invite":
				if c.changeRoomId.Load() && msg.Event.Invite.RoomId == remoteRoomId {
					msg.Event.Invite.RoomId = roomId
				}
			case "disinvite":
				if c.changeRoomId.Load() && msg.Event.Disinvite.RoomId == remoteRoomId {
					msg.Event.Disinvite.RoomId = roomId
				}
			case "update":
				if c.changeRoomId.Load() && msg.Event.Update.RoomId == remoteRoomId {
					msg.Event.Update.RoomId = roomId
				}
			}
		}
	case "error":
		if c.changeRoomId.Load() && msg.Error.Code == "already_joined" {
			if len(msg.Error.Details) > 0 {
				var details api.RoomErrorDetails
				if err := json.Unmarshal(msg.Error.Details, &details); err == nil && details.Room != nil {
					if details.Room.RoomId == remoteRoomId {
						details.Room.RoomId = roomId
						if data, err := json.Marshal(details); err == nil {
							msg.Error.Details = data
						}
					}
				}
			}
		}
	case "room":
		if message := c.message.Load(); message != nil {
			if msg.Id != "" && message.Id == msg.Id {
				// Got response to initial join request, clear id so future join
				// requests will not be mapped to any client callbacks.
				message.Id = ""
				c.message.Store(message)
			}

			room := message.Room
			roomId = room.RoomId
			remoteRoomId = room.Federation.RoomId
			if remoteRoomId == "" {
				remoteRoomId = room.RoomId
			}

			c.roomId.Store(room.RoomId)
			c.remoteRoomId.Store(remoteRoomId)
			c.changeRoomId.Store(room.RoomId != remoteRoomId)
			c.federation.Store(room.Federation)
		}

		if msg.Room.RoomId == "" && c.closeOnLeave.Load() {
			doClose = true
		} else if c.changeRoomId.Load() && msg.Room.RoomId == remoteRoomId {
			msg.Room.RoomId = roomId
		}
	case "message":
		c.updateSessionRecipient(msg.Message.Recipient, localSessionId, remoteSessionId)
		c.updateSessionSender(msg.Message.Sender, localSessionId, remoteSessionId)
		if remoteSessionId != "" && len(msg.Message.Data) > 0 {
			var ao api.AnswerOfferMessage
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
	case "transient":
		if remoteSessionId != "" && msg.TransientData != nil && msg.TransientData.Key == api.TransientSessionDataPrefix+string(remoteSessionId) {
			msg.TransientData.Key = api.TransientSessionDataPrefix + string(localSessionId)
		}
	}
	c.session.SendMessage(msg)

	if doClose {
		c.Close()
	}
}

func (c *FederationClient) ProxyMessage(message *api.ClientMessage) error {
	switch message.Type {
	case "message":
		if hello := c.hello.Load(); hello != nil {
			c.updateSessionRecipient(&message.Message.Recipient, hello.SessionId, c.session.PublicId())
		}
	case "transient":
		if hello := c.hello.Load(); hello != nil {
			if message.TransientData != nil && message.TransientData.Key == api.TransientSessionDataPrefix+string(c.session.PublicId()) {
				message.TransientData.Key = api.TransientSessionDataPrefix + string(hello.SessionId)
			}
		}
	}

	return c.SendMessage(message)
}

func (c *FederationClient) SendMessage(message *api.ClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sendMessageLocked(message)
}

func (c *FederationClient) deferMessage(message *api.ClientMessage) {
	c.helloMu.Lock()
	defer c.helloMu.Unlock()
	if c.resumeId == "" {
		return
	}

	c.pendingMessages = append(c.pendingMessages, message)
	if len(c.pendingMessages) >= warnPendingMessagesCount {
		c.logger.Printf("Session %s has %d pending federated messages", c.session.PublicId(), len(c.pendingMessages))
	}
}

// +checklocks:c.mu
func (c *FederationClient) sendMessageLocked(message *api.ClientMessage) error {
	if c.conn == nil {
		if message.Type != "room" {
			// Join requests will be automatically sent after the hello response has
			// been received.
			c.deferMessage(message)
		}
		return nil
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	writer, err := c.conn.NextWriter(websocket.TextMessage)
	if err == nil {
		if m, ok := (any(message)).(easyjson.Marshaler); ok {
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

		c.logger.Printf("Could not send message %+v for %s to federated client %s: %v", message, c.session.PublicId(), c.URL(), err)
		c.deferMessage(message)
		c.scheduleReconnectLocked()
	}

	return nil
}
