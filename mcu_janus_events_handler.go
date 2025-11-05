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
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

const (
	JanusEventsSubprotocol = "janus-events"

	JanusEventTypeSession = 1

	JanusEventTypeHandle = 2

	JanusEventTypeExternal = 4

	JanusEventTypeJSEP = 8

	JanusEventTypeWebRTC                   = 16
	JanusEventSubTypeWebRTCICE             = 1
	JanusEventSubTypeWebRTCLocalCandidate  = 2
	JanusEventSubTypeWebRTCRemoteCandidate = 3
	JanusEventSubTypeWebRTCSelectedPair    = 4
	JanusEventSubTypeWebRTCDTLS            = 5
	JanusEventSubTypeWebRTCPeerConnection  = 6

	JanusEventTypeMedia            = 32
	JanusEventSubTypeMediaState    = 1
	JanusEventSubTypeMediaSlowLink = 2
	JanusEventSubTypeMediaStats    = 3

	JanusEventTypePlugin = 64

	JanusEventTypeTransport = 128

	JanusEventTypeCore                  = 256
	JanusEventSubTypeCoreStatusStartup  = 1
	JanusEventSubTypeCoreStatusShutdown = 2
)

func unmarshalEvent[T any](data json.RawMessage) (*T, error) {
	var e T
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}

	return &e, nil
}

func marshalEvent[T any](e T) string {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", e, err)
	}

	return string(data)
}

type JanusEvent struct {
	Emitter   string          `json:"emitter"`
	Type      int             `json:"type"`
	SubType   int             `json:"subtype,omitempty"`
	Timestamp uint64          `json:"timestamp"`
	SessionId uint64          `json:"session_id,omitempty"`
	HandleId  uint64          `json:"handle_id,omitempty"`
	OpaqueId  uint64          `json:"opaque_id,omitempty"`
	Event     json.RawMessage `json:"event"`
}

func (e JanusEvent) String() string {
	return marshalEvent(e)
}

func (e JanusEvent) Decode() (any, error) {
	switch e.Type {
	case JanusEventTypeSession:
		return unmarshalEvent[JanusEventSession](e.Event)
	case JanusEventTypeHandle:
		return unmarshalEvent[JanusEventHandle](e.Event)
	case JanusEventTypeExternal:
		return unmarshalEvent[JanusEventExternal](e.Event)
	case JanusEventTypeJSEP:
		return unmarshalEvent[JanusEventJSEP](e.Event)
	case JanusEventTypeWebRTC:
		return unmarshalEvent[JanusEventWebRTC](e.Event)
	case JanusEventTypeMedia:
		switch e.SubType {
		case JanusEventSubTypeMediaState:
			return unmarshalEvent[JanusEventMediaState](e.Event)
		case JanusEventSubTypeMediaSlowLink:
			return unmarshalEvent[JanusEventMediaSlowLink](e.Event)
		case JanusEventSubTypeMediaStats:
			return unmarshalEvent[JanusEventMediaStats](e.Event)
		}
	case JanusEventTypePlugin:
		return unmarshalEvent[JanusEventPlugin](e.Event)
	case JanusEventTypeTransport:
		return unmarshalEvent[JanusEventTransport](e.Event)
	case JanusEventTypeCore:
		switch e.SubType {
		case JanusEventSubTypeCoreStatusStartup:
			event, err := unmarshalEvent[JanusEventCoreStartup](e.Event)
			if err != nil {
				return nil, err
			}

			switch event.Status {
			case "started":
				return unmarshalEvent[JanusEventStatusStartupInfo](event.Info)
			case "update":
				return unmarshalEvent[JanusEventStatusUpdateInfo](event.Info)
			}

			return event, nil
		case JanusEventSubTypeCoreStatusShutdown:
			return unmarshalEvent[JanusEventCoreShutdown](e.Event)
		}
	}

	return nil, fmt.Errorf("unsupported event type %d", e.Type)
}

type JanusEventSessionTransport struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// type=1
type JanusEventSession struct {
	Name string `json:"name"` // "created", "destroyed", "timeout"

	Transport *JanusEventSessionTransport `json:"transport,omitempty"`
}

func (e JanusEventSession) String() string {
	return marshalEvent(e)
}

// type=2
type JanusEventHandle struct {
	Name   string `json:"name"` // "attached", "detached"
	Plugin string `json:"plugin"`
	Token  string `json:"token,omitempty"`
	// Deprecated
	OpaqueId string `json:"opaque_id,omitempty"`
}

func (e JanusEventHandle) String() string {
	return marshalEvent(e)
}

// type=4
type JanusEventExternal struct {
	Schema string          `json:"schema"`
	Data   json.RawMessage `json:"data"`
}

func (e JanusEventExternal) String() string {
	return marshalEvent(e)
}

// type=8
type JanusEventJSEP struct {
	Owner string `json:"owner"`
	Jsep  struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	} `json:"jsep"`
}

func (e JanusEventJSEP) String() string {
	return marshalEvent(e)
}

// type=16, subtype=1
type JanusEventWebRTCICE struct {
	ICE         string `json:"ice"` // "gathering", "connecting", "connected", "ready"
	StreamID    int    `json:"stream_id"`
	ComponentID int    `json:"component_id"`
}

func (e JanusEventWebRTCICE) String() string {
	return marshalEvent(e)
}

// type=16, subtype=2
type JanusEventWebRTCLocalCandidate struct {
	LocalCandidate string `json:"local-candidate"`
	StreamID       int    `json:"stream_id"`
	ComponentID    int    `json:"component_id"`
}

func (e JanusEventWebRTCLocalCandidate) String() string {
	return marshalEvent(e)
}

// type=16, subtype=3
type JanusEventWebRTCRemoteCandidate struct {
	RemoteCandidate string `json:"remote-candidate"`
	StreamID        int    `json:"stream_id"`
	ComponentID     int    `json:"component_id"`
}

func (e JanusEventWebRTCRemoteCandidate) String() string {
	return marshalEvent(e)
}

type JanusEventCandidate struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Type      string `json:"type"`
	Transport string `json:"transport"`
	Family    int    `json:"family"`
}

func (e JanusEventCandidate) String() string {
	return marshalEvent(e)
}

type JanusEventCandidates struct {
	Local  JanusEventCandidate `json:"local"`
	Remote JanusEventCandidate `json:"remote"`
}

func (e JanusEventCandidates) String() string {
	return marshalEvent(e)
}

// type=16, subtype=4
type JanusEventWebRTCSelectedPair struct {
	StreamID    int `json:"stream_id"`
	ComponentID int `json:"component_id"`

	SelectedPair string               `json:"selected-pair"`
	Candidates   JanusEventCandidates `json:"candidates"`
}

func (e JanusEventWebRTCSelectedPair) String() string {
	return marshalEvent(e)
}

// type=16, subtype=5
type JanusEventWebRTCDTLS struct {
	DTLS string `json:"dtls"` // "trying", "connected"

	StreamID    int `json:"stream_id"`
	ComponentID int `json:"component_id"`

	Retransmissions int `json:"retransmissions"`
}

func (e JanusEventWebRTCDTLS) String() string {
	return marshalEvent(e)
}

// type=16, subtype=6
type JanusEventWebRTCPeerConnection struct {
	Connection string `json:"connection"` // "webrtcup"
}

func (e JanusEventWebRTCPeerConnection) String() string {
	return marshalEvent(e)
}

// type=16
type JanusEventWebRTC struct {
	Owner string          `json:"owner"`
	Jsep  json.RawMessage `json:"jsep"`
}

func (e JanusEventWebRTC) String() string {
	return marshalEvent(e)
}

// type=32, subtype=1
type JanusEventMediaState struct {
	Media     string `json:"media"` // "audio", "video"
	MID       string `json:"mid"`
	SubStream *int   `json:"substream,omitempty"`
	Receiving bool   `json:"receiving"`
	Seconds   int    `json:"seconds"`
}

func (e JanusEventMediaState) String() string {
	return marshalEvent(e)
}

// type=32, subtype=2
type JanusEventMediaSlowLink struct {
	Media       string `json:"media"` // "audio", "video"
	MID         string `json:"mid"`
	SlowLink    string `json:"slow_link"` // "uplink", "downlink"
	LostLastSec int    `json:"lost_lastsec"`
}

func (e JanusEventMediaSlowLink) String() string {
	return marshalEvent(e)
}

type JanusMediaStatsRTTValues struct {
	NTP  uint32 `json:"ntp"`
	LSR  uint32 `json:"lsr"`
	DLSR uint32 `json:"dlsr"`
}

func (e JanusMediaStatsRTTValues) String() string {
	return marshalEvent(e)
}

// type=32, subtype=3
type JanusEventMediaStats struct {
	MID    string `json:"mid"`
	MIndex int    `json:"mindex"`
	Media  string `json:"media"` // "audio", "video", "video-sim1", "video-sim2"

	// Audio / video only
	Codec                   string `json:"codec,omitempty"`
	Base                    uint32 `json:"base"`
	Lost                    int32  `json:"lost"`
	LostByRemote            int32  `json:"lost-by-remote"`
	JitterLocal             uint32 `json:"jitter-local"`
	JitterRemote            uint32 `json:"jitter-remote"`
	InLinkQuality           uint32 `json:"in-link-quality"`
	InMediaLinkQuality      uint32 `json:"in-media-link-quality"`
	OutLinkQuality          uint32 `json:"out-link-quality"`
	OutMediaLinkQuality     uint32 `json:"out-media-link-quality"`
	BytesReceivedLastSec    uint32 `json:"bytes-received-lastsec"`
	BytesSentLastSec        uint32 `json:"bytes-sent-lastsec"`
	NacksReceived           uint32 `json:"nacks-received"`
	NacksSent               uint32 `json:"nacks-sent"`
	RetransmissionsReceived uint32 `json:"retransmissions-received"`

	// Only for audio / video on layer 0
	RTT uint32 `json:"rtt,omitempty"`
	// Only for audio / video on layer 0 if RTCP is active
	RTTValues *JanusMediaStatsRTTValues `json:"rtt-values,omitempty"`

	// For all media on all layers
	PacketsReceived int32 `json:"packets-received"`
	PacketsSent     int32 `json:"packets-sent"`
	BytesReceived   int64 `json:"bytes-received"`
	BytesSent       int64 `json:"bytes-sent"`

	// For layer 0 if REMB is enabled
	REMBBitrate uint32 `json:"remb-bitrate"`
}

func (e JanusEventMediaStats) String() string {
	return marshalEvent(e)
}

// type=64
type JanusEventPlugin struct {
	Plugin string          `json:"plugin"`
	Data   json.RawMessage `json:"data"`
}

func (e JanusEventPlugin) String() string {
	return marshalEvent(e)
}

type JanusEventTransportWebsocket struct {
	Event    string `json:"event"`
	AdminApi bool   `json:"admin_api,omitempty"`
	IP       string `json:"ip,omitempty"`
}

// type=128
type JanusEventTransport struct {
	Transport string                       `json:"transport"`
	Id        string                       `json:"id"`
	Data      JanusEventTransportWebsocket `json:"data"`
}

func (e JanusEventTransport) String() string {
	return marshalEvent(e)
}

type JanusEventDependenciesInfo struct {
	Glib2   string `json:"glib2"`
	Jansson string `json:"jansson"`
	Libnice string `json:"libnice"`
	Libsrtp string `json:"libsrtp"`
	Libcurl string `json:"libcurl,omitempty"`
	Crypto  string `json:"crypto"`
}

func (e JanusEventDependenciesInfo) String() string {
	return marshalEvent(e)
}

type JanusEventPluginInfo struct {
	Name          string `json:"name"`
	Author        string `json:"author"`
	Description   string `json:"description"`
	VersionString string `json:"version_string"`
	Version       int    `json:"version"`
}

func (e JanusEventPluginInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1, status="startup"
type JanusEventStatusStartupInfo struct {
	Janus                 string   `json:"janus"`
	Version               int      `json:"version"`
	VersionString         string   `json:"version_string"`
	Author                string   `json:"author"`
	CommitHash            string   `json:"commit-hash"`
	CompileTime           string   `json:"compile-time"`
	LogToStdout           bool     `json:"log-to-stdout"`
	LogToFile             bool     `json:"log-to-file"`
	LogPath               string   `json:"log-path,omitempty"`
	DataChannels          bool     `json:"data_channels"`
	AcceptingNewSessions  bool     `json:"accepting-new-sessions"`
	SessionTimeout        int      `json:"session-timeout"`
	ReclaimSessionTimeout int      `json:"reclaim-session-timeout"`
	CandidatesTimeout     int      `json:"candidates-timeout"`
	ServerName            string   `json:"server-name"`
	LocalIP               string   `json:"local-ip"`
	PublicIP              string   `json:"public-ip,omitempty"`
	PublicIPs             []string `json:"public-ips,omitempty"`
	IPv6                  bool     `json:"ipv6"`
	IPv6LinkLocal         bool     `json:"ipv6-link-local,omitempty"`
	ICELite               bool     `json:"ice-lite"`
	ICETCP                bool     `json:"ice-tcp"`
	ICENomination         string   `json:"ice-nomination,omitempty"`
	ICEConsentFreshness   bool     `json:"ice-consent-freshness"`
	ICEKeepaliveConncheck bool     `json:"ice-keepalive-conncheck"`
	HangupOnFailed        bool     `json:"hangup-on-failed"`
	FullTrickle           bool     `json:"full-trickle"`
	MDNSEnabled           bool     `json:"mdns-enabled"`
	MinNACKQueue          int      `json:"min-nack-queue"`
	NACKOptimizations     bool     `json:"nack-optimizations"`
	TWCCPeriod            int      `json:"twcc-period"`
	DSCP                  int      `json:"dscp,omitempty"`
	DTLSMCU               int      `json:"dtls-mcu"`
	STUNServer            string   `json:"stun-server,omitempty"`
	TURNServer            string   `json:"turn-server,omitempty"`
	AllowForceRelay       bool     `json:"allow-force-relay,omitempty"`
	StaticEventLoops      int      `json:"static-event-loops"`
	LoopIndication        bool     `json:"loop-indication,omitempty"`
	APISecret             bool     `json:"api_secret"`
	AuthToken             bool     `json:"auth_token"`
	EventHandlers         bool     `json:"event_handlers"`
	OpaqueIdInAPI         bool     `json:"opaqueid_in_api"`
	WebRTCEncryption      bool     `json:"webrtc_encryption"`

	Dependencies *JanusEventDependenciesInfo     `json:"dependencies,omitempty"`
	Transports   map[string]JanusEventPluginInfo `json:"transports,omitempty"`
	Events       map[string]JanusEventPluginInfo `json:"events,omitempty"`
	Loggers      map[string]JanusEventPluginInfo `json:"loggers,omitempty"`
	Plugins      map[string]JanusEventPluginInfo `json:"plugins,omitempty"`
}

func (e JanusEventStatusStartupInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1, status="update"
type JanusEventStatusUpdateInfo struct {
	Sessions        int `json:"sessions"`
	Handles         int `json:"handles"`
	PeerConnections int `json:"peerconnections"`
	StatsPeriod     int `json:"stats-period"`
}

func (e JanusEventStatusUpdateInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1
type JanusEventCoreStartup struct {
	Status string          `json:"status"`
	Info   json.RawMessage `json:"info"`
}

func (e JanusEventCoreStartup) String() string {
	return marshalEvent(e)
}

// type=256, subtype=2
type JanusEventCoreShutdown struct {
	Status string `json:"status"`
	Signum int    `json:"signum"`
}

func (e JanusEventCoreShutdown) String() string {
	return marshalEvent(e)
}

type McuEventHandler interface {
	UpdateBandwidth(handle uint64, media string, sent uint32, received uint32)
}

type JanusEventsHandler struct {
	mu sync.Mutex

	ctx context.Context
	mcu McuEventHandler
	// +checklocks:mu
	conn  *websocket.Conn
	addr  string
	agent string

	events chan JanusEvent
}

func RunJanusEventsHandler(ctx context.Context, mcu Mcu, conn *websocket.Conn, addr string, agent string) {
	deadline := time.Now().Add(time.Second)
	if mcu == nil {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "no mcu configured"), deadline) // nolint
		return
	}

	m, ok := mcu.(McuEventHandler)
	if !ok {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "mcu does not support events"), deadline) // nolint
		return
	}

	if !internal.IsLoopbackIP(addr) && !internal.IsPrivateIP(addr) {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "only loopback and private connections allowed"), deadline) // nolint
		return
	}

	client, err := NewJanusEventsHandler(ctx, m, conn, addr, agent)
	if err != nil {
		log.Printf("Could not create Janus events handler for %s: %s", addr, err)
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "error creating handler"), deadline) // nolint
		return
	}

	client.Run()
}

func NewJanusEventsHandler(ctx context.Context, mcu McuEventHandler, conn *websocket.Conn, addr string, agent string) (*JanusEventsHandler, error) {
	handler := &JanusEventsHandler{
		ctx:   ctx,
		mcu:   mcu,
		conn:  conn,
		addr:  addr,
		agent: agent,

		events: make(chan JanusEvent, 1),
	}

	return handler, nil
}

func (h *JanusEventsHandler) Run() {
	log.Printf("Processing Janus events from %s", h.addr)
	go h.writePump()
	go h.processEvents()

	h.readPump()
}

func (h *JanusEventsHandler) close() {
	h.mu.Lock()
	conn := h.conn
	h.conn = nil
	h.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			log.Printf("Error closing %s", err)
		}
	}
}

func (h *JanusEventsHandler) readPump() {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		log.Printf("Connection from %s closed while starting readPump", h.addr)
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
		messageType, reader, err := conn.NextReader()
		if err != nil {
			// Gorilla websocket hides the original net.Error, so also compare error messages
			if errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) || strings.Contains(err.Error(), net.ErrClosed.Error()) {
				break
			} else if _, ok := err.(*websocket.CloseError); !ok || websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				log.Printf("Error reading from %s: %v", h.addr, err)
			}
			break
		}

		if messageType != websocket.TextMessage {
			log.Printf("Unsupported message type %v from %s", messageType, h.addr)
			continue
		}

		decodeBuffer, err := bufferPool.ReadAll(reader)
		if err != nil {
			log.Printf("Error reading message from %s: %v", h.addr, err)
			break
		}

		if decodeBuffer.Len() == 0 {
			log.Printf("Received empty message from %s", h.addr)
			bufferPool.Put(decodeBuffer)
			break
		}

		var events []JanusEvent
		if data := decodeBuffer.Bytes(); data[0] != '[' {
			var event JanusEvent
			if err := json.Unmarshal(data, &event); err != nil {
				log.Printf("Error decoding message %s from %s: %v", decodeBuffer.String(), h.addr, err)
				bufferPool.Put(decodeBuffer)
				break
			}

			events = append(events, event)
		} else {
			if err := json.Unmarshal(data, &events); err != nil {
				log.Printf("Error decoding message %s from %s: %v", decodeBuffer.String(), h.addr, err)
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

func (h *JanusEventsHandler) sendPing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return false
	}

	now := time.Now().UnixNano()
	msg := strconv.FormatInt(now, 10)
	h.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := h.conn.WriteMessage(websocket.PingMessage, []byte(msg)); err != nil {
		log.Printf("Could not send ping to %s: %v", h.addr, err)
		return false
	}

	return true
}

func (h *JanusEventsHandler) writePump() {
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

func (h *JanusEventsHandler) processEvents() {
	for {
		select {
		case event := <-h.events:
			h.processEvent(event)
		case <-h.ctx.Done():
			return
		}
	}
}

func (h *JanusEventsHandler) processEvent(event JanusEvent) {
	evt, err := event.Decode()
	if err != nil {
		log.Printf("Error decoding event %s (%s)", event, err)
		return
	}

	switch evt := evt.(type) {
	case *JanusEventMediaStats:
		h.mcu.UpdateBandwidth(event.HandleId, evt.Media, evt.BytesSentLastSec, evt.BytesReceivedLastSec)
	}
}
