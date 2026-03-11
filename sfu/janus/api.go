/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
	"encoding/json"
	"fmt"
)

const (
	janusEventTypeSession = 1

	janusEventTypeHandle = 2

	janusEventTypeExternal = 4

	janusEventTypeJSEP = 8

	janusEventTypeWebRTC                   = 16
	janusEventSubTypeWebRTCICE             = 1
	janusEventSubTypeWebRTCLocalCandidate  = 2
	janusEventSubTypeWebRTCRemoteCandidate = 3
	janusEventSubTypeWebRTCSelectedPair    = 4
	janusEventSubTypeWebRTCDTLS            = 5
	janusEventSubTypeWebRTCPeerConnection  = 6

	janusEventTypeMedia            = 32
	janusEventSubTypeMediaState    = 1
	janusEventSubTypeMediaSlowLink = 2
	janusEventSubTypeMediaStats    = 3

	janusEventTypePlugin = 64

	janusEventTypeTransport = 128

	janusEventTypeCore                  = 256
	janusEventSubTypeCoreStatusStartup  = 1
	janusEventSubTypeCoreStatusShutdown = 2
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

type janusEvent struct {
	Emitter   string          `json:"emitter"`
	Type      int             `json:"type"`
	SubType   int             `json:"subtype,omitempty"`
	Timestamp uint64          `json:"timestamp"`
	SessionId uint64          `json:"session_id,omitempty"`
	HandleId  uint64          `json:"handle_id,omitempty"`
	OpaqueId  uint64          `json:"opaque_id,omitempty"`
	Event     json.RawMessage `json:"event"`
}

func (e janusEvent) String() string {
	return marshalEvent(e)
}

func (e janusEvent) Decode() (any, error) {
	switch e.Type {
	case janusEventTypeSession:
		return unmarshalEvent[janusEventSession](e.Event)
	case janusEventTypeHandle:
		return unmarshalEvent[janusEventHandle](e.Event)
	case janusEventTypeExternal:
		return unmarshalEvent[janusEventExternal](e.Event)
	case janusEventTypeJSEP:
		return unmarshalEvent[janusEventJSEP](e.Event)
	case janusEventTypeWebRTC:
		switch e.SubType {
		case janusEventSubTypeWebRTCICE:
			return unmarshalEvent[janusEventWebRTCICE](e.Event)
		case janusEventSubTypeWebRTCLocalCandidate:
			return unmarshalEvent[janusEventWebRTCLocalCandidate](e.Event)
		case janusEventSubTypeWebRTCRemoteCandidate:
			return unmarshalEvent[janusEventWebRTCRemoteCandidate](e.Event)
		case janusEventSubTypeWebRTCSelectedPair:
			return unmarshalEvent[janusEventWebRTCSelectedPair](e.Event)
		case janusEventSubTypeWebRTCDTLS:
			return unmarshalEvent[janusEventWebRTCDTLS](e.Event)
		case janusEventSubTypeWebRTCPeerConnection:
			return unmarshalEvent[janusEventWebRTCPeerConnection](e.Event)
		}
	case janusEventTypeMedia:
		switch e.SubType {
		case janusEventSubTypeMediaState:
			return unmarshalEvent[janusEventMediaState](e.Event)
		case janusEventSubTypeMediaSlowLink:
			return unmarshalEvent[janusEventMediaSlowLink](e.Event)
		case janusEventSubTypeMediaStats:
			return unmarshalEvent[janusEventMediaStats](e.Event)
		}
	case janusEventTypePlugin:
		return unmarshalEvent[janusEventPlugin](e.Event)
	case janusEventTypeTransport:
		return unmarshalEvent[janusEventTransport](e.Event)
	case janusEventTypeCore:
		switch e.SubType {
		case janusEventSubTypeCoreStatusStartup:
			event, err := unmarshalEvent[janusEventCoreStartup](e.Event)
			if err != nil {
				return nil, err
			}

			switch event.Status {
			case "started":
				return unmarshalEvent[janusEventStatusStartupInfo](event.Info)
			case "update":
				return unmarshalEvent[janusEventStatusUpdateInfo](event.Info)
			}

			return event, nil
		case janusEventSubTypeCoreStatusShutdown:
			return unmarshalEvent[janusEventCoreShutdown](e.Event)
		}
	}

	return nil, fmt.Errorf("unsupported event type %d", e.Type)
}

type janusEventSessionTransport struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// type=1
type janusEventSession struct {
	Name string `json:"name"` // "created", "destroyed", "timeout"

	Transport *janusEventSessionTransport `json:"transport,omitempty"`
}

func (e janusEventSession) String() string {
	return marshalEvent(e)
}

// type=2
type janusEventHandle struct {
	Name   string `json:"name"` // "attached", "detached"
	Plugin string `json:"plugin"`
	Token  string `json:"token,omitempty"`
	// Deprecated
	OpaqueId string `json:"opaque_id,omitempty"`
}

func (e janusEventHandle) String() string {
	return marshalEvent(e)
}

// type=4
type janusEventExternal struct {
	Schema string          `json:"schema"`
	Data   json.RawMessage `json:"data"`
}

func (e janusEventExternal) String() string {
	return marshalEvent(e)
}

// type=8
type janusEventJSEP struct {
	Owner string `json:"owner"`
	Jsep  struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	} `json:"jsep"`
}

func (e janusEventJSEP) String() string {
	return marshalEvent(e)
}

// type=16, subtype=1
type janusEventWebRTCICE struct {
	ICE         string `json:"ice"` // "gathering", "connecting", "connected", "ready"
	StreamID    int    `json:"stream_id"`
	ComponentID int    `json:"component_id"`
}

func (e janusEventWebRTCICE) String() string {
	return marshalEvent(e)
}

// type=16, subtype=2
type janusEventWebRTCLocalCandidate struct {
	LocalCandidate string `json:"local-candidate"`
	StreamID       int    `json:"stream_id"`
	ComponentID    int    `json:"component_id"`
}

func (e janusEventWebRTCLocalCandidate) String() string {
	return marshalEvent(e)
}

// type=16, subtype=3
type janusEventWebRTCRemoteCandidate struct {
	RemoteCandidate string `json:"remote-candidate"`
	StreamID        int    `json:"stream_id"`
	ComponentID     int    `json:"component_id"`
}

func (e janusEventWebRTCRemoteCandidate) String() string {
	return marshalEvent(e)
}

type janusEventCandidate struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Type      string `json:"type"`
	Transport string `json:"transport"`
	Family    int    `json:"family"`
}

func (e janusEventCandidate) String() string {
	return marshalEvent(e)
}

type janusEventCandidates struct {
	Local  janusEventCandidate `json:"local"`
	Remote janusEventCandidate `json:"remote"`
}

func (e janusEventCandidates) String() string {
	return marshalEvent(e)
}

// type=16, subtype=4
type janusEventWebRTCSelectedPair struct {
	StreamID    int `json:"stream_id"`
	ComponentID int `json:"component_id"`

	SelectedPair string               `json:"selected-pair"`
	Candidates   janusEventCandidates `json:"candidates"`
}

func (e janusEventWebRTCSelectedPair) String() string {
	return marshalEvent(e)
}

// type=16, subtype=5
type janusEventWebRTCDTLS struct {
	DTLS string `json:"dtls"` // "trying", "connected"

	StreamID    int `json:"stream_id"`
	ComponentID int `json:"component_id"`

	Retransmissions int `json:"retransmissions"`
}

func (e janusEventWebRTCDTLS) String() string {
	return marshalEvent(e)
}

// type=16, subtype=6
type janusEventWebRTCPeerConnection struct {
	Connection string `json:"connection"`       // "webrtcup", "hangup"
	Reason     string `json:"reason,omitempty"` // Only if "connection" == "hangup"
}

func (e janusEventWebRTCPeerConnection) String() string {
	return marshalEvent(e)
}

// type=32, subtype=1
type janusEventMediaState struct {
	Media     string `json:"media"` // "audio", "video"
	MID       string `json:"mid"`
	SubStream *int   `json:"substream,omitempty"`
	Receiving bool   `json:"receiving"`
	Seconds   int    `json:"seconds"`
}

func (e janusEventMediaState) String() string {
	return marshalEvent(e)
}

// type=32, subtype=2
type janusEventMediaSlowLink struct {
	Media       string `json:"media"` // "audio", "video"
	MID         string `json:"mid"`
	SlowLink    string `json:"slow_link"` // "uplink", "downlink"
	LostLastSec int    `json:"lost_lastsec"`
}

func (e janusEventMediaSlowLink) String() string {
	return marshalEvent(e)
}

type janusMediaStatsRTTValues struct {
	NTP  uint32 `json:"ntp"`
	LSR  uint32 `json:"lsr"`
	DLSR uint32 `json:"dlsr"`
}

func (e janusMediaStatsRTTValues) String() string {
	return marshalEvent(e)
}

// type=32, subtype=3
type janusEventMediaStats struct {
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
	RTTValues *janusMediaStatsRTTValues `json:"rtt-values,omitempty"`

	// For all media on all layers
	PacketsReceived uint32 `json:"packets-received"`
	PacketsSent     uint32 `json:"packets-sent"`
	BytesReceived   uint64 `json:"bytes-received"`
	BytesSent       uint64 `json:"bytes-sent"`

	// For layer 0 if REMB is enabled
	REMBBitrate uint32 `json:"remb-bitrate"`
}

func (e janusEventMediaStats) String() string {
	return marshalEvent(e)
}

// type=64
type janusEventPlugin struct {
	Plugin string          `json:"plugin"`
	Data   json.RawMessage `json:"data"`
}

func (e janusEventPlugin) String() string {
	return marshalEvent(e)
}

type janusEventTransportWebsocket struct {
	Event    string `json:"event"`
	AdminApi bool   `json:"admin_api,omitempty"`
	IP       string `json:"ip,omitempty"`
}

// type=128
type janusEventTransport struct {
	Transport string                       `json:"transport"`
	Id        string                       `json:"id"`
	Data      janusEventTransportWebsocket `json:"data"`
}

func (e janusEventTransport) String() string {
	return marshalEvent(e)
}

type janusEventDependenciesInfo struct {
	Glib2   string `json:"glib2"`
	Jansson string `json:"jansson"`
	Libnice string `json:"libnice"`
	Libsrtp string `json:"libsrtp"`
	Libcurl string `json:"libcurl,omitempty"`
	Crypto  string `json:"crypto"`
}

func (e janusEventDependenciesInfo) String() string {
	return marshalEvent(e)
}

type janusEventPluginInfo struct {
	Name          string `json:"name"`
	Author        string `json:"author"`
	Description   string `json:"description"`
	VersionString string `json:"version_string"`
	Version       int    `json:"version"`
}

func (e janusEventPluginInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1, status="startup"
type janusEventStatusStartupInfo struct {
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

	Dependencies *janusEventDependenciesInfo     `json:"dependencies,omitempty"`
	Transports   map[string]janusEventPluginInfo `json:"transports,omitempty"`
	Events       map[string]janusEventPluginInfo `json:"events,omitempty"`
	Loggers      map[string]janusEventPluginInfo `json:"loggers,omitempty"`
	Plugins      map[string]janusEventPluginInfo `json:"plugins,omitempty"`
}

func (e janusEventStatusStartupInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1, status="update"
type janusEventStatusUpdateInfo struct {
	Sessions        int `json:"sessions"`
	Handles         int `json:"handles"`
	PeerConnections int `json:"peerconnections"`
	StatsPeriod     int `json:"stats-period"`
}

func (e janusEventStatusUpdateInfo) String() string {
	return marshalEvent(e)
}

// type=256, subtype=1
type janusEventCoreStartup struct {
	Status string          `json:"status"`
	Info   json.RawMessage `json:"info"`
}

func (e janusEventCoreStartup) String() string {
	return marshalEvent(e)
}

// type=256, subtype=2
type janusEventCoreShutdown struct {
	Status string `json:"status"`
	Signum int    `json:"signum"`
}

func (e janusEventCoreShutdown) String() string {
	return marshalEvent(e)
}
