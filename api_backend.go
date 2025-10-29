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
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

const (
	BackendVersion = "1.0"

	HeaderBackendSignalingRandom   = "Spreed-Signaling-Random"
	HeaderBackendSignalingChecksum = "Spreed-Signaling-Checksum"
	HeaderBackendServer            = "Spreed-Signaling-Backend"

	ConfigGroupSignaling = "signaling"

	ConfigKeyHelloV2TokenKey  = "hello-v2-token-key"
	ConfigKeySessionPingLimit = "session-ping-limit"
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
	mac.Write([]byte(random)) // nolint
	mac.Write(body)           // nolint
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

	SwitchTo *BackendRoomSwitchToMessageRequest `json:"switchto,omitempty"`

	Dialout *BackendRoomDialoutRequest `json:"dialout,omitempty"`

	Transient *BackendRoomTransientRequest `json:"transient,omitempty"`

	// Internal properties
	ReceivedTime int64 `json:"received,omitempty"`
}

type BackendRoomInviteRequest struct {
	UserIds []string `json:"userids,omitempty"`
	// TODO(jojo): We should get rid of "AllUserIds" and find a better way to
	// notify existing users the room has changed and they need to update it.
	AllUserIds []string        `json:"alluserids,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomDisinviteRequest struct {
	UserIds    []string        `json:"userids,omitempty"`
	SessionIds []RoomSessionId `json:"sessionids,omitempty"`
	// TODO(jojo): We should get rid of "AllUserIds" and find a better way to
	// notify existing users the room has changed and they need to update it.
	AllUserIds []string        `json:"alluserids,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomUpdateRequest struct {
	UserIds    []string        `json:"userids,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type BackendRoomDeleteRequest struct {
	UserIds []string `json:"userids,omitempty"`
}

type BackendRoomInCallRequest struct {
	// TODO(jojo): Change "InCall" to "int" when #914 has landed in NC Talk.
	InCall  json.RawMessage `json:"incall,omitempty"`
	All     bool            `json:"all,omitempty"`
	Changed []api.StringMap `json:"changed,omitempty"`
	Users   []api.StringMap `json:"users,omitempty"`
}

type BackendRoomParticipantsRequest struct {
	Changed []api.StringMap `json:"changed,omitempty"`
	Users   []api.StringMap `json:"users,omitempty"`
}

type BackendRoomMessageRequest struct {
	Data json.RawMessage `json:"data,omitempty"`
}

type BackendRoomSwitchToSessionsList []RoomSessionId
type BackendRoomSwitchToSessionsMap map[RoomSessionId]json.RawMessage

type BackendRoomSwitchToPublicSessionsList []PublicSessionId
type BackendRoomSwitchToPublicSessionsMap map[PublicSessionId]json.RawMessage

type BackendRoomSwitchToMessageRequest struct {
	// Target room id
	RoomId string `json:"roomid"`

	// Sessions is either a BackendRoomSwitchToSessionsList or a
	// BackendRoomSwitchToSessionsMap.
	// In the map, the key is the session id, the value additional details
	// (or null) for the session. The details will be included in the request
	// to the connected client.
	Sessions json.RawMessage `json:"sessions,omitempty"`

	// Internal properties
	SessionsList BackendRoomSwitchToPublicSessionsList `json:"sessionslist,omitempty"`
	SessionsMap  BackendRoomSwitchToPublicSessionsMap  `json:"sessionsmap,omitempty"`
}

type BackendRoomDialoutRequest struct {
	// E.164 number to dial (e.g. "+1234567890")
	Number string `json:"number"`

	Options json.RawMessage `json:"options,omitempty"`
}

var (
	checkE164Number = regexp.MustCompile(`^\+\d{2,}$`)
)

func isValidNumber(s string) bool {
	return checkE164Number.MatchString(s)
}

func (r *BackendRoomDialoutRequest) ValidateNumber() *Error {
	if r.Number == "" {
		return NewError("number_missing", "No number provided")
	}

	if !isValidNumber(r.Number) {
		return NewError("invalid_number", "Expected E.164 number.")
	}

	return nil
}

func (r *BackendRoomDialoutRequest) String() string {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", r, err)
	}
	return string(data)
}

type TransientAction string

const (
	TransientActionSet    TransientAction = "set"
	TransientActionDelete TransientAction = "delete"
)

type BackendRoomTransientRequest struct {
	Action TransientAction `json:"action"`
	Key    string          `json:"key"`
	Value  any             `json:"value,omitempty"`
	TTL    time.Duration   `json:"ttl,omitempty"`
}

type BackendServerRoomResponse struct {
	Type string `json:"type"`

	Dialout *BackendRoomDialoutResponse `json:"dialout,omitempty"`
}

type BackendRoomDialoutError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type BackendRoomDialoutResponse struct {
	CallId string `json:"callid,omitempty"`

	Error *Error `json:"error,omitempty"`
}

// Requests from the signaling server to the Nextcloud backend.

type BackendClientAuthRequest struct {
	Version string          `json:"version"`
	Params  json.RawMessage `json:"params"`
}

type BackendClientRequest struct {
	json.Marshaler
	json.Unmarshaler

	Type string `json:"type"`

	Auth *BackendClientAuthRequest `json:"auth,omitempty"`

	Room *BackendClientRoomRequest `json:"room,omitempty"`

	Ping *BackendClientPingRequest `json:"ping,omitempty"`

	Session *BackendClientSessionRequest `json:"session,omitempty"`
}

func NewBackendClientAuthRequest(params json.RawMessage) *BackendClientRequest {
	return &BackendClientRequest{
		Type: "auth",
		Auth: &BackendClientAuthRequest{
			Version: BackendVersion,
			Params:  params,
		},
	}
}

type BackendClientResponse struct {
	json.Marshaler
	json.Unmarshaler

	Type string `json:"type"`

	Error *Error `json:"error,omitempty"`

	Auth *BackendClientAuthResponse `json:"auth,omitempty"`

	Room *BackendClientRoomResponse `json:"room,omitempty"`

	Ping *BackendClientRingResponse `json:"ping,omitempty"`

	Session *BackendClientSessionResponse `json:"session,omitempty"`
}

type BackendClientAuthResponse struct {
	Version string          `json:"version"`
	UserId  string          `json:"userid"`
	User    json.RawMessage `json:"user"`
}

type BackendClientRoomRequest struct {
	Version   string        `json:"version"`
	RoomId    string        `json:"roomid"`
	Action    string        `json:"action,omitempty"`
	UserId    string        `json:"userid"`
	SessionId RoomSessionId `json:"sessionid"`

	// For Nextcloud Talk with SIP support and for federated sessions.
	ActorId   string `json:"actorid,omitempty"`
	ActorType string `json:"actortype,omitempty"`
	InCall    int    `json:"incall,omitempty"`
}

func (r *BackendClientRoomRequest) UpdateFromSession(s Session) {
	if s.ClientType() == HelloClientTypeFederation {
		// Need to send additional data for requests of federated users.
		if u, err := s.ParsedUserData(); err == nil && len(u) > 0 {
			if actorType, found := api.GetStringMapEntry[string](u, "actorType"); found {
				if actorId, found := api.GetStringMapEntry[string](u, "actorId"); found {
					r.ActorId = actorId
					r.ActorType = actorType
				}
			}
		}
	}
}

func NewBackendClientRoomRequest(roomid string, userid string, sessionid RoomSessionId) *BackendClientRequest {
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
	Version    string          `json:"version"`
	RoomId     string          `json:"roomid"`
	Properties json.RawMessage `json:"properties"`

	// Optional information about the Nextcloud Talk session. Can be used for
	// example to define a "userid" for otherwise anonymous users.
	// See "RoomSessionData" for a possible content.
	Session json.RawMessage `json:"session,omitempty"`

	Permissions *[]Permission `json:"permissions,omitempty"`
}

type RoomSessionData struct {
	UserId string `json:"userid,omitempty"`
}

type BackendPingEntry struct {
	UserId    string        `json:"userid,omitempty"`
	SessionId RoomSessionId `json:"sessionid"`
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
	Version   string          `json:"version"`
	RoomId    string          `json:"roomid"`
	Action    string          `json:"action"`
	SessionId PublicSessionId `json:"sessionid"`
	UserId    string          `json:"userid,omitempty"`
	User      json.RawMessage `json:"user,omitempty"`
}

type BackendClientSessionResponse struct {
	Version string `json:"version"`
	RoomId  string `json:"roomid"`
}

func NewBackendClientSessionRequest(roomid string, action string, sessionid PublicSessionId, msg *AddSessionInternalClientMessage) *BackendClientRequest {
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
	Meta OcsMeta         `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type OcsResponse struct {
	json.Marshaler
	json.Unmarshaler

	Ocs *OcsBody `json:"ocs"`
}

// See https://tools.ietf.org/html/draft-uberti-behave-turn-rest-00
type TurnCredentials struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	TTL      int64    `json:"ttl"`
	URIs     []string `json:"uris"`
}

// Information on a backend in the etcd cluster.

type BackendInformationEtcd struct {
	// Compat setting.
	Url string `json:"url,omitempty"`

	Urls       []string `json:"urls,omitempty"`
	parsedUrls []*url.URL
	Secret     string `json:"secret"`

	MaxStreamBitrate int `json:"maxstreambitrate,omitempty"`
	MaxScreenBitrate int `json:"maxscreenbitrate,omitempty"`

	SessionLimit uint64 `json:"sessionlimit,omitempty"`
}

func (p *BackendInformationEtcd) CheckValid() (err error) {
	if p.Secret == "" {
		return fmt.Errorf("secret missing")
	}

	if len(p.Urls) > 0 {
		slices.Sort(p.Urls)
		p.Urls = slices.Compact(p.Urls)
		seen := make(map[string]bool)
		outIdx := 0
		for _, u := range p.Urls {
			parsedUrl, err := url.Parse(u)
			if err != nil {
				return fmt.Errorf("invalid url %s: %w", u, err)
			}

			var changed bool
			if parsedUrl, changed = internal.CanonicalizeUrl(parsedUrl); changed {
				u = parsedUrl.String()
			}
			p.Urls[outIdx] = u
			if seen[u] {
				continue
			}
			seen[u] = true
			p.parsedUrls = append(p.parsedUrls, parsedUrl)
			outIdx++
		}
		if len(p.Urls) != outIdx {
			clear(p.Urls[outIdx:])
			p.Urls = p.Urls[:outIdx]
		}
	} else if p.Url != "" {
		parsedUrl, err := url.Parse(p.Url)
		if err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}
		var changed bool
		if parsedUrl, changed = internal.CanonicalizeUrl(parsedUrl); changed {
			p.Url = parsedUrl.String()
		}

		p.Urls = append(p.Urls, p.Url)
		p.parsedUrls = append(p.parsedUrls, parsedUrl)
	} else {
		return fmt.Errorf("urls missing")
	}

	return nil
}

type BackendServerInfoVideoRoom struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Author  string `json:"author,omitempty"`
}

type BackendServerInfoSfuJanus struct {
	Url string `json:"url"`

	Connected bool `json:"connected"`

	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Author  string `json:"author,omitempty"`

	DataChannels *bool  `json:"datachannels,omitempty"`
	FullTrickle  *bool  `json:"fulltrickle,omitempty"`
	LocalIP      string `json:"localip,omitempty"`
	IPv6         *bool  `json:"ipv6,omitempty"`

	VideoRoom *BackendServerInfoVideoRoom `json:"videoroom,omitempty"`
}

type BackendServerInfoSfuProxy struct {
	Url string `json:"url"`
	IP  string `json:"ip,omitempty"`

	Connected bool       `json:"connected"`
	Temporary bool       `json:"temporary"`
	Shutdown  *bool      `json:"shutdown,omitempty"`
	Uptime    *time.Time `json:"uptime,omitempty"`

	Version  string   `json:"version,omitempty"`
	Features []string `json:"features,omitempty"`

	Country   string                     `json:"country,omitempty"`
	Load      *uint64                    `json:"load,omitempty"`
	Bandwidth *EventProxyServerBandwidth `json:"bandwidth,omitempty"`
}

type SfuMode string

const (
	SfuModeJanus SfuMode = "janus"
	SfuModeProxy SfuMode = "proxy"
)

type BackendServerInfoSfu struct {
	Mode SfuMode `json:"mode"`

	Janus   *BackendServerInfoSfuJanus  `json:"janus,omitempty"`
	Proxies []BackendServerInfoSfuProxy `json:"proxies,omitempty"`
}

type BackendServerInfoDialout struct {
	SessionId PublicSessionId `json:"sessionid"`
	Connected bool            `json:"connected"`
	Address   string          `json:"address,omitempty"`
	UserAgent string          `json:"useragent,omitempty"`
	Version   string          `json:"version,omitempty"`
	Features  []string        `json:"features,omitempty"`
}

type BackendServerInfoNats struct {
	Urls      []string `json:"urls"`
	Connected bool     `json:"connected"`

	ServerUrl     string `json:"serverurl,omitempty"`
	ServerID      string `json:"serverid,omitempty"`
	ServerVersion string `json:"version,omitempty"`
	ClusterName   string `json:"clustername,omitempty"`
}

type BackendServerInfoGrpc struct {
	Target    string `json:"target"`
	IP        string `json:"ip,omitempty"`
	Connected bool   `json:"connected"`

	Version string `json:"version,omitempty"`
}

type BackendServerInfoEtcd struct {
	Endpoints []string `json:"endpoints"`

	Active    string `json:"active,omitempty"`
	Connected *bool  `json:"connected,omitempty"`
}

type BackendServerInfo struct {
	Version  string   `json:"version"`
	Features []string `json:"features"`

	Sfu     *BackendServerInfoSfu      `json:"sfu,omitempty"`
	Dialout []BackendServerInfoDialout `json:"dialout,omitempty"`

	Nats *BackendServerInfoNats  `json:"nats,omitempty"`
	Grpc []BackendServerInfoGrpc `json:"grpc,omitempty"`
	Etcd *BackendServerInfoEtcd  `json:"etcd,omitempty"`
}
