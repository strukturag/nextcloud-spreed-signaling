/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 LibreCode Coop
 *
 * @license GNU AGPL version 3 or any later version
 */
package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// A temporarily delayed response to a heartbeat should not expire an
// otherwise healthy, idle connection. The 20-second delay matches a class
// of mobile/browser stalls; it is not a change to the 60-second timeout.
func TestHeartbeatAllowsIntermittentDelayedPong(t *testing.T) {
	previousPong := time.Millisecond
	delayedPong := 20 * time.Second
	nextPongAt := pingPeriod + delayedPong
	readDeadline := previousPong + pongWait

	require.Less(t, pingPeriod, pongWait)
	require.Less(t, nextPongAt, readDeadline)
}

func TestHeartbeatSlowPongDoesNotExpireWebSocket(t *testing.T) {
	// Scale the production policy down, preserving its interval/timeout ratio.
	// A single large second-pong delay distinguishes the former 90% interval
	// from a 50% interval with a healthy 60-second application timeout.
	const timeout = 4 * time.Second
	const secondPongDelay = 1300 * time.Millisecond
	interval := time.Duration(float64(timeout) * float64(pingPeriod) / float64(pongWait))

	var readTimedOut atomic.Bool
	var sentPings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		ws.SetReadDeadline(time.Now().Add(timeout)) // nolint
		ws.SetPongHandler(func(string) error {
			return ws.SetReadDeadline(time.Now().Add(timeout)) // nolint
		})

		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
				return
			}
			for range ticker.C {
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
					return
				}
			}
		}()

		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				if strings.Contains(err.Error(), "i/o timeout") {
					readTimedOut.Store(true)
				}
				return
			}
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
	require.NoError(t, err)
	defer conn.Close()
	conn.SetPingHandler(func(data string) error {
		if sentPings.Add(1) == 2 {
			time.Sleep(secondPongDelay)
		}
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	time.Sleep(5400 * time.Millisecond)
	require.GreaterOrEqual(t, sentPings.Load(), int32(2))
	require.False(t, readTimedOut.Load(), "the late pong must not expire a healthy connection")
}
