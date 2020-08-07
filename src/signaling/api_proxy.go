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
package signaling

import (
	"fmt"

	"gopkg.in/dgrijalva/jwt-go.v3"
)

type ProxyClientMessage struct {
	// The unique request id (optional).
	Id string `json:"id,omitempty"`

	// The type of the request.
	Type string `json:"type"`

	// Filled for type "hello"
	Hello *HelloProxyClientMessage `json:"hello,omitempty"`

	Bye *ByeProxyClientMessage `json:"bye,omitempty"`

	Command *CommandProxyClientMessage `json:"command,omitempty"`

	Payload *PayloadProxyClientMessage `json:"payload,omitempty"`
}

func (m *ProxyClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return fmt.Errorf("type missing")
	case "hello":
		if m.Hello == nil {
			return fmt.Errorf("hello missing")
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
			return fmt.Errorf("command missing")
		} else if err := m.Command.CheckValid(); err != nil {
			return err
		}
	case "payload":
		if m.Payload == nil {
			return fmt.Errorf("payload missing")
		} else if err := m.Payload.CheckValid(); err != nil {
			return err
		}
	}
	return nil
}

func (m *ProxyClientMessage) NewErrorServerMessage(e *Error) *ProxyServerMessage {
	return &ProxyServerMessage{
		Id:    m.Id,
		Type:  "error",
		Error: e,
	}
}

func (m *ProxyClientMessage) NewWrappedErrorServerMessage(e error) *ProxyServerMessage {
	return m.NewErrorServerMessage(NewError("internal_error", e.Error()))
}

// ProxyServerMessage is a message that is sent from the server to a client.
type ProxyServerMessage struct {
	Id string `json:"id,omitempty"`

	Type string `json:"type"`

	Error *Error `json:"error,omitempty"`

	Hello *HelloProxyServerMessage `json:"hello,omitempty"`

	Bye *ByeProxyServerMessage `json:"bye,omitempty"`

	Command *CommandProxyServerMessage `json:"command,omitempty"`

	Payload *PayloadProxyServerMessage `json:"payload,omitempty"`

	Event *EventProxyServerMessage `json:"event,omitempty"`
}

func (r *ProxyServerMessage) CloseAfterSend(session Session) bool {
	if r.Type == "bye" {
		return true
	}

	return false
}

// Type "hello"

type TokenClaims struct {
	jwt.StandardClaims
}

type HelloProxyClientMessage struct {
	Version string `json:"version"`

	ResumeId string `json:"resumeid"`

	Features []string `json:"features,omitempty"`

	// The authentication credentials.
	Token string `json:"token"`
}

func (m *HelloProxyClientMessage) CheckValid() error {
	if m.Version != HelloVersion {
		return fmt.Errorf("unsupported hello version: %s", m.Version)
	}
	if m.ResumeId == "" {
		if m.Token == "" {
			return fmt.Errorf("token missing")
		}
	}
	return nil
}

type HelloProxyServerMessage struct {
	Version string `json:"version"`

	SessionId string                    `json:"sessionid"`
	Server    *HelloServerMessageServer `json:"server,omitempty"`
}

// Type "bye"

type ByeProxyClientMessage struct {
}

func (m *ByeProxyClientMessage) CheckValid() error {
	// No additional validation required.
	return nil
}

type ByeProxyServerMessage struct {
	Reason string `json:"reason"`
}

// Type "command"

type CommandProxyClientMessage struct {
	Type string `json:"type"`

	StreamType  string `json:"streamType,omitempty"`
	PublisherId string `json:"publisherId,omitempty"`
	ClientId    string `json:"clientId,omitempty"`
}

func (m *CommandProxyClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return fmt.Errorf("type missing")
	case "create-publisher":
		if m.StreamType == "" {
			return fmt.Errorf("stream type missing")
		}
	case "create-subscriber":
		if m.PublisherId == "" {
			return fmt.Errorf("publisher id missing")
		}
		if m.StreamType == "" {
			return fmt.Errorf("stream type missing")
		}
	case "delete-publisher":
		fallthrough
	case "delete-subscriber":
		if m.ClientId == "" {
			return fmt.Errorf("client id missing")
		}
	}
	return nil
}

type CommandProxyServerMessage struct {
	Id string `json:"id,omitempty"`
}

// Type "payload"

type PayloadProxyClientMessage struct {
	Type string `json:"type"`

	ClientId string                 `json:"clientId"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

func (m *PayloadProxyClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return fmt.Errorf("type missing")
	case "offer":
		fallthrough
	case "answer":
		fallthrough
	case "candidate":
		if len(m.Payload) == 0 {
			return fmt.Errorf("payload missing")
		}
	case "endOfCandidates":
		fallthrough
	case "requestoffer":
		// No payload required.
	}
	if m.ClientId == "" {
		return fmt.Errorf("client id missing")
	}
	return nil
}

type PayloadProxyServerMessage struct {
	Type string `json:"type"`

	ClientId string                 `json:"clientId"`
	Payload  map[string]interface{} `json:"payload"`
}

// Type "event"

type EventProxyServerMessage struct {
	Type string `json:"type"`

	ClientId string `json:"clientId,omitempty"`
	Load     int64  `json:"load,omitempty"`
}
