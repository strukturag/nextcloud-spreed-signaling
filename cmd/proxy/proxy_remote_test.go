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
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/client"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/proxy"
)

func (c *RemoteConnection) WaitForConnection(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only used in tests, so a busy-loop should be fine.
	for c.conn == nil || c.connectedSince.IsZero() || !c.helloReceived {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.mu.Unlock()
		time.Sleep(time.Nanosecond)
		c.mu.Lock()
	}

	return nil
}

func (c *RemoteConnection) WaitForDisconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	initial := c.conn
	if initial == nil {
		return nil
	}

	// Only used in tests, so a busy-loop should be fine.
	for c.conn == initial {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.mu.Unlock()
		time.Sleep(time.Nanosecond)
		c.mu.Lock()
	}
	return nil
}

func Test_ProxyRemoteConnectionReconnect(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, httpserver := newProxyServerForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, httpserver.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.SendBye())
		assert.NoError(conn.Close())
	})

	assert.NoError(conn.WaitForConnection(ctx))

	// Closing the connection will reconnect automatically
	conn.mu.Lock()
	c := conn.conn
	conn.mu.Unlock()
	assert.NoError(c.Close())
	assert.NoError(conn.WaitForDisconnect(ctx))
	assert.NoError(conn.WaitForConnection(ctx))
}

func getRemoteTimeoutsConfigForTest() (*goconf.ConfigFile, client.Timeouts) {
	config := goconf.NewConfigFile()
	config.AddOption("mcu", "pinginterval", "1")
	config.AddOption("mcu", "readtimeout", "1")
	config.AddOption("mcu", "writetimeout", "2")
	return config, client.Timeouts{
		PingInterval: time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: 2 * time.Second,
	}
}

func Test_ProxyRemoteConnectionTimeouts(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, httpserver := newProxyServerForTest(t)
	assert.Equal(client.Timeouts{}.WithDefaults(), server.getRemoteTimeouts())

	config, expected := getRemoteTimeoutsConfigForTest()
	require.NoError(server.loadConfig(config, true))
	assert.Equal(expected, server.getRemoteTimeouts())
	// Timeouts for remote connections don't affect the listener.
	assert.Equal(client.Timeouts{}.WithDefaults(), server.getTimeouts())

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, httpserver.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.SendBye())
		assert.NoError(conn.Close())
	})
	assert.Equal(expected, conn.timeouts)

	require.NoError(conn.WaitForConnection(ctx))
	conn.mu.Lock()
	initial := conn.conn
	conn.mu.Unlock()

	// The remote proxy responds to pings, so the connection is kept open
	// after the read timeout expired.
	time.Sleep(2 * (expected.PingInterval + expected.ReadTimeout))
	conn.mu.Lock()
	current := conn.conn
	conn.mu.Unlock()
	assert.Same(initial, current, "remote connection was reconnected")

	// Changing the timeouts only affects new connections.
	require.NoError(server.loadConfig(goconf.NewConfigFile(), true))
	assert.Equal(client.Timeouts{}.WithDefaults(), server.getRemoteTimeouts())
	assert.Equal(expected, conn.timeouts)
}

func Test_ProxyRemoteConnectionReadTimeout(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, _ := newProxyServerForTest(t)
	config, expected := getRemoteTimeoutsConfigForTest()
	require.NoError(server.loadConfig(config, true))

	// Remote proxy that never responds to pings.
	var connections atomic.Int32
	upgrader := websocket.Upgrader{}
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(err) {
			return
		}
		defer ws.Close()

		connections.Add(1)
		ws.SetPingHandler(func(string) error {
			return nil
		})
		for {
			if _, _, err := ws.NextReader(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(silent.Close)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, silent.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.Close())
	})

	// The connection is closed after the read timeout and reconnected.
	start := time.Now()
	for connections.Load() < 2 {
		require.NoError(ctx.Err())
		time.Sleep(10 * time.Millisecond)
	}
	assert.GreaterOrEqual(time.Since(start), expected.PingInterval+expected.ReadTimeout)
}

func Test_ProxyRemoteConnectionReconnectUnknownSession(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, httpserver := newProxyServerForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, httpserver.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.SendBye())
		assert.NoError(conn.Close())
	})

	assert.NoError(conn.WaitForConnection(ctx))

	// Closing the connection will reconnect automatically
	conn.mu.Lock()
	c := conn.conn
	sessionId := conn.sessionId
	conn.mu.Unlock()
	var sid uint64
	server.IterateSessions(func(session *ProxySession) {
		if session.PublicId() == sessionId {
			sid = session.Sid()
		}
	})
	require.NotEqualValues(0, sid)
	server.DeleteSession(sid)
	if err := c.Close(); err != nil {
		// If an error occurs while closing, it may only be "use of closed network
		// connection" because the "DeleteSession" might have already closed the
		// socket.
		assert.ErrorIs(err, net.ErrClosed)
	}
	assert.NoError(conn.WaitForDisconnect(ctx))
	assert.NoError(conn.WaitForConnection(ctx))
	assert.NotEqual(sessionId, conn.SessionId())
}

func Test_ProxyRemoteConnectionReconnectExpiredSession(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, httpserver := newProxyServerForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, httpserver.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.SendBye())
		assert.NoError(conn.Close())
	})

	assert.NoError(conn.WaitForConnection(ctx))

	// Closing the connection will reconnect automatically
	conn.mu.Lock()
	sessionId := conn.sessionId
	conn.mu.Unlock()
	var session *ProxySession
	server.IterateSessions(func(sess *ProxySession) {
		if sess.PublicId() == sessionId {
			session = sess
		}
	})
	require.NotNil(session)
	session.Close()
	assert.NoError(conn.WaitForDisconnect(ctx))
	assert.NoError(conn.WaitForConnection(ctx))
	assert.NotEqual(sessionId, conn.SessionId())
}

func Test_ProxyRemoteConnectionCreatePublisher(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	server, key, httpserver := newProxyServerForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := NewRemoteConnection(server, httpserver.URL, TokenIdForTest, key, nil)
	require.NoError(err)
	t.Cleanup(func() {
		assert.NoError(conn.SendBye())
		assert.NoError(conn.Close())
	})

	publisherId := "the-publisher"
	hostname := "the-hostname"
	port := 1234
	rtcpPort := 2345

	_, err = conn.RequestMessage(ctx, &proxy.ClientMessage{
		Type: "command",
		Command: &proxy.CommandClientMessage{
			Type:     "publish-remote",
			ClientId: publisherId,
			Hostname: hostname,
			Port:     port,
			RtcpPort: rtcpPort,
		},
	})
	assert.ErrorContains(err, UnknownClient.Error())
}
