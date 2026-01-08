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
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

var (
	testBackendSecret  = []byte("secret")
	testInternalSecret = []byte("internal-secret")

	ErrNoMessageReceived = errors.New("no message was received by the server")

	testClientDialer = websocket.Dialer{
		WriteBufferPool: &sync.Pool{},
	}
)

type TestBackendClientAuthParams struct {
	UserId string `json:"userid"`
}

func getWebsocketUrl(url string) string {
	if url, found := strings.CutPrefix(url, "http://"); found {
		return "ws://" + url + "/spreed"
	} else if url, found := strings.CutPrefix(url, "https://"); found {
		return "wss://" + url + "/spreed"
	} else {
		panic("Unsupported URL: " + url)
	}
}

func getPubliceSessionIdData(h *Hub, publicId api.PublicSessionId) *SessionIdData {
	decodedPublic := h.decodePublicSessionId(publicId)
	if decodedPublic == nil {
		panic("invalid public session id")
	}
	return decodedPublic
}

func checkMessageType(t *testing.T, message *api.ServerMessage, expectedType string) bool {
	assert := assert.New(t)
	if !assert.NotNil(message, "no message received") {
		return false
	}

	failed := !assert.Equal(expectedType, message.Type, "invalid message type in %+v", message)

	switch message.Type {
	case "hello":
		if !assert.NotNil(message.Hello, "hello missing in %+v", message) {
			failed = true
		}
	case "message":
		if assert.NotNil(message.Message, "message missing in %+v", message) {
			if !assert.NotEmpty(message.Message.Data, "message %+v has no data", message) {
				failed = true
			}
		} else {
			failed = true
		}
	case "room":
		if !assert.NotNil(message.Room, "room missing in %+v", message) {
			failed = true
		}
	case "event":
		if !assert.NotNil(message.Event, "event missing in %+v", message) {
			failed = true
		}
	case "transient":
		if !assert.NotNil(message.TransientData, "transient data missing in %+v", message) {
			failed = true
		}
	}
	return !failed
}

func checkMessageSender(t *testing.T, hub *Hub, sender *api.MessageServerMessageSender, senderType string, hello *api.HelloServerMessage) bool {
	assert := assert.New(t)
	return assert.Equal(senderType, sender.Type, "invalid sender type in %+v", sender) &&
		assert.Equal(hello.SessionId, sender.SessionId, "invalid session id, expectd %+v, got %+v in %+v",
			getPubliceSessionIdData(hub, hello.SessionId),
			getPubliceSessionIdData(hub, sender.SessionId),
			sender,
		) &&
		assert.Equal(hello.UserId, sender.UserId, "invalid userid in %+v", sender)
}

func checkReceiveClientMessageWithSenderAndRecipient(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any, sender **api.MessageServerMessageSender, recipient **api.MessageClientMessageRecipient) bool {
	assert := assert.New(t)
	message, ok := client.RunUntilMessage(ctx)
	if !ok ||
		!checkMessageType(t, message, "message") ||
		!checkMessageSender(t, client.hub, message.Message.Sender, senderType, hello) ||
		!assert.NoError(json.Unmarshal(message.Message.Data, payload)) {
		return false
	}

	if sender != nil {
		*sender = message.Message.Sender
	}
	if recipient != nil {
		*recipient = message.Message.Recipient
	}
	return true
}

func checkReceiveClientMessageWithSender(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any, sender **api.MessageServerMessageSender) bool {
	return checkReceiveClientMessageWithSenderAndRecipient(ctx, t, client, senderType, hello, payload, sender, nil)
}

func checkReceiveClientMessage(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any) bool {
	return checkReceiveClientMessageWithSenderAndRecipient(ctx, t, client, senderType, hello, payload, nil, nil)
}

func checkReceiveClientControlWithSenderAndRecipient(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any, sender **api.MessageServerMessageSender, recipient **api.MessageClientMessageRecipient) bool {
	assert := assert.New(t)
	message, ok := client.RunUntilMessage(ctx)
	if !ok ||
		!checkMessageType(t, message, "control") ||
		!checkMessageSender(t, client.hub, message.Control.Sender, senderType, hello) ||
		!assert.NoError(json.Unmarshal(message.Control.Data, payload)) {
		return false
	}

	if sender != nil {
		*sender = message.Control.Sender
	}
	if recipient != nil {
		*recipient = message.Control.Recipient
	}
	return true
}

func checkReceiveClientControlWithSender(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any, sender **api.MessageServerMessageSender) bool { // nolint
	return checkReceiveClientControlWithSenderAndRecipient(ctx, t, client, senderType, hello, payload, sender, nil)
}

func checkReceiveClientControl(ctx context.Context, t *testing.T, client *TestClient, senderType string, hello *api.HelloServerMessage, payload any) bool {
	return checkReceiveClientControlWithSenderAndRecipient(ctx, t, client, senderType, hello, payload, nil, nil)
}

func checkReceiveClientEvent(ctx context.Context, t *testing.T, client *TestClient, eventType string, msg **api.EventServerMessage) bool {
	assert := assert.New(t)
	message, ok := client.RunUntilMessage(ctx)
	if !ok ||
		!checkMessageType(t, message, "event") ||
		!assert.Equal(eventType, message.Event.Type, "invalid event type in %+v", message) {
		return false
	}

	if msg != nil {
		*msg = message.Event
	}
	return true
}

type TestClient struct {
	t       *testing.T
	assert  *assert.Assertions
	require *require.Assertions
	hub     *Hub
	server  *httptest.Server

	mu sync.Mutex
	// +checklocks:mu
	conn      *websocket.Conn
	localAddr net.Addr

	messageChan   chan []byte
	readErrorChan chan error

	publicId api.PublicSessionId
}

func NewTestClientContext(ctx context.Context, t *testing.T, server *httptest.Server, hub *Hub) *TestClient {
	// Reference "hub" to prevent compiler error.
	conn, _, err := testClientDialer.DialContext(ctx, getWebsocketUrl(server.URL), nil)
	require.NoError(t, err)

	messageChan := make(chan []byte)
	readErrorChan := make(chan error, 1)

	closing := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				readErrorChan <- err
				return
			} else if !assert.Equal(t, websocket.TextMessage, messageType) {
				return
			}

			select {
			case messageChan <- data:
			case <-closing:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(closing)
		<-closed
	})

	return &TestClient{
		t:       t,
		assert:  assert.New(t),
		require: require.New(t),
		hub:     hub,
		server:  server,

		conn:      conn,
		localAddr: conn.LocalAddr(),

		messageChan:   messageChan,
		readErrorChan: readErrorChan,
	}
}

func NewTestClient(t *testing.T, server *httptest.Server, hub *Hub) *TestClient {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := NewTestClientContext(ctx, t, server, hub)
	if msg, ok := client.RunUntilMessage(ctx); ok {
		assert.Equal(t, "welcome", msg.Type, "invalid initial message type in %+v", msg)
	}
	return client
}

func NewTestClientWithHello(ctx context.Context, t *testing.T, server *httptest.Server, hub *Hub, userId string) (*TestClient, *api.ServerMessage) {
	client := NewTestClient(t, server, hub)
	t.Cleanup(func() {
		client.CloseWithBye()
	})

	require.NoError(t, client.SendHello(userId))
	hello := MustSucceed1(t, client.RunUntilHello, ctx)
	return client, hello
}

func (c *TestClient) CloseWithBye() {
	c.SendBye() // nolint
	c.Close()
}

func (c *TestClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err == websocket.ErrCloseSent {
		// Already closed
		return
	}

	// Wait a bit for close message to be processed.
	time.Sleep(100 * time.Millisecond)
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
			if cc, ok := client.(*HubClient); ok {
				conn := cc.GetConn()
				if conn != nil && conn.RemoteAddr().String() == c.localAddr.String() {
					found = true
					break
				}
			}
		}
		if !found {
			break
		}

		c.hub.mu.Unlock()
		select {
		case <-ctx.Done():
			c.hub.mu.Lock()
			return ctx.Err()
		default:
			time.Sleep(time.Millisecond)
		}
		c.hub.mu.Lock()
	}
	return nil
}

func (c *TestClient) WaitForSessionRemoved(ctx context.Context, sessionId api.PublicSessionId) error {
	data := c.hub.decodePublicSessionId(sessionId)
	if data == nil {
		return errors.New("Invalid session id passed")
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
			c.hub.mu.Lock()
			return ctx.Err()
		default:
			time.Sleep(time.Millisecond)
		}
		c.hub.mu.Lock()
	}
	return nil
}

func (c *TestClient) WriteJSON(data any) error {
	if !strings.Contains(c.t.Name(), "HelloUnsupportedVersion") {
		if msg, ok := data.(*api.ClientMessage); ok {
			if err := msg.CheckValid(); err != nil {
				return err
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(data)
}

func (c *TestClient) EnsuerWriteJSON(data any) {
	require.NoError(c.t, c.WriteJSON(data), "Could not write JSON %+v", data)
}

func (c *TestClient) SendHello(userid string) error {
	return c.SendHelloV1(userid)
}

func (c *TestClient) SendHelloV1(userid string) error {
	params := TestBackendClientAuthParams{
		UserId: userid,
	}
	return c.SendHelloParams(c.server.URL, api.HelloVersionV1, "", nil, params)
}

func (c *TestClient) SendHelloV2(userid string) error {
	return c.SendHelloV2WithFeatures(userid, nil)
}

func (c *TestClient) SendHelloV2WithFeatures(userid string, features []string) error {
	now := time.Now()
	return c.SendHelloV2WithTimesAndFeatures(userid, now, now.Add(time.Minute), features)
}

func (c *TestClient) CreateHelloV2TokenWithUserdata(userid string, issuedAt time.Time, expiresAt time.Time, userdata api.StringMap) (string, error) {
	data, err := json.Marshal(userdata)
	if err != nil {
		return "", err
	}

	claims := &api.HelloV2TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  c.server.URL,
			Subject: userid,
		},
		UserData: data,
	}
	if !issuedAt.IsZero() {
		claims.IssuedAt = jwt.NewNumericDate(issuedAt)
	}
	if !expiresAt.IsZero() {
		claims.ExpiresAt = jwt.NewNumericDate(expiresAt)
	}

	var token *jwt.Token
	if strings.Contains(c.t.Name(), "ECDSA") {
		token = jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	} else if strings.Contains(c.t.Name(), "Ed25519") {
		token = jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	} else {
		token = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	}
	private := getPrivateAuthToken(c.t)
	return token.SignedString(private)
}

func (c *TestClient) CreateHelloV2Token(userid string, issuedAt time.Time, expiresAt time.Time) (string, error) {
	userdata := api.StringMap{
		"displayname": "Displayname " + userid,
	}

	return c.CreateHelloV2TokenWithUserdata(userid, issuedAt, expiresAt, userdata)
}

func (c *TestClient) SendHelloV2WithTimes(userid string, issuedAt time.Time, expiresAt time.Time) error {
	return c.SendHelloV2WithTimesAndFeatures(userid, issuedAt, expiresAt, nil)
}

func (c *TestClient) SendHelloV2WithTimesAndFeatures(userid string, issuedAt time.Time, expiresAt time.Time, features []string) error {
	tokenString, err := c.CreateHelloV2Token(userid, issuedAt, expiresAt)
	require.NoError(c.t, err)

	params := api.HelloV2AuthParams{
		Token: tokenString,
	}
	return c.SendHelloParams(c.server.URL, api.HelloVersionV2, "", features, params)
}

func (c *TestClient) SendHelloResume(resumeId api.PrivateSessionId) error {
	hello := &api.ClientMessage{
		Id:   "1234",
		Type: "hello",
		Hello: &api.HelloClientMessage{
			Version:  api.HelloVersionV1,
			ResumeId: resumeId,
		},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendHelloClient(userid string) error {
	return c.SendHelloClientWithFeatures(userid, nil)
}

func (c *TestClient) SendHelloClientWithFeatures(userid string, features []string) error {
	params := TestBackendClientAuthParams{
		UserId: userid,
	}
	return c.SendHelloParams(c.server.URL, api.HelloVersionV1, "client", features, params)
}

func (c *TestClient) SendHelloInternal() error {
	return c.SendHelloInternalWithFeatures(nil)
}

func (c *TestClient) SendHelloInternalWithFeatures(features []string) error {
	random := internal.RandomString(48)
	mac := hmac.New(sha256.New, testInternalSecret)
	mac.Write([]byte(random)) // nolint
	token := hex.EncodeToString(mac.Sum(nil))
	backend := c.server.URL

	params := api.ClientTypeInternalAuthParams{
		Random:  random,
		Token:   token,
		Backend: backend,
	}
	return c.SendHelloParams("", api.HelloVersionV1, "internal", features, params)
}

func (c *TestClient) SendHelloParams(url string, version string, clientType api.ClientType, features []string, params any) error {
	data, err := json.Marshal(params)
	require.NoError(c.t, err)

	hello := &api.ClientMessage{
		Id:   "1234",
		Type: "hello",
		Hello: &api.HelloClientMessage{
			Version:  version,
			Features: features,
			Auth: &api.HelloClientMessageAuth{
				Type:   clientType,
				Url:    url,
				Params: data,
			},
		},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendBye() error {
	hello := &api.ClientMessage{
		Id:   "9876",
		Type: "bye",
		Bye:  &api.ByeClientMessage{},
	}
	return c.WriteJSON(hello)
}

func (c *TestClient) SendMessage(recipient api.MessageClientMessageRecipient, data any) error {
	payload, err := json.Marshal(data)
	require.NoError(c.t, err)

	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "message",
		Message: &api.MessageClientMessage{
			Recipient: recipient,
			Data:      payload,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SendControl(recipient api.MessageClientMessageRecipient, data any) error {
	payload, err := json.Marshal(data)
	require.NoError(c.t, err)

	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "control",
		Control: &api.ControlClientMessage{
			MessageClientMessage: api.MessageClientMessage{
				Recipient: recipient,
				Data:      payload,
			},
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SendInternalAddSession(msg *api.AddSessionInternalClientMessage) error {
	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "internal",
		Internal: &api.InternalClientMessage{
			Type:       "addsession",
			AddSession: msg,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SendInternalUpdateSession(msg *api.UpdateSessionInternalClientMessage) error {
	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "internal",
		Internal: &api.InternalClientMessage{
			Type:          "updatesession",
			UpdateSession: msg,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SendInternalRemoveSession(msg *api.RemoveSessionInternalClientMessage) error {
	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "internal",
		Internal: &api.InternalClientMessage{
			Type:          "removesession",
			RemoveSession: msg,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SendInternalDialout(msg *api.DialoutInternalClientMessage) error {
	message := &api.ClientMessage{
		Id:   "abcd",
		Type: "internal",
		Internal: &api.InternalClientMessage{
			Type:    "dialout",
			Dialout: msg,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) SetTransientData(key string, value any, ttl time.Duration) error {
	payload, err := json.Marshal(value)
	require.NoError(c.t, err)

	message := &api.ClientMessage{
		Id:   "efgh",
		Type: "transient",
		TransientData: &api.TransientDataClientMessage{
			Type:  "set",
			Key:   key,
			Value: payload,
			TTL:   ttl,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) RemoveTransientData(key string) error {
	message := &api.ClientMessage{
		Id:   "ijkl",
		Type: "transient",
		TransientData: &api.TransientDataClientMessage{
			Type: "remove",
			Key:  key,
		},
	}
	return c.WriteJSON(message)
}

func (c *TestClient) GetPendingMessages(ctx context.Context) ([]*api.ServerMessage, error) {
	var result []*api.ServerMessage
	select {
	case err := <-c.readErrorChan:
		return nil, err
	case msg := <-c.messageChan:
		var m api.ServerMessage
		if err := json.Unmarshal(msg, &m); err != nil {
			return nil, err
		}
		result = append(result, &m)
		n := len(c.messageChan)
		for range n {
			var m api.ServerMessage
			msg = <-c.messageChan
			if err := json.Unmarshal(msg, &m); err != nil {
				return nil, err
			}
			result = append(result, &m)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return result, nil
}

func (c *TestClient) RunUntilClosed(ctx context.Context) bool {
	select {
	case err := <-c.readErrorChan:
		if c.assert.Error(err) && websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
			return true
		}

		c.assert.NoError(err, "Received unexpected error")
	case msg := <-c.messageChan:
		var m api.ServerMessage
		if err := json.Unmarshal(msg, &m); c.assert.NoError(err, "error decoding received message") {
			c.assert.Fail("Server should have closed the connection", "received %+v", m)
		}
	case <-ctx.Done():
		c.assert.NoError(ctx.Err(), "error while waiting for closed connection")
	}
	return false
}

func (c *TestClient) RunUntilErrorIs(ctx context.Context, targets ...error) bool {
	var err error
	select {
	case err = <-c.readErrorChan:
	case msg := <-c.messageChan:
		var m api.ServerMessage
		if err := json.Unmarshal(msg, &m); c.assert.NoError(err, "error decoding received message") {
			c.assert.Fail("received message", "expected one of errors %+v, got message %+v", targets, m)
		}
		return false
	case <-ctx.Done():
		err = ctx.Err()
	}

	if c.assert.Error(err, "expected one of errors %+v", targets) {
		for _, t := range targets {
			if errors.Is(err, t) {
				return true
			}
		}

		c.assert.Fail("invalid error", "expected one of errors %+v, got %s", targets, err)
	}

	return false
}

func (c *TestClient) RunUntilMessage(ctx context.Context) (*api.ServerMessage, bool) {
	select {
	case err := <-c.readErrorChan:
		c.assert.NoError(err, "error reading while waiting for message")
		return nil, false
	case msg := <-c.messageChan:
		var m api.ServerMessage
		if err := json.Unmarshal(msg, &m); c.assert.NoError(err, "error decoding received message") {
			return &m, true
		}
	case <-ctx.Done():
		c.assert.NoError(ctx.Err(), "error while waiting for message")
	}
	return nil, false
}

func (c *TestClient) RunUntilMessageOrClosed(ctx context.Context) (*api.ServerMessage, bool) {
	select {
	case err := <-c.readErrorChan:
		if c.assert.Error(err) && websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
			return nil, true
		}

		c.assert.NoError(err, "Received unexpected error")
		return nil, false
	case msg := <-c.messageChan:
		var m api.ServerMessage
		if err := json.Unmarshal(msg, &m); c.assert.NoError(err, "error decoding received message") {
			return &m, true
		}
	case <-ctx.Done():
		c.assert.NoError(ctx.Err(), "error while waiting for message")
	}
	return nil, false
}

func (c *TestClient) RunUntilError(ctx context.Context, code string) (*api.Error, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok ||
		!checkMessageType(c.t, message, "error") ||
		!c.assert.Equal(code, message.Error.Code, "invalid error code in %+v", message) {
		return nil, false
	}

	return message.Error, true
}

func (c *TestClient) RunUntilHello(ctx context.Context) (*api.ServerMessage, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok ||
		!checkMessageType(c.t, message, "hello") {
		return nil, false
	}

	c.publicId = message.Hello.SessionId
	return message, true
}

func (c *TestClient) JoinRoom(ctx context.Context, roomId string) (*api.ServerMessage, bool) {
	return c.JoinRoomWithRoomSession(ctx, roomId, api.RoomSessionId(fmt.Sprintf("%s-%s", roomId, c.publicId)))
}

func (c *TestClient) JoinRoomWithRoomSession(ctx context.Context, roomId string, roomSessionId api.RoomSessionId) (message *api.ServerMessage, ok bool) {
	msg := &api.ClientMessage{
		Id:   "ABCD",
		Type: "room",
		Room: &api.RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomSessionId,
		},
	}
	if err := c.WriteJSON(msg); !c.assert.NoError(err) {
		return nil, false
	}

	if message, ok = c.RunUntilMessage(ctx); !ok ||
		!checkMessageType(c.t, message, "room") ||
		!c.assert.Equal(msg.Id, message.Id, "invalid message id in %+v", message) {
		return nil, false
	}

	return message, true
}

func checkMessageRoomId(t *testing.T, message *api.ServerMessage, roomId string) bool {
	return checkMessageType(t, message, "room") &&
		assert.Equal(t, roomId, message.Room.RoomId, "invalid room id in %+v", message)
}

func (c *TestClient) RunUntilRoom(ctx context.Context, roomId string) bool {
	message, ok := c.RunUntilMessage(ctx)
	return ok && checkMessageRoomId(c.t, message, roomId)
}

func (c *TestClient) checkMessageJoined(message *api.ServerMessage, hello *api.HelloServerMessage) bool {
	return c.checkMessageJoinedSession(message, hello.SessionId, hello.UserId)
}

func (c *TestClient) checkSingleMessageJoined(message *api.ServerMessage) bool {
	return checkMessageType(c.t, message, "event") &&
		c.assert.Equal("room", message.Event.Target, "invalid event target in %+v", message) &&
		c.assert.Equal("join", message.Event.Type, "invalid event type in %+v", message) &&
		c.assert.Len(message.Event.Join, 1, "invalid number of join event entries in %+v", message)
}

func (c *TestClient) checkMessageJoinedSession(message *api.ServerMessage, sessionId api.PublicSessionId, userId string) bool {
	if !c.checkSingleMessageJoined(message) {
		return false
	}

	failed := false
	evt := message.Event.Join[0]
	if sessionId != "" {
		if !c.assert.Equal(sessionId, evt.SessionId, "invalid join session id: expected %+v, got %+v in %+v",
			getPubliceSessionIdData(c.hub, sessionId),
			getPubliceSessionIdData(c.hub, evt.SessionId),
			message,
		) {
			failed = true
		}
	}
	if !c.assert.Equal(userId, evt.UserId, "invalid user id in %+v", evt) {
		failed = true
	}
	return !failed
}

func (c *TestClient) RunUntilJoinedAndReturn(ctx context.Context, hello ...*api.HelloServerMessage) ([]api.EventServerMessageSessionEntry, []*api.ServerMessage, bool) {
	received := make([]api.EventServerMessageSessionEntry, len(hello))
	var ignored []*api.ServerMessage
	hellos := make(map[*api.HelloServerMessage]int, len(hello))
	for idx, h := range hello {
		hellos[h] = idx
	}
	for len(hellos) > 0 {
		message, ok := c.RunUntilMessage(ctx)
		if !ok {
			return nil, nil, false
		}

		if message.Type != "event" || message.Event == nil {
			ignored = append(ignored, message)
			continue
		} else if message.Event.Target != "room" || message.Event.Type != "join" {
			ignored = append(ignored, message)
			continue
		}

		if !checkMessageType(c.t, message, "event") {
			continue
		}

		for len(message.Event.Join) > 0 {
			found := false
		loop:
			for h, idx := range hellos {
				for idx2, evt := range message.Event.Join {
					if evt.SessionId == h.SessionId && evt.UserId == h.UserId {
						received[idx] = evt
						delete(hellos, h)
						message.Event.Join = append(message.Event.Join[:idx2], message.Event.Join[idx2+1:]...)
						found = true
						break loop
					}
				}
			}
			if !c.assert.True(found, "expected one of the passed hello sessions, got %+v", message.Event.Join) {
				break
			}
		}
	}
	return received, ignored, true
}

func (c *TestClient) RunUntilJoined(ctx context.Context, hello ...*api.HelloServerMessage) bool {
	_, unexpected, ok := c.RunUntilJoinedAndReturn(ctx, hello...)
	return ok && c.assert.Empty(unexpected, "Received unexpected messages: %+v", unexpected)
}

func (c *TestClient) checkMessageRoomLeave(message *api.ServerMessage, hello *api.HelloServerMessage) bool {
	return c.checkMessageRoomLeaveSession(message, hello.SessionId)
}

func (c *TestClient) checkMessageRoomLeaveSession(message *api.ServerMessage, sessionId api.PublicSessionId) bool {
	return checkMessageType(c.t, message, "event") &&
		c.assert.Equal("room", message.Event.Target, "invalid target in %+v", message) &&
		c.assert.Equal("leave", message.Event.Type, "invalid event type in %+v", message) &&
		c.assert.Len(message.Event.Leave, 1, "invalid number of leave event entries: %+v", message.Event) &&
		c.assert.Equal(sessionId, message.Event.Leave[0], "invalid leave session: expected %+v, got %+v in %+v",
			getPubliceSessionIdData(c.hub, sessionId),
			getPubliceSessionIdData(c.hub, message.Event.Leave[0]),
			message,
		)
}

func (c *TestClient) RunUntilLeft(ctx context.Context, hello *api.HelloServerMessage) bool {
	message, ok := c.RunUntilMessage(ctx)
	return ok && c.checkMessageRoomLeave(message, hello)
}

func checkMessageRoomlistUpdate(t *testing.T, message *api.ServerMessage) (*api.RoomEventServerMessage, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("roomlist", message.Event.Target, "invalid event target in %+v", message) ||
		!assert.Equal("update", message.Event.Type, "invalid event type in %+v", message) ||
		!assert.NotNil(message.Event.Update, "update missing in %+v", message) {
		return nil, false
	}

	return message.Event.Update, true
}

func (c *TestClient) RunUntilRoomlistUpdate(ctx context.Context) (*api.RoomEventServerMessage, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok {
		return nil, false
	}

	return checkMessageRoomlistUpdate(c.t, message)
}

func checkMessageRoomlistDisinvite(t *testing.T, message *api.ServerMessage) (*api.RoomDisinviteEventServerMessage, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("roomlist", message.Event.Target, "invalid event target in %+v", message) ||
		!assert.Equal("disinvite", message.Event.Type, "invalid event type in %+v", message) ||
		!assert.NotNil(message.Event.Disinvite, "disinvite missing in %+v", message) {
		return nil, false
	}

	return message.Event.Disinvite, true
}

func (c *TestClient) RunUntilRoomlistDisinvite(ctx context.Context) (*api.RoomDisinviteEventServerMessage, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok {
		return nil, false
	}

	return checkMessageRoomlistDisinvite(c.t, message)
}

func checkMessageParticipantsInCall(t *testing.T, message *api.ServerMessage) (*api.RoomEventServerMessage, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("participants", message.Event.Target, "invalid event target in %+v", message) ||
		!assert.Equal("update", message.Event.Type, "invalid event type in %+v", message) ||
		!assert.NotNil(message.Event.Update, "update missing in %+v", message) {
		return nil, false
	}

	return message.Event.Update, true
}

func checkMessageParticipantFlags(t *testing.T, message *api.ServerMessage) (*api.RoomFlagsServerMessage, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("participants", message.Event.Target, "invalid event target in %+v", message) ||
		!assert.Equal("flags", message.Event.Type, "invalid event type in %+v", message) ||
		!assert.NotNil(message.Event.Flags, "flags missing in %+v", message) {
		return nil, false
	}

	return message.Event.Flags, true
}

func checkMessageRoomMessage(t *testing.T, message *api.ServerMessage) (*api.RoomEventMessage, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("room", message.Event.Target, "invalid event target in %+v", message) ||
		!assert.Equal("message", message.Event.Type, "invalid event type in %+v", message) ||
		!assert.NotNil(message.Event.Message, "message missing in %+v", message) {
		return nil, false
	}

	return message.Event.Message, true
}

func (c *TestClient) RunUntilRoomMessage(ctx context.Context) (*api.RoomEventMessage, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok {
		return nil, false
	}

	return checkMessageRoomMessage(c.t, message)
}

func checkMessageError(t *testing.T, message *api.ServerMessage, msgid string) bool {
	return checkMessageType(t, message, "error") &&
		assert.Equal(t, msgid, message.Error.Code, "invalid error code in %+v", message)
}

func (c *TestClient) RunUntilOffer(ctx context.Context, offer string) bool {
	message, ok := c.RunUntilMessage(ctx)
	if !ok || !checkMessageType(c.t, message, "message") {
		return false
	}

	var data api.StringMap
	if err := json.Unmarshal(message.Message.Data, &data); !c.assert.NoError(err) {
		return false
	}

	if dt, ok := api.GetStringMapEntry[string](data, "type"); !c.assert.True(ok, "no/invalid type in %+v", data) ||
		!c.assert.Equal("offer", dt, "invalid data type in %+v", data) {
		return false
	}

	if payload, ok := api.ConvertStringMap(data["payload"]); !c.assert.True(ok, "not a string map, got %+v", data["payload"]) {
		return false
	} else {
		if pt, ok := api.GetStringMapEntry[string](payload, "type"); !c.assert.True(ok, "no/invalid type in payload %+v", payload) ||
			!c.assert.Equal("offer", pt, "invalid payload type in %+v", payload) {
			return false
		}
		if sdp, ok := api.GetStringMapEntry[string](payload, "sdp"); !c.assert.True(ok, "no/invalid sdp in payload %+v", payload) ||
			!c.assert.Equal(offer, sdp, "invalid payload offer") {
			return false
		}
	}

	return true
}

func (c *TestClient) RunUntilAnswer(ctx context.Context, answer string) bool {
	return c.RunUntilAnswerFromSender(ctx, answer, nil)
}

func (c *TestClient) RunUntilAnswerFromSender(ctx context.Context, answer string, sender *api.MessageServerMessageSender) bool {
	message, ok := c.RunUntilMessage(ctx)
	if !ok || !checkMessageType(c.t, message, "message") {
		return false
	}

	if sender != nil {
		if !checkMessageSender(c.t, c.hub, message.Message.Sender, sender.Type, &api.HelloServerMessage{
			SessionId: sender.SessionId,
			UserId:    sender.UserId,
		}) {
			return false
		}
	}

	var data api.StringMap
	if err := json.Unmarshal(message.Message.Data, &data); !c.assert.NoError(err) {
		return false
	}

	if dt, ok := api.GetStringMapEntry[string](data, "type"); !c.assert.True(ok, "no/invalid type in %+v", data) ||
		!c.assert.Equal("answer", dt, "invalid data type in %+v", data) {
		return false
	}

	if payload, ok := api.ConvertStringMap(data["payload"]); !c.assert.True(ok, "not a string map, got %+v", data["payload"]) {
		return false
	} else {
		if pt, ok := api.GetStringMapEntry[string](payload, "type"); !c.assert.True(ok, "no/invalid type in payload %+v", payload) ||
			!c.assert.Equal("answer", pt, "invalid payload type in %+v", payload) {
			return false
		}
		if sdp, ok := api.GetStringMapEntry[string](payload, "sdp"); !c.assert.True(ok, "no/invalid sdp in payload %+v", payload) ||
			!c.assert.Equal(answer, sdp, "invalid payload answer") {
			return false
		}
	}

	return true
}

func checkMessageTransientInitialOrSet(t *testing.T, message *api.ServerMessage, key string, value any) bool {
	assert := assert.New(t)
	return checkMessageType(t, message, "transient") &&
		assert.True(message.TransientData.Type == "initial" || message.TransientData.Type == "set", "invalid message type in %+v", message) &&
		assert.Equal(key, message.TransientData.Key, "invalid key in %+v", message) &&
		assert.EqualValues(value, message.TransientData.Value, "invalid value in %+v", message) &&
		assert.Nil(message.TransientData.OldValue, "invalid old value in %+v", message)
}

func checkMessageTransientSet(t *testing.T, message *api.ServerMessage, key string, value any, oldValue any) bool {
	assert := assert.New(t)
	return checkMessageType(t, message, "transient") &&
		assert.Equal("set", message.TransientData.Type, "invalid message type in %+v", message) &&
		assert.Equal(key, message.TransientData.Key, "invalid key in %+v", message) &&
		assert.EqualValues(value, message.TransientData.Value, "invalid value in %+v", message) &&
		assert.EqualValues(oldValue, message.TransientData.OldValue, "invalid old value in %+v", message)
}

func checkMessageTransientRemove(t *testing.T, message *api.ServerMessage, key string, oldValue any) bool {
	assert := assert.New(t)
	return checkMessageType(t, message, "transient") &&
		assert.Equal("remove", message.TransientData.Type, "invalid message type in %+v", message) &&
		assert.Equal(key, message.TransientData.Key, "invalid key in %+v", message) &&
		assert.EqualValues(oldValue, message.TransientData.OldValue, "invalid old value in %+v", message)
}

func checkMessageTransientInitial(t *testing.T, message *api.ServerMessage, data api.StringMap) bool {
	assert := assert.New(t)
	return checkMessageType(t, message, "transient") &&
		assert.Equal("initial", message.TransientData.Type, "invalid message type in %+v", message) &&
		assert.Equal(data, message.TransientData.Data, "invalid initial data in %+v", message)
}

func checkMessageInCallAll(t *testing.T, message *api.ServerMessage, roomId string, inCall int) bool {
	assert := assert.New(t)
	return checkMessageType(t, message, "event") &&
		assert.Equal("update", message.Event.Type, "invalid event type, got %+v", message.Event) &&
		assert.Equal("participants", message.Event.Target, "invalid event target, got %+v", message.Event) &&
		assert.Equal(roomId, message.Event.Update.RoomId, "invalid event update room id, got %+v", message.Event) &&
		assert.True(message.Event.Update.All, "expected participants update event for all, got %+v", message.Event) &&
		assert.EqualValues(strconv.FormatInt(int64(inCall), 10), message.Event.Update.InCall, "expected incall flags %d, got %+v", inCall, message.Event.Update)
}

func checkMessageSwitchTo(t *testing.T, message *api.ServerMessage, roomId string, details json.RawMessage) (*api.EventServerMessageSwitchTo, bool) {
	assert := assert.New(t)
	if !checkMessageType(t, message, "event") ||
		!assert.Equal("switchto", message.Event.Type, "invalid event type, got %+v", message.Event) ||
		!assert.Equal("room", message.Event.Target, "invalid event target, got %+v", message.Event) ||
		!assert.Equal(roomId, message.Event.SwitchTo.RoomId, "invalid event switchto room id, got %+v", message.Event) {
		return nil, false
	}
	if details != nil {
		if !assert.NotEmpty(message.Event.SwitchTo.Details, "details missing in %+v", message) ||
			!assert.Equal(details, message.Event.SwitchTo.Details, "invalid details, got %+v", message.Event) {
			return nil, false
		}
	} else if assert.Empty(message.Event.SwitchTo.Details, "expected no details in %+v", message) {
		return nil, false
	}
	return message.Event.SwitchTo, true
}

func (c *TestClient) RunUntilSwitchTo(ctx context.Context, roomId string, details json.RawMessage) (*api.EventServerMessageSwitchTo, bool) {
	message, ok := c.RunUntilMessage(ctx)
	if !ok {
		return nil, false
	}

	return checkMessageSwitchTo(c.t, message, roomId, details)
}
