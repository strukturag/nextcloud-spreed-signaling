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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const (
	BackendVersion = "1.0"

	HeaderBackendSignalingRandom   = "Spreed-Signaling-Random"
	HeaderBackendSignalingChecksum = "Spreed-Signaling-Checksum"
	HeaderBackendServer            = "Spreed-Signaling-Backend"
)

func newRandomString(length int) string {
	b := make([]byte, length/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func CalculateBackendChecksum(random string, body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(random))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func AddBackendChecksum(r *http.Request, body []byte, secret []byte) {
	// Add checksum so the backend can validate the request.
	rnd := newRandomString(64)
	checksum := CalculateBackendChecksum(rnd, body, secret)
	r.Header.Set(HeaderBackendSignalingRandom, rnd)
	r.Header.Set(HeaderBackendSignalingChecksum, checksum)
}

func ValidateBackendChecksum(r *http.Request, body []byte, secret []byte) bool {
	rnd := r.Header.Get(HeaderBackendSignalingRandom)
	checksum := r.Header.Get(HeaderBackendSignalingChecksum)
	return ValidateBackendChecksumValue(checksum, rnd, body, secret)
}

func ValidateBackendChecksumValue(checksum string, random string, body []byte, secret []byte) bool {
	verify := CalculateBackendChecksum(random, body, secret)
	return subtle.ConstantTimeCompare([]byte(verify), []byte(checksum)) == 1
}

// Requests from Nextcloud to the signaling server.

type BackendServerRoomRequest struct {
	room *Room

	Type string `json:"type"`

	Invite *BackendRoomInviteRequest `json:"invite,omitempty"`

	Disinvite *BackendRoomDisinviteRequest `json:"disinvite,omitempty"`

	Update *BackendRoomUpdateRequest `json:"update,omitempty"`

	Delete *BackendRoomDeleteRequest `json:"delete,omitempty"`

	InCall *BackendRoomInCallRequest `json:"incall,omitempty"`

	Participants *BackendRoomParticipantsRequest `json:"participants,omitempty"`

	Message *BackendRoomMessageRequest `json:"message,omitempty"`

	// Internal properties
	ReceivedTime int64 `json:"received,omitempty"`
}

type BackendRoomInviteRequest struct {
	UserIds []string `json:"userids,omitempty"`
	// TODO(jojo): We should get rid of "AllUserIds" and find a better way to
	// notify existing users the room has changed and they need to update it.
	AllUserIds []string         `json:"alluserids,omitempty"`
	Properties *json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomDisinviteRequest struct {
	UserIds    []string `json:"userids,omitempty"`
	SessionIds []string `json:"sessionids,omitempty"`
	// TODO(jojo): We should get rid of "AllUserIds" and find a better way to
	// notify existing users the room has changed and they need to update it.
	AllUserIds []string         `json:"alluserids,omitempty"`
	Properties *json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomUpdateRequest struct {
	UserIds    []string         `json:"userids,omitempty"`
	Properties *json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomDeleteRequest struct {
	UserIds []string `json:"userids,omitempty"`
}

type BackendRoomInCallRequest struct {
	// TODO(jojo): Change "InCall" to "int" when #914 has landed in NC Talk.
	InCall  json.RawMessage          `json:"incall,omitempty"`
	Changed []map[string]interface{} `json:"changed,omitempty"`
	Users   []map[string]interface{} `json:"users,omitempty"`
}

type BackendRoomParticipantsRequest struct {
	Changed []map[string]interface{} `json:"changed,omitempty"`
	Users   []map[string]interface{} `json:"users,omitempty"`
}

type BackendRoomMessageRequest struct {
	Data *json.RawMessage `json:"data,omitempty"`
}

// Requests from the signaling server to the Nextcloud backend.

type BackendClientAuthRequest struct {
	Version string           `json:"version"`
	Params  *json.RawMessage `json:"params"`
}

type BackendClientRequest struct {
	Type string `json:"type"`

	Auth *BackendClientAuthRequest `json:"auth,omitempty"`

	Room *BackendClientRoomRequest `json:"room,omitempty"`

	Ping *BackendClientPingRequest `json:"ping,omitempty"`

	Session *BackendClientSessionRequest `json:"session,omitempty"`
}

func NewBackendClientAuthRequest(params *json.RawMessage) *BackendClientRequest {
	return &BackendClientRequest{
		Type: "auth",
		Auth: &BackendClientAuthRequest{
			Version: BackendVersion,
			Params:  params,
		},
	}
}

type BackendClientResponse struct {
	Type string `json:"type"`

	Error *Error `json:"error,omitempty"`

	Auth *BackendClientAuthResponse `json:"auth,omitempty"`

	Room *BackendClientRoomResponse `json:"room,omitempty"`

	Ping *BackendClientRingResponse `json:"ping,omitempty"`

	Session *BackendClientSessionResponse `json:"session,omitempty"`
}

type BackendClientAuthResponse struct {
	Version string           `json:"version"`
	UserId  string           `json:"userid"`
	User    *json.RawMessage `json:"user"`
}

type BackendClientRoomRequest struct {
	Version   string `json:"version"`
	RoomId    string `json:"roomid"`
	Action    string `json:"action,omitempty"`
	UserId    string `json:"userid"`
	SessionId string `json:"sessionid"`
}

func NewBackendClientRoomRequest(roomid string, userid string, sessionid string) *BackendClientRequest {
	return &BackendClientRequest{
		Type: "room",
		Room: &BackendClientRoomRequest{
			Version:   BackendVersion,
			RoomId:    roomid,
			UserId:    userid,
			SessionId: sessionid,
		},
	}
}

type BackendClientRoomResponse struct {
	Version    string           `json:"version"`
	RoomId     string           `json:"roomid"`
	Properties *json.RawMessage `json:"properties"`

	// Optional information about the Nextcloud Talk session. Can be used for
	// example to define a "userid" for otherwise anonymous users.
	// See "RoomSessionData" for a possible content.
	Session *json.RawMessage `json:"session,omitempty"`

	Permissions *[]Permission `json:"permissions,omitempty"`
}

type RoomSessionData struct {
	UserId string `json:"userid,omitempty"`
}

type BackendPingEntry struct {
	UserId    string `json:"userid,omitempty"`
	SessionId string `json:"sessionid"`
}

type BackendClientPingRequest struct {
	Version string             `json:"version"`
	RoomId  string             `json:"roomid"`
	Entries []BackendPingEntry `json:"entries"`
}

func NewBackendClientPingRequest(roomid string, entries []BackendPingEntry) *BackendClientRequest {
	return &BackendClientRequest{
		Type: "ping",
		Ping: &BackendClientPingRequest{
			Version: BackendVersion,
			RoomId:  roomid,
			Entries: entries,
		},
	}
}

type BackendClientRingResponse struct {
	Version string `json:"version"`
	RoomId  string `json:"roomid"`
}

type BackendClientSessionRequest struct {
	Version   string           `json:"version"`
	RoomId    string           `json:"roomid"`
	Action    string           `json:"action"`
	SessionId string           `json:"sessionid"`
	UserId    string           `json:"userid,omitempty"`
	User      *json.RawMessage `json:"user,omitempty"`
}

type BackendClientSessionResponse struct {
	Version string `json:"version"`
	RoomId  string `json:"roomid"`
}

func NewBackendClientSessionRequest(roomid string, action string, sessionid string, msg *AddSessionInternalClientMessage) *BackendClientRequest {
	request := &BackendClientRequest{
		Type: "session",
		Session: &BackendClientSessionRequest{
			Version:   BackendVersion,
			RoomId:    roomid,
			Action:    action,
			SessionId: sessionid,
		},
	}
	if msg != nil {
		request.Session.UserId = msg.UserId
		request.Session.User = msg.User
	}
	return request
}

type OcsMeta struct {
	Status     string `json:"status"`
	StatusCode int    `json:"statuscode"`
	Message    string `json:"message"`
}

type OcsBody struct {
	Meta OcsMeta          `json:"meta"`
	Data *json.RawMessage `json:"data"`
}

type OcsResponse struct {
	Ocs *OcsBody `json:"ocs"`
}

// See https://tools.ietf.org/html/draft-uberti-behave-turn-rest-00
type TurnCredentials struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	TTL      int64    `json:"ttl"`
	URIs     []string `json:"uris"`
}
