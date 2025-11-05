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
package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestJanusEventsServerHandler struct {
	t        *testing.T
	upgrader websocket.Upgrader

	mcu  Mcu
	addr string
}

func (h *TestJanusEventsServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.t.Helper()
	require := require.New(h.t)
	conn, err := h.upgrader.Upgrade(w, r, nil)
	require.NoError(err)

	if conn.Subprotocol() == JanusEventsSubprotocol {
		addr := h.addr
		if addr == "" {
			addr = r.RemoteAddr
		}
		if host, _, err := net.SplitHostPort(addr); err == nil {
			addr = host
		}
		RunJanusEventsHandler(r.Context(), h.mcu, conn, addr, r.Header.Get("User-Agent"))
		return
	}

	deadline := time.Now().Add(time.Second)
	require.NoError(conn.SetWriteDeadline(deadline))
	require.NoError(conn.WriteJSON(map[string]string{"error": "invalid_subprotocol"}))
	require.NoError(conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseProtocolError, "invalid_subprotocol"), deadline))
	require.NoError(conn.Close())
}

func NewTestJanusEventsHandlerServer(t *testing.T) (*httptest.Server, string, *TestJanusEventsServerHandler) {
	t.Helper()

	handler := &TestJanusEventsServerHandler{
		t: t,
		upgrader: websocket.Upgrader{
			Subprotocols: []string{
				JanusEventsSubprotocol,
			},
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})
	url := strings.ReplaceAll(server.URL, "http://", "ws://")
	url = strings.ReplaceAll(url, "https://", "wss://")
	return server, url, handler
}

func TestJanusEventsHandlerNoMcu(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, _ := NewTestJanusEventsHandlerServer(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var ce *websocket.CloseError
	require.NoError(conn.SetReadDeadline(time.Now().Add(testTimeout)))
	if mt, msg, err := conn.ReadMessage(); err == nil {
		assert.Fail("connection was not closed", "expected close error, got message %s with type %d", string(msg), mt)
	} else if assert.ErrorAs(err, &ce) {
		assert.EqualValues(websocket.CloseInternalServerErr, ce.Code)
		assert.Equal("no mcu configured", ce.Text)
	}
}

func TestJanusEventsHandlerInvalidMcu(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, handler := NewTestJanusEventsHandlerServer(t)

	handler.mcu = &mcuProxy{}

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var ce *websocket.CloseError
	require.NoError(conn.SetReadDeadline(time.Now().Add(testTimeout)))
	if mt, msg, err := conn.ReadMessage(); err == nil {
		assert.Fail("connection was not closed", "expected close error, got message %s with type %d", string(msg), mt)
	} else if assert.ErrorAs(err, &ce) {
		assert.EqualValues(websocket.CloseInternalServerErr, ce.Code)
		assert.Equal("mcu does not support events", ce.Text)
	}
}

func TestJanusEventsHandlerPublicIP(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, handler := NewTestJanusEventsHandlerServer(t)

	handler.mcu = &mcuJanus{}
	handler.addr = "1.2.3.4"

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var ce *websocket.CloseError
	require.NoError(conn.SetReadDeadline(time.Now().Add(testTimeout)))
	if mt, msg, err := conn.ReadMessage(); err == nil {
		assert.Fail("connection was not closed", "expected close error, got message %s with type %d", string(msg), mt)
	} else if assert.ErrorAs(err, &ce) {
		assert.EqualValues(websocket.ClosePolicyViolation, ce.Code)
		assert.Equal("only loopback and private connections allowed", ce.Text)
	}
}

type TestMcuWithEvents struct {
	TestMCU

	t  *testing.T
	mu sync.Mutex
	// +checklocks:mu
	idx int
}

func (m *TestMcuWithEvents) UpdateBandwidth(handle uint64, media string, sent uint32, received uint32) {
	assert := assert.New(m.t)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.idx++
	switch m.idx {
	case 1:
		assert.EqualValues(1, handle)
		assert.EqualValues("audio", media)
		assert.EqualValues(100, sent)
		assert.EqualValues(200, received)
	case 2:
		assert.EqualValues(1, handle)
		assert.EqualValues("video", media)
		assert.EqualValues(200, sent)
		assert.EqualValues(300, received)
	default:
		assert.Fail("too many updates", "received update %d (handle=%d, media=%s, sent=%d, received=%d)", m.idx, handle, media, sent, received)
	}
}

func (m *TestMcuWithEvents) WaitForUpdates(ctx context.Context, waitForIdx int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		m.mu.Lock()
		idx := m.idx
		m.mu.Unlock()
		if idx == waitForIdx {
			return nil
		}

		time.Sleep(time.Millisecond)
	}
}

type janusEventSender struct {
	events []JanusEvent
}

func (s *janusEventSender) SendSingle(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	require := require.New(t)
	require.Len(s.events, 1)

	require.NoError(conn.WriteJSON(s.events[0]))
}

func (s *janusEventSender) Send(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	require := require.New(t)
	require.NoError(conn.WriteJSON(s.events))
}

func (s *janusEventSender) AddEvent(t *testing.T, eventType int, eventSubtype int, handleId uint64, event any) {
	t.Helper()

	require := require.New(t)
	assert := assert.New(t)
	data, err := json.Marshal(event)
	require.NoError(err)
	if s, ok := event.(fmt.Stringer); assert.True(ok, "%T should implement fmt.Stringer", event) {
		assert.Equal(s.String(), string(data))
	}

	message := JanusEvent{
		Type:     eventType,
		SubType:  eventSubtype,
		HandleId: handleId,
		Event:    data,
	}

	s.events = append(s.events, message)
}

func TestJanusEventsHandlerDifferentTypes(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, handler := NewTestJanusEventsHandlerServer(t)

	mcu := &TestMcuWithEvents{
		t: t,
	}
	handler.mcu = mcu

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var sender janusEventSender

	sender.AddEvent(
		t,
		JanusEventTypeSession,
		0,
		1,
		JanusEventSession{
			Name: "created",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeHandle,
		0,
		1,
		JanusEventHandle{
			Name: "attached",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeExternal,
		0,
		0,
		JanusEventExternal{
			Schema: "test-external",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeJSEP,
		0,
		1,
		JanusEventJSEP{
			Owner: "testing",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCICE,
		1,
		JanusEventWebRTCICE{
			ICE: "gathering",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCLocalCandidate,
		1,
		JanusEventWebRTCLocalCandidate{
			LocalCandidate: "invalid-candidate",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCRemoteCandidate,
		1,
		JanusEventWebRTCRemoteCandidate{
			RemoteCandidate: "invalid-candidate",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCSelectedPair,
		1,
		JanusEventWebRTCSelectedPair{
			SelectedPair: "invalid-pair",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCDTLS,
		1,
		JanusEventWebRTCDTLS{
			DTLS: "trying",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeWebRTC,
		JanusEventSubTypeWebRTCPeerConnection,
		1,
		JanusEventWebRTCPeerConnection{
			Connection: "webrtcup",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaState,
		1,
		JanusEventMediaState{
			Media: "audio",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaSlowLink,
		1,
		JanusEventMediaSlowLink{
			Media: "audio",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypePlugin,
		0,
		1,
		JanusEventPlugin{
			Plugin: "test-plugin",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeTransport,
		0,
		1,
		JanusEventTransport{
			Transport: "test-transport",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeCore,
		JanusEventSubTypeCoreStatusStartup,
		0,
		JanusEventCoreStartup{
			Status: "started",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeCore,
		JanusEventSubTypeCoreStatusStartup,
		0,
		JanusEventCoreStartup{
			Status: "update",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeCore,
		JanusEventSubTypeCoreStatusShutdown,
		0,
		JanusEventCoreShutdown{
			Status: "shutdown",
		},
	)

	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaStats,
		1,
		JanusEventMediaStats{
			Media:                "audio",
			BytesSentLastSec:     100,
			BytesReceivedLastSec: 200,
		},
	)
	sender.Send(t, conn)

	// Wait until all events are processed.
	assert.NoError(mcu.WaitForUpdates(ctx, 1))
}

func TestJanusEventsHandlerNotGrouped(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, handler := NewTestJanusEventsHandlerServer(t)

	mcu := &TestMcuWithEvents{
		t: t,
	}
	handler.mcu = mcu

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var sender janusEventSender
	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaStats,
		1,
		JanusEventMediaStats{
			Media:                "audio",
			BytesSentLastSec:     100,
			BytesReceivedLastSec: 200,
		},
	)
	sender.SendSingle(t, conn)
	assert.NoError(mcu.WaitForUpdates(ctx, 1))
}

func TestJanusEventsHandlerGrouped(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	_, url, handler := NewTestJanusEventsHandlerServer(t)

	mcu := &TestMcuWithEvents{
		t: t,
	}
	handler.mcu = mcu

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		Subprotocols: []string{
			JanusEventsSubprotocol,
		},
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	require.NoError(err)

	assert.Equal(JanusEventsSubprotocol, response.Header.Get("Sec-WebSocket-Protocol"))

	var sender janusEventSender
	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaStats,
		1,
		JanusEventMediaStats{
			Media:                "audio",
			BytesSentLastSec:     100,
			BytesReceivedLastSec: 200,
		},
	)
	sender.AddEvent(
		t,
		JanusEventTypeMedia,
		JanusEventSubTypeMediaStats,
		1,
		JanusEventMediaStats{
			Media:                "video",
			BytesSentLastSec:     200,
			BytesReceivedLastSec: 300,
		},
	)
	sender.Send(t, conn)

	assert.NoError(mcu.WaitForUpdates(ctx, 2))
}
