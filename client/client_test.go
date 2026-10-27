/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
)

func TestCounterWriter(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	var b bytes.Buffer
	var written int
	w := &counterWriter{
		w:       &b,
		counter: &written,
	}
	if count, err := w.Write(nil); assert.NoError(err) && assert.Equal(0, count) {
		assert.Equal(0, written)
	}
	if count, err := w.Write([]byte("foo")); assert.NoError(err) && assert.Equal(3, count) {
		assert.Equal(3, written)
	}
}

type serverClient struct {
	Client

	t       *testing.T
	handler *testHandler

	id            string
	received      atomic.Uint32
	sessionClosed atomic.Bool
}

func newTestClient(h *testHandler, r *http.Request, conn *websocket.Conn, id uint64) *serverClient {
	result := &serverClient{
		t:       h.t,
		handler: h,
		id:      fmt.Sprintf("session-%d", id),
	}

	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}

	logger := logtest.NewLoggerForTest(h.t)
	ctx := log.NewLoggerContext(r.Context(), logger)
	result.SetConn(ctx, conn, addr, r.Header.Get("User-Agent"), false, result)
	return result
}

func (c *serverClient) WaitReceived(ctx context.Context, count uint32) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		} else if c.received.Load() >= count {
			return nil
		}

		time.Sleep(time.Millisecond)
	}
}

func (c *serverClient) GetSessionId() api.PublicSessionId {
	return api.PublicSessionId(c.id)
}

func (c *serverClient) OnClosed() {
	c.Close()
	c.handler.removeClient(c)
}

func (c *serverClient) OnMessageReceived(message []byte) {
	switch c.received.Add(1) {
	case 1:
		var s string
		if err := json.Unmarshal(message, &s); assert.NoError(c.t, err) {
			assert.Equal(c.t, "Hello world!", s)
			c.sendPing()
			assert.EqualValues(c.t, "DE", c.Country())
			assert.False(c.t, c.Client.IsInRoom("room-id"))
			c.SendMessage(&api.ServerMessage{
				Type: "welcome",
				Welcome: &api.WelcomeServerMessage{
					Version: "1.0",
				},
			})
		}
	case 2:
		var s string
		if err := json.Unmarshal(message, &s); assert.NoError(c.t, err) {
			assert.Equal(c.t, "Send error", s)
			c.SendError(api.NewError("test_error", "This is a test error."))
		}
	case 3:
		var s string
		if err := json.Unmarshal(message, &s); assert.NoError(c.t, err) {
			assert.Equal(c.t, "Send bye", s)
			c.SendByeResponseWithReason(nil, "Go away!")
		}
	}
}

func (c *serverClient) OnRTTReceived(rtt time.Duration) {

}

func (c *serverClient) OnLookupCountry(addr string) geoip.Country {
	return "DE"
}

func (c *serverClient) IsInRoom(roomId string) bool {
	return false
}

func (c *serverClient) IsPendingCloseRoom(roomId string) bool {
	return false
}

func (c *serverClient) CloseSession() {
	if c.sessionClosed.Swap(true) {
		assert.Fail(c.t, "session closed more than once")
	}
}

type testHandler struct {
	mu sync.Mutex

	t *testing.T

	upgrader websocket.Upgrader

	id atomic.Uint64
	// +checklocks:mu
	activeClients map[string]*serverClient
	// +checklocks:mu
	allClients []*serverClient
}

func newTestHandler(t *testing.T) *testHandler {
	return &testHandler{
		t:             t,
		activeClients: make(map[string]*serverClient),
	}
}

func (h *testHandler) addClient(client *serverClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.activeClients[client.id] = client
	h.allClients = append(h.allClients, client)
}

func (h *testHandler) removeClient(client *serverClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.activeClients, client.id)
}

func (h *testHandler) getClients() []*serverClient {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.allClients)
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if !assert.NoError(h.t, err) {
		return
	}

	id := h.id.Add(1)
	client := newTestClient(h, r, conn, id)
	h.addClient(client)

	closed := make(chan struct{})
	context.AfterFunc(client.Context(), func() {
		close(closed)
	})

	go client.WritePump()
	client.ReadPump()
	<-closed
}

type localClient struct {
	t *testing.T

	conn *websocket.Conn
}

func newLocalClient(t *testing.T, url string) *localClient {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	require.NoError(t, err)
	return &localClient{
		t: t,

		conn: conn,
	}
}

func (c *localClient) Close() error {
	err := c.conn.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

func (c *localClient) WriteJSON(v any) error {
	return c.conn.WriteJSON(v)
}

func (c *localClient) Write(v []byte) error {
	return c.conn.WriteMessage(websocket.BinaryMessage, v)
}

func (c *localClient) ReadJSON(v any) error {
	return c.conn.ReadJSON(v)
}

func TestClient(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	serverHandler := newTestHandler(t)

	server := httptest.NewServer(serverHandler)
	t.Cleanup(func() {
		server.Close()
	})

	client := newLocalClient(t, strings.ReplaceAll(server.URL, "http://", "ws://"))
	t.Cleanup(func() {
		assert.NoError(client.Close())
	})

	var msg api.ServerMessage

	require.NoError(client.WriteJSON("Hello world!"))
	if assert.NoError(client.ReadJSON(&msg)) &&
		assert.Equal("welcome", msg.Type) &&
		assert.NotNil(msg.Welcome) {
		assert.Equal("1.0", msg.Welcome.Version)
	}
	if clients := serverHandler.getClients(); assert.Len(clients, 1) {
		assert.False(clients[0].sessionClosed.Load())
		assert.EqualValues(1, clients[0].received.Load())
	}

	require.NoError(client.Write([]byte("Hello world!")))
	if assert.NoError(client.ReadJSON(&msg)) &&
		assert.Equal("error", msg.Type) &&
		assert.NotNil(msg.Error) {
		assert.Equal("invalid_format", msg.Error.Code)
		assert.Equal("Invalid data format.", msg.Error.Message)
	}

	require.NoError(client.WriteJSON("Send error"))
	if assert.NoError(client.ReadJSON(&msg)) &&
		assert.Equal("error", msg.Type) &&
		assert.NotNil(msg.Error) {
		assert.Equal("test_error", msg.Error.Code)
		assert.Equal("This is a test error.", msg.Error.Message)
	}
	if clients := serverHandler.getClients(); assert.Len(clients, 1) {
		assert.False(clients[0].sessionClosed.Load())
		assert.EqualValues(2, clients[0].received.Load())
	}

	require.NoError(client.WriteJSON("Send bye"))
	if assert.NoError(client.ReadJSON(&msg)) &&
		assert.Equal("bye", msg.Type) &&
		assert.NotNil(msg.Bye) {
		assert.Equal("Go away!", msg.Bye.Reason)
	}
	if clients := serverHandler.getClients(); assert.Len(clients, 1) {
		assert.EqualValues(3, clients[0].received.Load())
	}

	// Sending a "bye" will close the connection.
	var we *websocket.CloseError
	if err := client.ReadJSON(&msg); assert.ErrorAs(err, &we) {
		assert.Equal(websocket.CloseNormalClosure, we.Code)
		assert.Empty(we.Text)
	}
	if clients := serverHandler.getClients(); assert.Len(clients, 1) {
		assert.True(clients[0].sessionClosed.Load())
		assert.EqualValues(3, clients[0].received.Load())
	}
}

// newHeartbeatTestServer uses the production Client read/write pump
// implementations with shorter test-only intervals. It neither mocks
// their WebSocket traffic nor changes global production constants.
func newHeartbeatTestServer(t *testing.T, timeout, interval time.Duration) (string, <-chan *serverClient, <-chan struct{}) {
	t.Helper()

	h := newTestHandler(t)
	ready := make(chan *serverClient, 1)
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}

		client := newTestClient(h, r, conn, h.id.Add(1))
		h.addClient(client)
		ready <- client

		go client.writePump(interval)
		client.readPump(timeout)
		close(closed)
	}))
	t.Cleanup(server.Close)
	return strings.ReplaceAll(server.URL, "http://", "ws://"), ready, closed
}

func TestClientHeartbeatDelayedPong(t *testing.T) {
	const timeout = 4 * time.Second
	const delayedPong = 1300 * time.Millisecond

	// Preserve production's ping/read-timeout ratio with a short test
	// timeout. At the previous 90% interval this delayed pong arrives
	// after the read deadline; at 50% it arrives beforehand.
	interval := time.Duration(float64(timeout) * float64(pingPeriod) / float64(pongWait))
	url, ready, closed := newHeartbeatTestServer(t, timeout, interval)
	client := newLocalClient(t, url)
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Error("signaling read pump did not stop after client disconnect")
		}
	})

	serverClient := <-ready
	var pings atomic.Int32
	client.conn.SetPingHandler(func(message string) error {
		if pings.Add(1) == 2 {
			time.Sleep(delayedPong)
		}
		return client.conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := client.conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-closed:
		t.Fatal("signaling closed a recoverable connection after a delayed pong")
	case <-time.After(timeout + 600*time.Millisecond):
	}
	require.GreaterOrEqual(t, pings.Load(), int32(2), "the test must exercise a delayed second pong")
	require.True(t, serverClient.IsConnected())
	require.NoError(t, client.WriteJSON("Hello world!"))
	require.Eventually(t, func() bool {
		return serverClient.received.Load() == 1
	}, 2*time.Second, 10*time.Millisecond, "the connection should still deliver messages")
}

func TestClientHeartbeatMissingPongCloses(t *testing.T) {
	const timeout = 600 * time.Millisecond
	url, ready, closed := newHeartbeatTestServer(t, timeout, 200*time.Millisecond)
	client := newLocalClient(t, url)
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
	})
	serverClient := <-ready

	// Continue reading control frames, but deliberately do not answer pings.
	client.conn.SetPingHandler(func(string) error { return nil })
	go func() {
		for {
			if _, _, err := client.conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-closed:
	case <-time.After(3 * timeout):
		t.Fatal("an unresponsive peer must still expire at the read deadline")
	}
	require.False(t, serverClient.IsConnected())
}
