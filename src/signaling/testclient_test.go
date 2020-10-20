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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var (
	testBackendSecret  = []byte("secret")
	testInternalSecret = []byte("internal-secret")

	NoMessageReceivedError = fmt.Errorf("No message was received by the server.")
)

type TestBackendClientAuthParams struct {
	UserId string `json:"userid"`
}

func getWebsocketUrl(url string) string {
	if strings.HasPrefix(url, "http://") {
		return "ws://" + url[7:] + "/spreed"
	} else if strings.HasPrefix(url, "https://") {
		return "wss://" + url[8:] + "/spreed"
	} else {
		panic("Unsupported URL: " + url)
	}
}

func getPrivateSessionIdData(h *Hub, privateId string) *SessionIdData {
	decodedPrivate := h.decodeSessionId(privateId, privateSessionName)
	if decodedPrivate == nil {
		panic("invalid private session id")
	}
	return decodedPrivate
}

func getPubliceSessionIdData(h *Hub, publicId string) *SessionIdData {
	decodedPublic := h.decodeSessionId(publicId, publicSessionName)
	if decodedPublic == nil {
		panic("invalid public session id")
	}
	return decodedPublic
}

func privateToPublicSessionId(h *Hub, privateId string) string {
	decodedPrivate := getPrivateSessionIdData(h, privateId)
	if decodedPrivate == nil {
		panic("invalid private session id")
	}
	encodedPublic, err := h.encodeSessionId(decodedPrivate, publicSessionName)
	if err != nil {
		panic(err)
	}
	return encodedPublic
}

func equalPublicAndPrivateSessionId(h *Hub, publicId, privateId string) bool {
	decodedPublic := h.decodeSessionId(publicId, publicSessionName)
	if decodedPublic == nil {
		panic("invalid public session id")
	}
	decodedPrivate := h.decodeSessionId(privateId, privateSessionName)
	if decodedPrivate == nil {
		panic("invalid private session id")
	}
	return decodedPublic.Sid == decodedPrivate.Sid
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

func toJsonString(o interface{}) string {
	if s, err := json.Marshal(o); err != nil {
		panic(err)
	} else {
		return string(s)
	}
}

func checkMessageType(message *ServerMessage, expectedType string) error {
	if message == nil {
		return NoMessageReceivedError
	}

	if message.Type != expectedType {
		return fmt.Errorf("Expected \"%s\" message, got %+v (%s)", expectedType, message, toJsonString(message))
	}
	switch message.Type {
	case "hello":
		if message.Hello == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v (%s)", expectedType, message, toJsonString(message))
		}
	case "message":
		if message.Message == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v (%s)", expectedType, message, toJsonString(message))
		} else if message.Message.Data == nil || len(*message.Message.Data) == 0 {
			return fmt.Errorf("Received message without data")
		}
	case "room":
		if message.Room == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v (%s)", expectedType, message, toJsonString(message))
		}
	case "event":
		if message.Event == nil {
			return fmt.Errorf("Expected \"%s\" message, got %+v (%s)", expectedType, message, toJsonString(message))
		}
	}

	return nil
}

func checkMessageSender(hub *Hub, message *MessageServerMessage, senderType string, hello *HelloServerMessage) error {
	if message.Sender.Type != senderType {
		return fmt.Errorf("Expected sender type %s, got %s", senderType, message.Sender.SessionId)
	} else if message.Sender.SessionId != hello.SessionId {
		return fmt.Errorf("Expected session id %+v, got %+v",
			getPubliceSessionIdData(hub, hello.SessionId), getPubliceSessionIdData(hub, message.Sender.SessionId))
	} else if message.Sender.UserId != hello.UserId {
		return fmt.Errorf("Expected user id %s, got %s", hello.UserId, message.Sender.UserId)
	}

	return nil
}

func checkReceiveClientMessageWithSender(ctx context.Context, client *TestClient, senderType string, hello *HelloServerMessage, payload interface{}, sender **MessageServerMessageSender) error {
	message, err := client.RunUntilMessage(ctx)
	if err := checkUnexpectedClose(err); err != nil {
		return err
	} else if err := checkMessageType(message, "message"); err != nil {
		return err
	} else if err := checkMessageSender(client.hub, message.Message, senderType, hello); err != nil {
		return err
	} else {
		if err := json.Unmarshal(*message.Message.Data, payload); err != nil {
			return err
		}
	}
	if sender != nil {
		*sender = message.Message.Sender
	}
	return nil
}

func checkReceiveClientMessage(ctx context.Context, client *TestClient, senderType string, hello *HelloServerMessage, payload interface{}) error {
	return checkReceiveClientMessageWithSender(ctx, client, senderType, hello, payload, nil)
}

func checkReceiveClientEvent(ctx context.Context, client *TestClient, eventType string, msg **EventServerMessage) error {
	message, err := client.RunUntilMessage(ctx)
	if err := checkUnexpectedClose(err); err != nil {
		return err
	} else if err := checkMessageType(message, "event"); err != nil {
		return err
	} else if message.Event.Type != eventType {
		return fmt.Errorf("Expected \"%s\" event type, got \"%s\"", eventType, message.Event.Type)
	} else {
		if msg != nil {
			*msg = message.Event
		}
	}
	return nil
}

type TestClient struct {
	t      *testing.T
	hub    *Hub
	server *httptest.Server

	conn      *websocket.Conn
	localAddr net.Addr

	messageChan   chan []byte
	readErrorChan chan error

	publicId string
}

func NewTestClient(t *testing.T, server *httptest.Server, hub *Hub) *TestClient {
	// Reference "hub" to prevent compiler error.
	conn, _, err := websocket.DefaultDialer.Dial(getWebsocketUrl(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}

	messageChan := make(chan []byte)
	readErrorChan := make(chan error)

	go func() {
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				readErrorChan <- err
				return
			} else if messageType != websocket.TextMessage {
				t.Errorf("Expect text message, got %d", messageType)
				return
			}

			messageChan <- data
		}
	}()

	return &TestClient{
		t:      t,
		hub:    hub,
		server: server,

		conn:      conn,
		localAddr: conn.LocalAddr(),

		messageChan:   messageChan,
		readErrorChan: readErrorChan,
	}
}

func (c *TestClient) CloseWithBye() {
	c.SendBye()
	c.Close()
}

func (c *TestClient) Close() {
	c.conn.WriteMessage(websocket.CloseMessage, []byte{})
	c.conn.Close()

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

func (c *TestClient) WaitForClientRemoved(ctx context.Context) error {
	c.hub.mu.Lock()
	defer c.hub.mu.Unlock()
	for {
		found := false
		for _, client := range c.hub.clients {
			client.mu.Lock()
			conn := client.conn
			client.mu.Unlock()
			if conn != nil && conn.RemoteAddr().String() == c.localAddr.String() {
				found = true
				break
			}
		}
		if !found {
			break
		}

		c.hub.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(time.Millisecond)
		}
		c.hub.mu.Lock()
	}
	return nil
}

func (c *TestClient) WaitForSessionRemoved(ctx context.Context, sessionId string) error {
	data := c.hub.decodeSessionId(sessionId, publicSessionName)
	if data == nil {
		return fmt.Errorf("Invalid session id passed")
	}

	c.hub.mu.Lock()
	defer c.hub.mu.Unlock()
	for {
		_, found := c.hub.sessions[data.Sid]
		if !found {
			break
		}

		c.hub.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(time.Millisecond)
		}
		c.hub.mu.Lock()
	}
	return nil
}

func (c *TestClient) WriteJSON(data interface{}) error {
	if msg, ok := data.(*ClientMessage); ok {
		if err := msg.CheckValid(); err != nil {
			return err
		}
	}
	return c.conn.WriteJSON(data)
}

func (c *TestClient) EnsuerWriteJSON(data interface{}) {
	if err := c.WriteJSON(data); err != nil {
		c.t.Fatalf("Could not write JSON %+v: %s", data, err)
	}
}

func (c *TestClient) SendHello(userid string) error {
	params := TestBackendClientAuthParams{
		UserId: userid,
	}
	return c.SendHelloParams(c.server.URL, "", params)
}

func (c *TestClient) SendHelloResume(resumeId string) error {
	hello := &ClientMessage{
		Id:   "1234",
		Type: "hello",
		Hello: &HelloClientMessage{
			Version:  HelloVersion,
			ResumeId: resumeId,
		},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendHelloClient(userid string) error {
	params := TestBackendClientAuthParams{
		UserId: userid,
	}
	return c.SendHelloParams(c.server.URL, "client", params)
}

func (c *TestClient) SendHelloInternal() error {
	random := newRandomString(48)
	mac := hmac.New(sha256.New, testInternalSecret)
	mac.Write([]byte(random))
	token := hex.EncodeToString(mac.Sum(nil))
	backend := c.server.URL

	params := ClientTypeInternalAuthParams{
		Random:  random,
		Token:   token,
		Backend: backend,
	}
	return c.SendHelloParams("", "internal", params)
}

func (c *TestClient) SendHelloParams(url string, clientType string, params interface{}) error {
	data, err := json.Marshal(params)
	if err != nil {
		c.t.Fatal(err)
	}

	hello := &ClientMessage{
		Id:   "1234",
		Type: "hello",
		Hello: &HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Type:   clientType,
				Url:    url,
				Params: (*json.RawMessage)(&data),
			},
		},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendBye() error {
	hello := &ClientMessage{
		Id:   "9876",
		Type: "bye",
		Bye:  &ByeClientMessage{},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendMessage(recipient MessageClientMessageRecipient, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		c.t.Fatal(err)
	}

	message := &ClientMessage{
		Id:   "abcd",
		Type: "message",
		Message: &MessageClientMessage{
			Recipient: recipient,
			Data:      (*json.RawMessage)(&payload),
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) DrainMessages(ctx context.Context) error {
	select {
	case err := <-c.readErrorChan:
		return err
	case <-c.messageChan:
		n := len(c.messageChan)
		for i := 0; i < n; i++ {
			<-c.messageChan
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (c *TestClient) RunUntilMessage(ctx context.Context) (message *ServerMessage, err error) {
	select {
	case err = <-c.readErrorChan:
	case msg := <-c.messageChan:
		var m ServerMessage
		if err = json.Unmarshal(msg, &m); err == nil {
			message = &m
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	return
}

func (c *TestClient) RunUntilHello(ctx context.Context) (message *ServerMessage, err error) {
	if message, err = c.RunUntilMessage(ctx); err != nil {
		return nil, err
	}
	if err := checkUnexpectedClose(err); err != nil {
		return nil, err
	}
	if err := checkMessageType(message, "hello"); err != nil {
		return nil, err
	}
	c.publicId = message.Hello.SessionId
	return message, nil
}

func (c *TestClient) JoinRoom(ctx context.Context, roomId string) (message *ServerMessage, err error) {
	return c.JoinRoomWithRoomSession(ctx, roomId, roomId+"-"+c.publicId)
}

func (c *TestClient) JoinRoomWithRoomSession(ctx context.Context, roomId string, roomSessionId string) (message *ServerMessage, err error) {
	msg := &ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomSessionId,
		},
	}
	if err := c.WriteJSON(msg); err != nil {
		return nil, err
	}

	if message, err = c.RunUntilMessage(ctx); err != nil {
		return nil, err
	}
	if err := checkUnexpectedClose(err); err != nil {
		return nil, err
	}
	if err := checkMessageType(message, "room"); err != nil {
		return nil, err
	}
	return message, nil
}

func checkMessageRoomId(message *ServerMessage, roomId string) error {
	if err := checkMessageType(message, "room"); err != nil {
		return err
	}
	if message.Room.RoomId != roomId {
		return fmt.Errorf("Expected room id %s, got %+v", roomId, message.Room)
	}
	return nil
}

func (c *TestClient) RunUntilRoom(ctx context.Context, roomId string) error {
	message, err := c.RunUntilMessage(ctx)
	if err != nil {
		return err
	}
	if err := checkUnexpectedClose(err); err != nil {
		return err
	}
	return checkMessageRoomId(message, roomId)
}

func (c *TestClient) checkMessageJoined(message *ServerMessage, hello *HelloServerMessage) error {
	return c.checkMessageJoinedSession(message, hello.SessionId, hello.UserId)
}

func (c *TestClient) checkMessageJoinedSession(message *ServerMessage, sessionId string, userId string) error {
	if err := checkMessageType(message, "event"); err != nil {
		return err
	} else if message.Event.Target != "room" {
		return fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "join" {
		return fmt.Errorf("Expected event type join, got %+v", message.Event)
	} else if len(message.Event.Join) != 1 {
		return fmt.Errorf("Expected one join event entry, got %+v", message.Event)
	} else {
		evt := message.Event.Join[0]
		if sessionId != "" && evt.SessionId != sessionId {
			return fmt.Errorf("Expected join session id %+v, got %+v",
				getPubliceSessionIdData(c.hub, sessionId), getPubliceSessionIdData(c.hub, evt.SessionId))
		}
		if evt.UserId != userId {
			return fmt.Errorf("Expected join user id %s, got %+v", userId, evt)
		}
	}
	return nil
}

func (c *TestClient) RunUntilJoined(ctx context.Context, hello *HelloServerMessage) error {
	if message, err := c.RunUntilMessage(ctx); err != nil {
		return err
	} else {
		return c.checkMessageJoined(message, hello)
	}
}

func (c *TestClient) checkMessageRoomLeave(message *ServerMessage, hello *HelloServerMessage) error {
	return c.checkMessageRoomLeaveSession(message, hello.SessionId)
}

func (c *TestClient) checkMessageRoomLeaveSession(message *ServerMessage, sessionId string) error {
	if err := checkMessageType(message, "event"); err != nil {
		return err
	} else if message.Event.Target != "room" {
		return fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "leave" {
		return fmt.Errorf("Expected event type leave, got %+v", message.Event)
	} else if len(message.Event.Leave) != 1 {
		return fmt.Errorf("Expected one leave event entry, got %+v", message.Event)
	} else if message.Event.Leave[0] != sessionId {
		return fmt.Errorf("Expected leave session id %+v, got %+v",
			getPubliceSessionIdData(c.hub, sessionId), getPubliceSessionIdData(c.hub, message.Event.Leave[0]))
	}
	return nil
}

func (c *TestClient) RunUntilLeft(ctx context.Context, hello *HelloServerMessage) error {
	if message, err := c.RunUntilMessage(ctx); err != nil {
		return err
	} else {
		return c.checkMessageRoomLeave(message, hello)
	}
}

func checkMessageRoomlistUpdate(message *ServerMessage) (*RoomEventServerMessage, error) {
	if err := checkMessageType(message, "event"); err != nil {
		return nil, err
	} else if message.Event.Target != "roomlist" {
		return nil, fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "update" || message.Event.Update == nil {
		return nil, fmt.Errorf("Expected event type update, got %+v", message.Event)
	} else {
		return message.Event.Update, nil
	}
}

func (c *TestClient) RunUntilRoomlistUpdate(ctx context.Context) (*RoomEventServerMessage, error) {
	if message, err := c.RunUntilMessage(ctx); err != nil {
		return nil, err
	} else {
		return checkMessageRoomlistUpdate(message)
	}
}

func checkMessageRoomlistDisinvite(message *ServerMessage) (*RoomDisinviteEventServerMessage, error) {
	if err := checkMessageType(message, "event"); err != nil {
		return nil, err
	} else if message.Event.Target != "roomlist" {
		return nil, fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "disinvite" || message.Event.Disinvite == nil {
		return nil, fmt.Errorf("Expected event type disinvite, got %+v", message.Event)
	}

	return message.Event.Disinvite, nil
}

func (c *TestClient) RunUntilRoomlistDisinvite(ctx context.Context) (*RoomDisinviteEventServerMessage, error) {
	if message, err := c.RunUntilMessage(ctx); err != nil {
		return nil, err
	} else {
		return checkMessageRoomlistDisinvite(message)
	}
}

func checkMessageParticipantsInCall(message *ServerMessage) (*RoomEventServerMessage, error) {
	if err := checkMessageType(message, "event"); err != nil {
		return nil, err
	} else if message.Event.Target != "participants" {
		return nil, fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "update" || message.Event.Update == nil {
		return nil, fmt.Errorf("Expected event type incall, got %+v", message.Event)
	}

	return message.Event.Update, nil
}

func checkMessageRoomMessage(message *ServerMessage) (*RoomEventMessage, error) {
	if err := checkMessageType(message, "event"); err != nil {
		return nil, err
	} else if message.Event.Target != "room" {
		return nil, fmt.Errorf("Expected event target room, got %+v", message.Event)
	} else if message.Event.Type != "message" || message.Event.Message == nil {
		return nil, fmt.Errorf("Expected event type message, got %+v", message.Event)
	}

	return message.Event.Message, nil
}

func (c *TestClient) RunUntilRoomMessage(ctx context.Context) (*RoomEventMessage, error) {
	if message, err := c.RunUntilMessage(ctx); err != nil {
		return nil, err
	} else {
		return checkMessageRoomMessage(message)
	}
}

func checkMessageError(message *ServerMessage, msgid string) error {
	if err := checkMessageType(message, "error"); err != nil {
		return err
	} else if message.Error.Code != msgid {
		return fmt.Errorf("Expected error \"%s\", got \"%s\" (%+v)", msgid, message.Error.Code, message.Error)
	}

	return nil
}
