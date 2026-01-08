/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-jwt/jwt/v5"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

type ClientMessage struct {
	json.Marshaler
	json.Unmarshaler

	// The unique request id (optional).
	Id string `json:"id,omitempty"`

	// The type of the request.
	Type string `json:"type"`

	// Filled for type "hello"
	Hello *HelloClientMessage `json:"hello,omitempty"`

	Bye *ByeClientMessage `json:"bye,omitempty"`

	Command *CommandClientMessage `json:"command,omitempty"`

	Payload *PayloadClientMessage `json:"payload,omitempty"`
}

func (m *ClientMessage) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", m, err)
	}
	return string(data)
}

func (m *ClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return errors.New("type missing")
	case "hello":
		if m.Hello == nil {
			return errors.New("hello missing")
		} else if err := m.Hello.CheckValid(); err != nil {
			return err
		}
	case "bye":
		if m.Bye != nil {
			// Bye contents are optional
			if err := m.Bye.CheckValid(); err != nil {
				return err
			}
		}
	case "command":
		if m.Command == nil {
			return errors.New("command missing")
		} else if err := m.Command.CheckValid(); err != nil {
			return err
		}
	case "payload":
		if m.Payload == nil {
			return errors.New("payload missing")
		} else if err := m.Payload.CheckValid(); err != nil {
			return err
		}
	}
	return nil
}

func (m *ClientMessage) NewErrorServerMessage(e *api.Error) *ServerMessage {
	return &ServerMessage{
		Id:    m.Id,
		Type:  "error",
		Error: e,
	}
}

func (m *ClientMessage) NewWrappedErrorServerMessage(e error) *ServerMessage {
	return m.NewErrorServerMessage(api.NewError("internal_error", e.Error()))
}

// ServerMessage is a message that is sent from the server to a client.
type ServerMessage struct {
	json.Marshaler
	json.Unmarshaler

	Id string `json:"id,omitempty"`

	Type string `json:"type"`

	Error *api.Error `json:"error,omitempty"`

	Hello *HelloServerMessage `json:"hello,omitempty"`

	Bye *ByeServerMessage `json:"bye,omitempty"`

	Command *CommandServerMessage `json:"command,omitempty"`

	Payload *PayloadServerMessage `json:"payload,omitempty"`

	Event *EventServerMessage `json:"event,omitempty"`
}

func (r *ServerMessage) String() string {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", r, err)
	}
	return string(data)
}

func (r *ServerMessage) CloseAfterSend(session api.RoomAware) bool {
	switch r.Type {
	case "bye":
		return true
	default:
		return false
	}
}

// Type "hello"

type TokenClaims struct {
	jwt.RegisteredClaims
}

type HelloClientMessage struct {
	Version string `json:"version"`

	ResumeId api.PublicSessionId `json:"resumeid"`

	Features []string `json:"features,omitempty"`

	// The authentication credentials.
	Token string `json:"token"`
}

func (m *HelloClientMessage) CheckValid() error {
	if m.Version != api.HelloVersionV1 {
		return fmt.Errorf("unsupported hello version: %s", m.Version)
	}
	if m.ResumeId == "" {
		if m.Token == "" {
			return errors.New("token missing")
		}
	}
	return nil
}

type HelloServerMessage struct {
	Version string `json:"version"`

	SessionId api.PublicSessionId       `json:"sessionid"`
	Server    *api.WelcomeServerMessage `json:"server,omitempty"`
}

// Type "bye"

type ByeClientMessage struct {
}

func (m *ByeClientMessage) CheckValid() error {
	// No additional validation required.
	return nil
}

type ByeServerMessage struct {
	Reason string `json:"reason"`
}

// Type "command"

type CommandClientMessage struct {
	Type string `json:"type"`

	Sid         string              `json:"sid,omitempty"`
	StreamType  sfu.StreamType      `json:"streamType,omitempty"`
	PublisherId api.PublicSessionId `json:"publisherId,omitempty"`
	ClientId    string              `json:"clientId,omitempty"`

	// Deprecated: use PublisherSettings instead.
	Bitrate api.Bandwidth `json:"bitrate,omitempty"`
	// Deprecated: use PublisherSettings instead.
	MediaTypes sfu.MediaType `json:"mediatypes,omitempty"`

	PublisherSettings *sfu.NewPublisherSettings `json:"publisherSettings,omitempty"`

	RemoteUrl   string `json:"remoteUrl,omitempty"`
	remoteUrl   *url.URL
	RemoteToken string `json:"remoteToken,omitempty"`

	Hostname string `json:"hostname,omitempty"`
	Port     int    `json:"port,omitempty"`
	RtcpPort int    `json:"rtcpPort,omitempty"`
}

func (m *CommandClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return errors.New("type missing")
	case "create-publisher":
		if m.StreamType == "" {
			return errors.New("stream type missing")
		}
	case "create-subscriber":
		if m.PublisherId == "" {
			return errors.New("publisher id missing")
		}
		if m.StreamType == "" {
			return errors.New("stream type missing")
		}
		if m.RemoteUrl != "" {
			if m.RemoteToken == "" {
				return errors.New("remote token missing")
			}

			remoteUrl, err := url.Parse(m.RemoteUrl)
			if err != nil {
				return fmt.Errorf("invalid remote url: %w", err)
			}
			m.remoteUrl = remoteUrl
		}
	case "delete-publisher":
		fallthrough
	case "delete-subscriber":
		if m.ClientId == "" {
			return errors.New("client id missing")
		}
	}
	return nil
}

type CommandServerMessage struct {
	Id  string `json:"id,omitempty"`
	Sid string `json:"sid,omitempty"`

	Bitrate api.Bandwidth `json:"bitrate,omitempty"`

	Streams []sfu.PublisherStream `json:"streams,omitempty"`
}

// Type "payload"

type PayloadClientMessage struct {
	Type string `json:"type"`

	ClientId string        `json:"clientId"`
	Sid      string        `json:"sid,omitempty"`
	Payload  api.StringMap `json:"payload,omitempty"`
}

func (m *PayloadClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return errors.New("type missing")
	case "offer":
		fallthrough
	case "answer":
		fallthrough
	case "candidate":
		if len(m.Payload) == 0 {
			return errors.New("payload missing")
		}
	case "endOfCandidates":
		fallthrough
	case "requestoffer":
		// No payload required.
	}
	if m.ClientId == "" {
		return errors.New("client id missing")
	}
	return nil
}

type PayloadServerMessage struct {
	Type string `json:"type"`

	ClientId string        `json:"clientId"`
	Payload  api.StringMap `json:"payload"`
}

// Type "event"

type EventServerBandwidth struct {
	// Incoming is the bandwidth utilization for publishers in percent.
	Incoming *float64 `json:"incoming,omitempty"`
	// Outgoing is the bandwidth utilization for subscribers in percent.
	Outgoing *float64 `json:"outgoing,omitempty"`

	// Received is the incoming bandwidth.
	Received api.Bandwidth `json:"received,omitempty"`
	// Sent is the outgoing bandwidth.
	Sent api.Bandwidth `json:"sent,omitempty"`
}

func (b *EventServerBandwidth) String() string {
	switch {
	case b.Incoming != nil && b.Outgoing != nil:
		return fmt.Sprintf("bandwidth: incoming=%.3f%%, outgoing=%.3f%%", *b.Incoming, *b.Outgoing)
	case b.Incoming != nil:
		return fmt.Sprintf("bandwidth: incoming=%.3f%%, outgoing=unlimited", *b.Incoming)
	case b.Outgoing != nil:
		return fmt.Sprintf("bandwidth: incoming=unlimited, outgoing=%.3f%%", *b.Outgoing)
	default:
		return "bandwidth: incoming=unlimited, outgoing=unlimited"
	}
}

func (b EventServerBandwidth) AllowIncoming() bool {
	return b.Incoming == nil || *b.Incoming < 100
}

func (b EventServerBandwidth) AllowOutgoing() bool {
	return b.Outgoing == nil || *b.Outgoing < 100
}

type EventServerMessage struct {
	Type string `json:"type"`

	ClientId string `json:"clientId,omitempty"`
	Load     uint64 `json:"load,omitempty"`
	Sid      string `json:"sid,omitempty"`

	Bandwidth *EventServerBandwidth `json:"bandwidth,omitempty"`
}

// Information on a proxy in the etcd cluster.

type InformationEtcd struct {
	Address string `json:"address"`
}

func (p *InformationEtcd) CheckValid() error {
	if p.Address == "" {
		return errors.New("address missing")
	}
	if p.Address[len(p.Address)-1] != '/' {
		p.Address += "/"
	}
	return nil
}
