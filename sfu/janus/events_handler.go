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
package janus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/pool"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

const (
	EventsSubprotocol = "janus-events"

	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

var (
	bufferPool pool.BufferPool
)

type EventHandler interface {
	UpdateBandwidth(handle uint64, media string, sent api.Bandwidth, received api.Bandwidth)
}

type valueCounter struct {
	values map[string]uint64
}

func (c *valueCounter) Update(key string, value uint64) uint64 {
	if c.values == nil {
		c.values = make(map[string]uint64)
	}

	var delta uint64
	prev := c.values[key]
	if value == prev {
		return 0
	} else if value < prev {
		// Wrap around
		c.values[key] = 0
		delta = math.MaxUint64 - prev + value
	} else {
		delta = value - prev
	}

	c.values[key] += delta
	return delta
}

type handleStats struct {
	codecs map[string]string

	bytesReceived valueCounter
	bytesSent     valueCounter

	nacksReceived valueCounter
	nacksSent     valueCounter

	lostLocal  valueCounter
	lostRemote valueCounter

	retransmissionsReceived valueCounter
}

func (h *handleStats) Codec(media string, codec string) {
	if h.codecs == nil {
		h.codecs = make(map[string]string)
	}
	if h.codecs[media] != codec {
		statsJanusMediaCodecsTotal.WithLabelValues(media, codec).Inc()
		h.codecs[media] = codec
	}
}

func (h *handleStats) BytesReceived(media string, bytes uint64) {
	delta := h.bytesReceived.Update(media, bytes)
	statsJanusMediaBytesTotal.WithLabelValues(media, "incoming").Add(float64(delta))
}

func (h *handleStats) BytesSent(media string, bytes uint64) {
	delta := h.bytesSent.Update(media, bytes)
	statsJanusMediaBytesTotal.WithLabelValues(media, "outgoing").Add(float64(delta))
}

func (h *handleStats) NacksReceived(media string, nacks uint64) {
	delta := h.nacksReceived.Update(media, nacks)
	statsJanusMediaNACKTotal.WithLabelValues(media, "incoming").Add(float64(delta))
}

func (h *handleStats) NacksSent(media string, nacks uint64) {
	delta := h.nacksSent.Update(media, nacks)
	statsJanusMediaNACKTotal.WithLabelValues(media, "outgoing").Add(float64(delta))
}

func (h *handleStats) RetransmissionsReceived(media string, retransmissions uint64) {
	delta := h.retransmissionsReceived.Update(media, retransmissions)
	statsJanusMediaRetransmissionsTotal.WithLabelValues(media).Add(float64(delta))
}

func (h *handleStats) LostLocal(media string, lost uint64) {
	delta := h.lostLocal.Update(media, lost)
	statsJanusMediaLostTotal.WithLabelValues(media, "local").Add(float64(delta))
}

func (h *handleStats) LostRemote(media string, lost uint64) {
	delta := h.lostRemote.Update(media, lost)
	statsJanusMediaLostTotal.WithLabelValues(media, "remote").Add(float64(delta))
}

type EventsHandler struct {
	mu sync.Mutex

	logger log.Logger
	ctx    context.Context
	mcu    EventHandler
	// +checklocks:mu
	conn  *websocket.Conn
	addr  string
	agent string

	supportsHandles bool
	handleStats     map[uint64]*handleStats

	events chan janusEvent
}

func RunEventsHandler(ctx context.Context, mcu sfu.SFU, conn *websocket.Conn, addr string, agent string) {
	deadline := time.Now().Add(time.Second)
	if mcu == nil {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "no mcu configured"), deadline) // nolint
		return
	}

	m, ok := mcu.(EventHandler)
	if !ok {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "mcu does not support events"), deadline) // nolint
		return
	}

	if !internal.IsLoopbackIP(addr) && !internal.IsPrivateIP(addr) {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "only loopback and private connections allowed"), deadline) // nolint
		return
	}

	client, err := NewEventsHandler(ctx, m, conn, addr, agent)
	if err != nil {
		logger := log.LoggerFromContext(ctx)
		logger.Printf("Could not create Janus events handler for %s: %s", addr, err)
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "error creating handler"), deadline) // nolint
		return
	}

	client.Run()
}

func NewEventsHandler(ctx context.Context, mcu EventHandler, conn *websocket.Conn, addr string, agent string) (*EventsHandler, error) {
	handler := &EventsHandler{
		logger: log.LoggerFromContext(ctx),
		ctx:    ctx,
		mcu:    mcu,
		conn:   conn,
		addr:   addr,
		agent:  agent,

		events: make(chan janusEvent, 1),
	}

	return handler, nil
}

func (h *EventsHandler) Run() {
	h.logger.Printf("Processing Janus events from %s", h.addr)
	go h.writePump()
	go h.processEvents()

	h.readPump()
}

func (h *EventsHandler) close() {
	h.mu.Lock()
	conn := h.conn
	h.conn = nil
	h.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			h.logger.Printf("Error closing %s", err)
		}
	}
}

func (h *EventsHandler) readPump() {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		h.logger.Printf("Connection from %s closed while starting readPump", h.addr)
		return
	}

	conn.SetPongHandler(func(msg string) error {
		now := time.Now()
		conn.SetReadDeadline(now.Add(pongWait)) // nolint
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		messageType, reader, err := conn.NextReader()
		if err != nil {
			// Gorilla websocket hides the original net.Error, so also compare error messages
			if errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) || strings.Contains(err.Error(), net.ErrClosed.Error()) {
				break
			} else if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				h.logger.Printf("Error reading from %s: %v", h.addr, err)
			}
			break
		}

		if messageType != websocket.TextMessage {
			h.logger.Printf("Unsupported message type %v from %s", messageType, h.addr)
			continue
		}

		decodeBuffer, err := bufferPool.ReadAll(reader)
		if err != nil {
			h.logger.Printf("Error reading message from %s: %v", h.addr, err)
			break
		}

		if decodeBuffer.Len() == 0 {
			h.logger.Printf("Received empty message from %s", h.addr)
			bufferPool.Put(decodeBuffer)
			break
		}

		var events []janusEvent
		if data := decodeBuffer.Bytes(); data[0] != '[' {
			var event janusEvent
			if err := json.Unmarshal(data, &event); err != nil {
				h.logger.Printf("Error decoding message %s from %s: %v", decodeBuffer.String(), h.addr, err)
				bufferPool.Put(decodeBuffer)
				break
			}

			events = append(events, event)
		} else {
			if err := json.Unmarshal(data, &events); err != nil {
				h.logger.Printf("Error decoding message %s from %s: %v", decodeBuffer.String(), h.addr, err)
				bufferPool.Put(decodeBuffer)
				break
			}
		}

		bufferPool.Put(decodeBuffer)
		for _, e := range events {
			h.events <- e
		}
	}
}

func (h *EventsHandler) sendPing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return false
	}

	now := time.Now().UnixNano()
	msg := strconv.FormatInt(now, 10)
	h.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := h.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		h.logger.Printf("Could not send ping to %s: %v", h.addr, err)
		return false
	}

	return true
}

func (h *EventsHandler) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		h.close()
	}()

	for {
		select {
		case <-ticker.C:
			if !h.sendPing() {
				return
			}
		case <-h.ctx.Done():
			return
		}
	}
}

func (h *EventsHandler) processEvents() {
	for {
		select {
		case event := <-h.events:
			h.processEvent(event)
		case <-h.ctx.Done():
			return
		}
	}
}

func (h *EventsHandler) deleteHandleStats(event janusEvent) {
	if event.HandleId != 0 {
		delete(h.handleStats, event.HandleId)
	}
}

func (h *EventsHandler) getHandleStats(event janusEvent) *handleStats {
	if !h.supportsHandles {
		// Only create per-handle stats if enabled in Janus. Otherwise the
		// handleStats map will never be cleaned up.
		return nil
	} else if event.HandleId == 0 {
		return nil
	}

	if h.handleStats == nil {
		h.handleStats = make(map[uint64]*handleStats)
	}
	stats, found := h.handleStats[event.HandleId]
	if !found {
		stats = &handleStats{}
		h.handleStats[event.HandleId] = stats
	}
	return stats
}

func (h *EventsHandler) processEvent(event janusEvent) {
	evt, err := event.Decode()
	if err != nil {
		h.logger.Printf("Error decoding event %s (%s)", event, err)
		return
	}

	switch evt := evt.(type) {
	case *janusEventHandle:
		switch evt.Name {
		case "attached":
			h.supportsHandles = true
		case "detached":
			h.deleteHandleStats(event)
		}
	case *janusEventWebRTCICE:
		statsJanusICEStateTotal.WithLabelValues(evt.ICE).Inc()
	case *janusEventWebRTCDTLS:
		statsJanusDTLSStateTotal.WithLabelValues(evt.DTLS).Inc()
	case *janusEventWebRTCPeerConnection:
		statsJanusPeerConnectionStateTotal.WithLabelValues(evt.Connection, evt.Reason).Inc()
	case *janusEventWebRTCSelectedPair:
		statsJanusSelectedCandidateTotal.WithLabelValues("local", evt.Candidates.Local.Type, evt.Candidates.Local.Transport, fmt.Sprintf("ipv%d", evt.Candidates.Local.Family)).Inc()
		statsJanusSelectedCandidateTotal.WithLabelValues("remote", evt.Candidates.Remote.Type, evt.Candidates.Remote.Transport, fmt.Sprintf("ipv%d", evt.Candidates.Remote.Family)).Inc()
	case *janusEventMediaSlowLink:
		var direction string
		// "uplink" is Janus -> client, "downlink" is client -> Janus.
		if evt.SlowLink == "uplink" {
			direction = "outgoing"
		} else {
			direction = "incoming"
		}
		statsJanusSlowLinkTotal.WithLabelValues(evt.Media, direction).Inc()
	case *janusEventMediaStats:
		if rtt := evt.RTT; rtt > 0 {
			statsJanusMediaRTT.WithLabelValues(evt.Media).Observe(float64(rtt))
		}
		if jitter := evt.JitterLocal; jitter > 0 {
			statsJanusMediaJitter.WithLabelValues(evt.Media, "local").Observe(float64(jitter))
		}
		if jitter := evt.JitterRemote; jitter > 0 {
			statsJanusMediaJitter.WithLabelValues(evt.Media, "remote").Observe(float64(jitter))
		}
		if codec := evt.Codec; codec != "" {
			if stats := h.getHandleStats(event); stats != nil {
				stats.Codec(evt.Media, codec)
			}
		}
		if stats := h.getHandleStats(event); stats != nil {
			stats.BytesReceived(evt.Media, evt.BytesReceived)
		}
		if stats := h.getHandleStats(event); stats != nil {
			stats.BytesSent(evt.Media, evt.BytesSent)
		}
		if nacks := evt.NacksReceived; nacks > 0 {
			if stats := h.getHandleStats(event); stats != nil {
				stats.NacksReceived(evt.Media, uint64(nacks))
			}
		}
		if nacks := evt.NacksSent; nacks > 0 {
			if stats := h.getHandleStats(event); stats != nil {
				stats.NacksSent(evt.Media, uint64(nacks))
			}
		}
		if retransmissions := evt.RetransmissionsReceived; retransmissions > 0 {
			if stats := h.getHandleStats(event); stats != nil {
				stats.RetransmissionsReceived(evt.Media, uint64(retransmissions))
			}
		}
		if lost := evt.Lost; lost > 0 {
			if stats := h.getHandleStats(event); stats != nil {
				stats.LostLocal(evt.Media, uint64(lost))
			}
		}
		if lost := evt.LostByRemote; lost > 0 {
			if stats := h.getHandleStats(event); stats != nil {
				stats.LostRemote(evt.Media, uint64(lost))
			}
		}
		h.mcu.UpdateBandwidth(event.HandleId, evt.Media, api.BandwidthFromBytes(uint64(evt.BytesSentLastSec)), api.BandwidthFromBytes(uint64(evt.BytesReceivedLastSec)))
	}
}
