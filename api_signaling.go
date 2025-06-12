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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/ice/v4"
	"github.com/pion/sdp/v3"
)

const (
	// Version 1.0 validates auth params against the Nextcloud instance.
	HelloVersionV1 = "1.0"

	// Version 2.0 validates auth params encoded as JWT.
	HelloVersionV2 = "2.0"

	ActorTypeUsers          = "users"
	ActorTypeFederatedUsers = "federated_users"
)

var (
	ErrNoSdp      = NewError("no_sdp", "Payload does not contain a SDP.")
	ErrInvalidSdp = NewError("invalid_sdp", "Payload does not contain a valid SDP.")

	ErrNoCandidate      = NewError("no_candidate", "Payload does not contain a candidate.")
	ErrInvalidCandidate = NewError("invalid_candidate", "Payload does not contain a valid candidate.")

	ErrCandidateFiltered = errors.New("candidate was filtered")
)

func makePtr[T any](v T) *T {
	return &v
}

func getStringMapEntry[T any](m map[string]interface{}, key string) (s T, ok bool) {
	var defaultValue T
	v, found := m[key]
	if !found {
		return defaultValue, false
	}

	s, ok = v.(T)
	return
}

// ClientMessage is a message that is sent from a client to the server.
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

	Room *RoomClientMessage `json:"room,omitempty"`

	Message *MessageClientMessage `json:"message,omitempty"`

	Control *ControlClientMessage `json:"control,omitempty"`

	Internal *InternalClientMessage `json:"internal,omitempty"`

	TransientData *TransientDataClientMessage `json:"transient,omitempty"`
}

func (m *ClientMessage) CheckValid() error {
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
		// No additional check required.
	case "room":
		if m.Room == nil {
			return fmt.Errorf("room missing")
		} else if err := m.Room.CheckValid(); err != nil {
			return err
		}
	case "message":
		if m.Message == nil {
			return fmt.Errorf("message missing")
		} else if err := m.Message.CheckValid(); err != nil {
			return err
		}
	case "control":
		if m.Control == nil {
			return fmt.Errorf("control missing")
		} else if err := m.Control.CheckValid(); err != nil {
			return err
		}
	case "internal":
		if m.Internal == nil {
			return fmt.Errorf("internal missing")
		} else if err := m.Internal.CheckValid(); err != nil {
			return err
		}
	case "transient":
		if m.TransientData == nil {
			return fmt.Errorf("transient missing")
		} else if err := m.TransientData.CheckValid(); err != nil {
			return err
		}
	}
	return nil
}

func (m ClientMessage) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", m, err)
	}
	return string(data)
}

func (m *ClientMessage) NewErrorServerMessage(e *Error) *ServerMessage {
	return &ServerMessage{
		Id:    m.Id,
		Type:  "error",
		Error: e,
	}
}

func (m *ClientMessage) NewWrappedErrorServerMessage(e error) *ServerMessage {
	if e, ok := e.(*Error); ok {
		return m.NewErrorServerMessage(e)
	}

	return m.NewErrorServerMessage(NewError("internal_error", e.Error()))
}

// ServerMessage is a message that is sent from the server to a client.
type ServerMessage struct {
	json.Marshaler
	json.Unmarshaler

	Id string `json:"id,omitempty"`

	Type string `json:"type"`

	Error *Error `json:"error,omitempty"`

	Welcome *WelcomeServerMessage `json:"welcome,omitempty"`

	Hello *HelloServerMessage `json:"hello,omitempty"`

	Bye *ByeServerMessage `json:"bye,omitempty"`

	Room *RoomServerMessage `json:"room,omitempty"`

	Message *MessageServerMessage `json:"message,omitempty"`

	Control *ControlServerMessage `json:"control,omitempty"`

	Event *EventServerMessage `json:"event,omitempty"`

	TransientData *TransientDataServerMessage `json:"transient,omitempty"`

	Internal *InternalServerMessage `json:"internal,omitempty"`

	Dialout *DialoutInternalClientMessage `json:"dialout,omitempty"`
}

func (r *ServerMessage) CloseAfterSend(session Session) bool {
	if r.Type == "bye" {
		return true
	}

	if r.Type == "event" {
		if evt := r.Event; evt != nil && evt.Target == "roomlist" && evt.Type == "disinvite" {
			// Only close session / connection if the disinvite was for the room
			// the session is currently in.
			if session != nil && evt.Disinvite != nil {
				if room := session.GetRoom(); room != nil && evt.Disinvite.RoomId == room.Id() {
					return true
				}
			}
		}
	}

	return false
}

func (r *ServerMessage) IsChatRefresh() bool {
	if r.Type != "message" || r.Message == nil || len(r.Message.Data) == 0 {
		return false
	}

	var data MessageServerMessageData
	if err := json.Unmarshal(r.Message.Data, &data); err != nil {
		return false
	}

	if data.Type != "chat" || data.Chat == nil {
		return false
	}

	return data.Chat.Refresh
}

func (r *ServerMessage) IsParticipantsUpdate() bool {
	if r.Type != "event" || r.Event == nil {
		return false
	}
	if event := r.Event; event.Target != "participants" || event.Type != "update" {
		return false
	}
	return true
}

func (r *ServerMessage) String() string {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", r, err)
	}
	return string(data)
}

type Error struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

func NewError(code string, message string) *Error {
	return NewErrorDetail(code, message, nil)
}

func NewErrorDetail(code string, message string, details interface{}) *Error {
	var rawDetails json.RawMessage
	if details != nil {
		var err error
		if rawDetails, err = json.Marshal(details); err != nil {
			log.Printf("Could not marshal details %+v for error %s with %s: %s", details, code, message, err)
			return NewError("internal_error", "Could not marshal error details")
		}
	}

	return &Error{
		Code:    code,
		Message: message,
		Details: rawDetails,
	}
}

func (e *Error) Error() string {
	return e.Message
}

type WelcomeServerMessage struct {
	Version  string   `json:"version"`
	Features []string `json:"features,omitempty"`
	Country  string   `json:"country,omitempty"`
}

func NewWelcomeServerMessage(version string, feature ...string) *WelcomeServerMessage {
	message := &WelcomeServerMessage{
		Version:  version,
		Features: feature,
	}
	if len(feature) > 0 {
		sort.Strings(message.Features)
	}
	return message
}

func (m *WelcomeServerMessage) AddFeature(feature ...string) {
	newFeatures := make([]string, len(m.Features))
	copy(newFeatures, m.Features)
	for _, feat := range feature {
		found := false
		for _, f := range newFeatures {
			if f == feat {
				found = true
				break
			}
		}
		if !found {
			newFeatures = append(newFeatures, feat)
		}
	}
	sort.Strings(newFeatures)
	m.Features = newFeatures
}

func (m *WelcomeServerMessage) RemoveFeature(feature ...string) {
	newFeatures := make([]string, len(m.Features))
	copy(newFeatures, m.Features)
	for _, feat := range feature {
		idx := sort.SearchStrings(newFeatures, feat)
		if idx < len(newFeatures) && newFeatures[idx] == feat {
			newFeatures = append(newFeatures[:idx], newFeatures[idx+1:]...)
		}
	}
	m.Features = newFeatures
}

func (m *WelcomeServerMessage) HasFeature(feature string) bool {
	for _, f := range m.Features {
		f = strings.TrimSpace(f)
		if f == feature {
			return true
		}
	}

	return false
}

const (
	HelloClientTypeClient     = "client"
	HelloClientTypeInternal   = "internal"
	HelloClientTypeFederation = "federation"

	HelloClientTypeVirtual = "virtual"
)

func hasStandardPort(u *url.URL) bool {
	switch u.Scheme {
	case "http":
		return u.Port() == "80"
	case "https":
		return u.Port() == "443"
	default:
		return false
	}
}

type ClientTypeInternalAuthParams struct {
	Random string `json:"random"`
	Token  string `json:"token"`

	Backend       string `json:"backend"`
	parsedBackend *url.URL
}

func (p *ClientTypeInternalAuthParams) CheckValid() error {
	if p.Backend == "" {
		return fmt.Errorf("backend missing")
	} else if u, err := url.Parse(p.Backend); err != nil {
		return err
	} else {
		if strings.Contains(u.Host, ":") && hasStandardPort(u) {
			u.Host = u.Hostname()
		}

		p.parsedBackend = u
	}
	return nil
}

type HelloV2AuthParams struct {
	Token string `json:"token"`
}

func (p *HelloV2AuthParams) CheckValid() error {
	if p.Token == "" {
		return fmt.Errorf("token missing")
	}
	return nil
}

type AuthTokenClaims interface {
	jwt.Claims

	GetUserData() json.RawMessage
}

type HelloV2TokenClaims struct {
	jwt.RegisteredClaims

	UserData json.RawMessage `json:"userdata,omitempty"`
}

func (c *HelloV2TokenClaims) GetUserData() json.RawMessage {
	return c.UserData
}

type FederationAuthParams struct {
	Token string `json:"token"`
}

func (p *FederationAuthParams) CheckValid() error {
	if p.Token == "" {
		return fmt.Errorf("token missing")
	}
	return nil
}

type FederationTokenClaims struct {
	jwt.RegisteredClaims

	UserData json.RawMessage `json:"userdata,omitempty"`
}

func (c *FederationTokenClaims) GetUserData() json.RawMessage {
	return c.UserData
}

type HelloClientMessageAuth struct {
	// The client type that is connecting. Leave empty to use the default
	// "HelloClientTypeClient"
	Type string `json:"type,omitempty"`

	Params json.RawMessage `json:"params"`

	Url       string `json:"url"`
	parsedUrl *url.URL

	internalParams   ClientTypeInternalAuthParams
	helloV2Params    HelloV2AuthParams
	federationParams FederationAuthParams
}

// Type "hello"

type HelloClientMessage struct {
	Version string `json:"version"`

	ResumeId string `json:"resumeid"`

	Features []string `json:"features,omitempty"`

	// The authentication credentials.
	Auth *HelloClientMessageAuth `json:"auth,omitempty"`
}

func (m *HelloClientMessage) CheckValid() error {
	if m.Version != HelloVersionV1 && m.Version != HelloVersionV2 {
		return InvalidHelloVersion
	}
	if m.ResumeId == "" {
		if m.Auth == nil || len(m.Auth.Params) == 0 {
			return fmt.Errorf("params missing")
		}
		if m.Auth.Type == "" {
			m.Auth.Type = HelloClientTypeClient
		}
		switch m.Auth.Type {
		case HelloClientTypeClient:
			fallthrough
		case HelloClientTypeFederation:
			if m.Auth.Url == "" {
				return fmt.Errorf("url missing")
			} else if u, err := url.ParseRequestURI(m.Auth.Url); err != nil {
				return err
			} else {
				if strings.Contains(u.Host, ":") && hasStandardPort(u) {
					u.Host = u.Hostname()
				}

				m.Auth.parsedUrl = u
			}

			switch m.Version {
			case HelloVersionV1:
				// No additional validation necessary.
			case HelloVersionV2:
				switch m.Auth.Type {
				case HelloClientTypeClient:
					if err := json.Unmarshal(m.Auth.Params, &m.Auth.helloV2Params); err != nil {
						return err
					} else if err := m.Auth.helloV2Params.CheckValid(); err != nil {
						return err
					}
				case HelloClientTypeFederation:
					if err := json.Unmarshal(m.Auth.Params, &m.Auth.federationParams); err != nil {
						return err
					} else if err := m.Auth.federationParams.CheckValid(); err != nil {
						return err
					}
				}
			}
		case HelloClientTypeInternal:
			if err := json.Unmarshal(m.Auth.Params, &m.Auth.internalParams); err != nil {
				return err
			} else if err := m.Auth.internalParams.CheckValid(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported auth type")
		}
	}
	return nil
}

const (
	// Features to send to all clients.
	ServerFeatureMcu                   = "mcu"
	ServerFeatureSimulcast             = "simulcast"
	ServerFeatureUpdateSdp             = "update-sdp"
	ServerFeatureAudioVideoPermissions = "audio-video-permissions"
	ServerFeatureTransientData         = "transient-data"
	ServerFeatureInCallAll             = "incall-all"
	ServerFeatureWelcome               = "welcome"
	ServerFeatureHelloV2               = "hello-v2"
	ServerFeatureSwitchTo              = "switchto"
	ServerFeatureDialout               = "dialout"
	ServerFeatureFederation            = "federation"
	ServerFeatureRecipientCall         = "recipient-call"
	ServerFeatureJoinFeatures          = "join-features"
	ServerFeatureOfferCodecs           = "offer-codecs"
	ServerFeatureServerInfo            = "serverinfo"

	// Features to send to internal clients only.
	ServerFeatureInternalVirtualSessions = "virtual-sessions"

	// Possible client features from the "hello" request.
	ClientFeatureInternalInCall = "internal-incall"
	ClientFeatureStartDialout   = "start-dialout"
)

var (
	DefaultFeatures = []string{
		ServerFeatureAudioVideoPermissions,
		ServerFeatureTransientData,
		ServerFeatureInCallAll,
		ServerFeatureWelcome,
		ServerFeatureHelloV2,
		ServerFeatureSwitchTo,
		ServerFeatureDialout,
		ServerFeatureFederation,
		ServerFeatureRecipientCall,
		ServerFeatureJoinFeatures,
		ServerFeatureOfferCodecs,
		ServerFeatureServerInfo,
	}
	DefaultFeaturesInternal = []string{
		ServerFeatureInternalVirtualSessions,
		ServerFeatureTransientData,
		ServerFeatureInCallAll,
		ServerFeatureWelcome,
		ServerFeatureHelloV2,
		ServerFeatureSwitchTo,
		ServerFeatureDialout,
		ServerFeatureFederation,
		ServerFeatureRecipientCall,
		ServerFeatureJoinFeatures,
		ServerFeatureOfferCodecs,
		ServerFeatureServerInfo,
	}
	DefaultWelcomeFeatures = []string{
		ServerFeatureAudioVideoPermissions,
		ServerFeatureInternalVirtualSessions,
		ServerFeatureTransientData,
		ServerFeatureInCallAll,
		ServerFeatureWelcome,
		ServerFeatureHelloV2,
		ServerFeatureSwitchTo,
		ServerFeatureDialout,
		ServerFeatureFederation,
		ServerFeatureRecipientCall,
		ServerFeatureJoinFeatures,
		ServerFeatureOfferCodecs,
		ServerFeatureServerInfo,
	}
)

type HelloServerMessage struct {
	Version string `json:"version"`

	SessionId string `json:"sessionid"`
	ResumeId  string `json:"resumeid"`
	UserId    string `json:"userid"`

	// TODO: Remove once all clients have switched to the "welcome" message.
	Server *WelcomeServerMessage `json:"server,omitempty"`
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

// Type "room"

type RoomClientMessage struct {
	RoomId    string `json:"roomid"`
	SessionId string `json:"sessionid,omitempty"`

	Federation *RoomFederationMessage `json:"federation,omitempty"`
}

func (m *RoomClientMessage) CheckValid() error {
	// No additional validation required.
	if m.Federation != nil {
		if err := m.Federation.CheckValid(); err != nil {
			return err
		}
	}

	return nil
}

type RoomFederationMessage struct {
	SignalingUrl       string `json:"signaling"`
	parsedSignalingUrl *url.URL

	NextcloudUrl       string `json:"url"`
	parsedNextcloudUrl *url.URL

	RoomId string `json:"roomid,omitempty"`
	Token  string `json:"token"`
}

func (m *RoomFederationMessage) CheckValid() error {
	if m.SignalingUrl == "" {
		return errors.New("signaling url missing")
	}

	if m.SignalingUrl[len(m.SignalingUrl)-1] != '/' {
		m.SignalingUrl += "/"
	}
	if u, err := url.Parse(m.SignalingUrl); err != nil {
		return fmt.Errorf("invalid signaling url: %w", err)
	} else {
		m.parsedSignalingUrl = u
	}
	if m.NextcloudUrl == "" {
		return errors.New("nextcloud url missing")
	} else if u, err := url.Parse(m.NextcloudUrl); err != nil {
		return fmt.Errorf("invalid nextcloud url: %w", err)
	} else {
		m.parsedNextcloudUrl = u
	}
	if m.Token == "" {
		return errors.New("token missing")
	}

	return nil
}

type RoomServerMessage struct {
	RoomId     string          `json:"roomid"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type RoomErrorDetails struct {
	Room *RoomServerMessage `json:"room"`
}

// Type "message"

const (
	RecipientTypeSession = "session"
	RecipientTypeUser    = "user"
	RecipientTypeRoom    = "room"
	RecipientTypeCall    = "call"
)

type MessageClientMessageRecipient struct {
	Type string `json:"type"`

	SessionId string `json:"sessionid,omitempty"`
	UserId    string `json:"userid,omitempty"`
}

type MessageClientMessage struct {
	Recipient MessageClientMessageRecipient `json:"recipient"`

	Data json.RawMessage `json:"data"`
}

type MessageClientMessageData struct {
	Type     string                 `json:"type"`
	Sid      string                 `json:"sid"`
	RoomType string                 `json:"roomType"`
	Payload  map[string]interface{} `json:"payload"`

	// Only supported if Type == "offer"
	Bitrate     int    `json:"bitrate,omitempty"`
	AudioCodec  string `json:"audiocodec,omitempty"`
	VideoCodec  string `json:"videocodec,omitempty"`
	VP9Profile  string `json:"vp9profile,omitempty"`
	H264Profile string `json:"h264profile,omitempty"`

	offerSdp  *sdp.SessionDescription // Only set if Type == "offer"
	answerSdp *sdp.SessionDescription // Only set if Type == "answer"
	candidate ice.Candidate           // Only set if Type == "candidate"
}

func (m *MessageClientMessageData) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", m, err)
	}
	return string(data)
}

func parseSDP(s string) (*sdp.SessionDescription, error) {
	var sdp sdp.SessionDescription
	if err := sdp.UnmarshalString(s); err != nil {
		return nil, NewErrorDetail("invalid_sdp", "Error parsing SDP from payload.", map[string]interface{}{
			"error": err.Error(),
		})
	}

	for _, m := range sdp.MediaDescriptions {
		for idx, a := range m.Attributes {
			if !a.IsICECandidate() {
				continue
			}

			if _, err := ice.UnmarshalCandidate(a.Value); err != nil {
				return nil, NewErrorDetail("invalid_sdp", "Error parsing candidate from media description.", map[string]interface{}{
					"media": m.MediaName.Media,
					"idx":   idx,
					"error": err.Error(),
				})
			}
		}
	}

	return &sdp, nil
}

var (
	emptyCandidate = &ice.CandidateHost{}
)

func (m *MessageClientMessageData) CheckValid() error {
	if m.RoomType != "" && !IsValidStreamType(m.RoomType) {
		return fmt.Errorf("invalid room type: %s", m.RoomType)
	}
	switch m.Type {
	case "offer", "answer":
		sdpValue, found := m.Payload["sdp"]
		if !found {
			return ErrNoSdp
		}
		sdpText, ok := sdpValue.(string)
		if !ok {
			return ErrInvalidSdp
		}

		sdp, err := parseSDP(sdpText)
		if err != nil {
			return err
		}

		switch m.Type {
		case "offer":
			m.offerSdp = sdp
		case "answer":
			m.answerSdp = sdp
		}
	case "candidate":
		candValue, found := m.Payload["candidate"]
		if !found {
			return ErrNoCandidate
		}
		candItem, ok := candValue.(map[string]interface{})
		if !ok {
			return ErrInvalidCandidate
		}
		candValue, ok = candItem["candidate"]
		if !ok {
			return ErrInvalidCandidate
		}
		candText, ok := candValue.(string)
		if !ok {
			return ErrInvalidCandidate
		}

		if candText == "" {
			m.candidate = emptyCandidate
		} else {
			cand, err := ice.UnmarshalCandidate(candText)
			if err != nil {
				return NewErrorDetail("invalid_candidate", "Error parsing candidate from payload.", map[string]interface{}{
					"error": err.Error(),
				})
			}
			m.candidate = cand
		}
	}
	return nil
}

func FilterCandidate(c ice.Candidate, allowed *AllowedIps, blocked *AllowedIps) bool {
	switch c {
	case nil:
		return true
	case emptyCandidate:
		return false
	}

	ip := net.ParseIP(c.Address())
	if len(ip) == 0 || ip.IsUnspecified() {
		return true
	}

	// Whitelist has preference.
	if allowed != nil && allowed.Allowed(ip) {
		return false
	}

	// Check if address is blocked manually.
	if blocked != nil && blocked.Allowed(ip) {
		return true
	}

	return false
}

func FilterSDPCandidates(s *sdp.SessionDescription, allowed *AllowedIps, blocked *AllowedIps) bool {
	modified := false
	for _, m := range s.MediaDescriptions {
		m.Attributes = slices.DeleteFunc(m.Attributes, func(a sdp.Attribute) bool {
			if !a.IsICECandidate() {
				return false
			}

			if a.Value == "" {
				return false
			}

			c, err := ice.UnmarshalCandidate(a.Value)
			if err != nil || FilterCandidate(c, allowed, blocked) {
				modified = true
				return true
			}

			return false
		})
	}

	return modified
}

func (m *MessageClientMessage) CheckValid() error {
	if len(m.Data) == 0 {
		return fmt.Errorf("message empty")
	}
	switch m.Recipient.Type {
	case RecipientTypeRoom:
		fallthrough
	case RecipientTypeCall:
		// No additional checks required.
	case RecipientTypeSession:
		if m.Recipient.SessionId == "" {
			return fmt.Errorf("session id missing")
		}
	case RecipientTypeUser:
		if m.Recipient.UserId == "" {
			return fmt.Errorf("user id missing")
		}
	default:
		return fmt.Errorf("unsupported recipient type %v", m.Recipient.Type)
	}
	return nil
}

type MessageServerMessageSender struct {
	Type string `json:"type"`

	SessionId string `json:"sessionid,omitempty"`
	UserId    string `json:"userid,omitempty"`
}

type MessageServerMessageDataChat struct {
	Refresh bool `json:"refresh"`
}

type MessageServerMessageData struct {
	Type string `json:"type"`

	Chat *MessageServerMessageDataChat `json:"chat,omitempty"`
}

type MessageServerMessage struct {
	Sender    *MessageServerMessageSender    `json:"sender"`
	Recipient *MessageClientMessageRecipient `json:"recipient,omitempty"`

	Data json.RawMessage `json:"data"`
}

// Type "control"

type ControlClientMessage struct {
	MessageClientMessage
}

func (m *ControlClientMessage) CheckValid() error {
	return m.MessageClientMessage.CheckValid()
}

type ControlServerMessage struct {
	Sender    *MessageServerMessageSender    `json:"sender"`
	Recipient *MessageClientMessageRecipient `json:"recipient,omitempty"`

	Data json.RawMessage `json:"data"`
}

// Type "internal"

type CommonSessionInternalClientMessage struct {
	SessionId string `json:"sessionid"`

	RoomId string `json:"roomid"`
}

func (m *CommonSessionInternalClientMessage) CheckValid() error {
	if m.SessionId == "" {
		return fmt.Errorf("sessionid missing")
	}
	if m.RoomId == "" {
		return fmt.Errorf("roomid missing")
	}
	return nil
}

type AddSessionOptions struct {
	ActorId   string `json:"actorId,omitempty"`
	ActorType string `json:"actorType,omitempty"`
}

type AddSessionInternalClientMessage struct {
	CommonSessionInternalClientMessage

	UserId string          `json:"userid,omitempty"`
	User   json.RawMessage `json:"user,omitempty"`
	Flags  uint32          `json:"flags,omitempty"`
	InCall *int            `json:"incall,omitempty"`

	Options *AddSessionOptions `json:"options,omitempty"`
}

func (m *AddSessionInternalClientMessage) CheckValid() error {
	return m.CommonSessionInternalClientMessage.CheckValid()
}

type UpdateSessionInternalClientMessage struct {
	CommonSessionInternalClientMessage

	Flags  *uint32 `json:"flags,omitempty"`
	InCall *int    `json:"incall,omitempty"`
}

func (m *UpdateSessionInternalClientMessage) CheckValid() error {
	return m.CommonSessionInternalClientMessage.CheckValid()
}

type RemoveSessionInternalClientMessage struct {
	CommonSessionInternalClientMessage

	UserId string `json:"userid,omitempty"`
}

func (m *RemoveSessionInternalClientMessage) CheckValid() error {
	return m.CommonSessionInternalClientMessage.CheckValid()
}

type InCallInternalClientMessage struct {
	InCall int `json:"incall"`
}

func (m *InCallInternalClientMessage) CheckValid() error {
	return nil
}

type DialoutStatus string

var (
	DialoutStatusAccepted  DialoutStatus = "accepted"
	DialoutStatusRinging   DialoutStatus = "ringing"
	DialoutStatusConnected DialoutStatus = "connected"
	DialoutStatusRejected  DialoutStatus = "rejected"
	DialoutStatusCleared   DialoutStatus = "cleared"
)

type DialoutStatusInternalClientMessage struct {
	CallId string        `json:"callid"`
	Status DialoutStatus `json:"status"`

	// Cause is set if Status is "cleared" or "rejected".
	Cause   string `json:"cause,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type DialoutInternalClientMessage struct {
	Type string `json:"type"`

	RoomId string `json:"roomid,omitempty"`

	Error *Error `json:"error,omitempty"`

	Status *DialoutStatusInternalClientMessage `json:"status,omitempty"`
}

func (m *DialoutInternalClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return errors.New("type missing")
	case "error":
		if m.Error == nil {
			return errors.New("error missing")
		}
	case "status":
		if m.Status == nil {
			return errors.New("status missing")
		}
	}
	return nil
}

type InternalClientMessage struct {
	Type string `json:"type"`

	AddSession *AddSessionInternalClientMessage `json:"addsession,omitempty"`

	UpdateSession *UpdateSessionInternalClientMessage `json:"updatesession,omitempty"`

	RemoveSession *RemoveSessionInternalClientMessage `json:"removesession,omitempty"`

	InCall *InCallInternalClientMessage `json:"incall,omitempty"`

	Dialout *DialoutInternalClientMessage `json:"dialout,omitempty"`
}

func (m *InternalClientMessage) CheckValid() error {
	switch m.Type {
	case "":
		return errors.New("type missing")
	case "addsession":
		if m.AddSession == nil {
			return fmt.Errorf("addsession missing")
		} else if err := m.AddSession.CheckValid(); err != nil {
			return err
		}
	case "updatesession":
		if m.UpdateSession == nil {
			return fmt.Errorf("updatesession missing")
		} else if err := m.UpdateSession.CheckValid(); err != nil {
			return err
		}
	case "removesession":
		if m.RemoveSession == nil {
			return fmt.Errorf("removesession missing")
		} else if err := m.RemoveSession.CheckValid(); err != nil {
			return err
		}
	case "incall":
		if m.InCall == nil {
			return fmt.Errorf("incall missing")
		} else if err := m.InCall.CheckValid(); err != nil {
			return err
		}
	case "dialout":
		if m.Dialout == nil {
			return fmt.Errorf("dialout missing")
		} else if err := m.Dialout.CheckValid(); err != nil {
			return err
		}
	}
	return nil
}

type InternalServerDialoutRequest struct {
	RoomId  string `json:"roomid"`
	Backend string `json:"backend"`

	Request *BackendRoomDialoutRequest `json:"request"`
}

type InternalServerMessage struct {
	Type string `json:"type"`

	Dialout *InternalServerDialoutRequest `json:"dialout,omitempty"`
}

// Type "event"

type RoomEventServerMessage struct {
	RoomId     string          `json:"roomid"`
	Properties json.RawMessage `json:"properties,omitempty"`
	// TODO(jojo): Change "InCall" to "int" when #914 has landed in NC Talk.
	InCall  json.RawMessage          `json:"incall,omitempty"`
	Changed []map[string]interface{} `json:"changed,omitempty"`
	Users   []map[string]interface{} `json:"users,omitempty"`

	All bool `json:"all,omitempty"`
}

func (m *RoomEventServerMessage) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", m, err)
	}
	return string(data)
}

const (
	DisinviteReasonDisinvited = "disinvited"
	DisinviteReasonDeleted    = "deleted"
)

type RoomDisinviteEventServerMessage struct {
	RoomEventServerMessage

	Reason string `json:"reason"`
}

type RoomEventMessage struct {
	RoomId string          `json:"roomid"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type RoomFlagsServerMessage struct {
	RoomId    string `json:"roomid"`
	SessionId string `json:"sessionid"`
	Flags     uint32 `json:"flags"`
}

type ChatComment map[string]interface{}

type RoomEventMessageDataChat struct {
	Comment *ChatComment `json:"comment,omitempty"`
}

type RoomEventMessageData struct {
	Type string `json:"type"`

	Chat *RoomEventMessageDataChat `json:"chat,omitempty"`
}

type EventServerMessage struct {
	Target string `json:"target"`
	Type   string `json:"type"`

	// Used for target "room"
	Join     []*EventServerMessageSessionEntry `json:"join,omitempty"`
	Leave    []string                          `json:"leave,omitempty"`
	Change   []*EventServerMessageSessionEntry `json:"change,omitempty"`
	SwitchTo *EventServerMessageSwitchTo       `json:"switchto,omitempty"`
	Resumed  *bool                             `json:"resumed,omitempty"`

	// Used for target "roomlist" / "participants"
	Invite    *RoomEventServerMessage          `json:"invite,omitempty"`
	Disinvite *RoomDisinviteEventServerMessage `json:"disinvite,omitempty"`
	Update    *RoomEventServerMessage          `json:"update,omitempty"`
	Flags     *RoomFlagsServerMessage          `json:"flags,omitempty"`

	// Used for target "message"
	Message *RoomEventMessage `json:"message,omitempty"`
}

func (m *EventServerMessage) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("Could not serialize %#v: %s", m, err)
	}
	return string(data)
}

type EventServerMessageSessionEntry struct {
	SessionId     string          `json:"sessionid"`
	UserId        string          `json:"userid"`
	Features      []string        `json:"features,omitempty"`
	User          json.RawMessage `json:"user,omitempty"`
	RoomSessionId string          `json:"roomsessionid,omitempty"`
	Federated     bool            `json:"federated,omitempty"`
}

func (e *EventServerMessageSessionEntry) Clone() *EventServerMessageSessionEntry {
	return &EventServerMessageSessionEntry{
		SessionId:     e.SessionId,
		UserId:        e.UserId,
		Features:      e.Features,
		User:          e.User,
		RoomSessionId: e.RoomSessionId,
		Federated:     e.Federated,
	}
}

type EventServerMessageSwitchTo struct {
	RoomId  string          `json:"roomid"`
	Details json.RawMessage `json:"details,omitempty"`
}

// MCU-related types

type AnswerOfferMessage struct {
	To       string                 `json:"to"`
	From     string                 `json:"from"`
	Type     string                 `json:"type"`
	RoomType string                 `json:"roomType"`
	Payload  map[string]interface{} `json:"payload"`
	Sid      string                 `json:"sid,omitempty"`
}

// Type "transient"

type TransientDataClientMessage struct {
	Type string `json:"type"`

	Key   string          `json:"key,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	TTL   time.Duration   `json:"ttl,omitempty"`
}

func (m *TransientDataClientMessage) CheckValid() error {
	switch m.Type {
	case "set":
		if m.Key == "" {
			return fmt.Errorf("key missing")
		}
		// A "nil" value is allowed and will remove the key.
	case "remove":
		if m.Key == "" {
			return fmt.Errorf("key missing")
		}
	}
	return nil
}

type TransientDataServerMessage struct {
	Type string `json:"type"`

	Key      string                 `json:"key,omitempty"`
	OldValue interface{}            `json:"oldvalue,omitempty"`
	Value    interface{}            `json:"value,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}
