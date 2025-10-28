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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/sdp/v3"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

var (
	// Warn if a session has 32 or more pending messages.
	warnPendingMessagesCount = 32

	// The "/api/v1/signaling/" URL will be changed to use "v3" as the "signaling-v3"
	// feature is returned by the capabilities endpoint.
	PathToOcsSignalingBackend = "ocs/v2.php/apps/spreed/api/v1/signaling/backend"
)

const (
	FederatedRoomSessionIdPrefix = "federated|"
)

// ResponseHandlerFunc will return "true" has been fully processed.
type ResponseHandlerFunc func(message *ClientMessage) bool

type ClientSession struct {
	hub       *Hub
	events    AsyncEvents
	privateId PrivateSessionId
	publicId  PublicSessionId
	data      *SessionIdData
	ctx       context.Context
	closeFunc context.CancelFunc

	clientType ClientType
	features   []string
	userId     string
	userData   json.RawMessage

	parseUserData func() (api.StringMap, error)

	inCall Flags
	// +checklocks:mu
	supportsPermissions bool
	// +checklocks:mu
	permissions map[Permission]bool

	backend          *Backend
	backendUrl       string
	parsedBackendUrl *url.URL

	mu sync.Mutex

	// +checklocks:mu
	client       HandlerClient
	room         atomic.Pointer[Room]
	roomJoinTime atomic.Int64
	federation   atomic.Pointer[FederationClient]

	roomSessionIdLock sync.RWMutex
	// +checklocks:roomSessionIdLock
	roomSessionId RoomSessionId

	publisherWaiters ChannelWaiters // +checklocksignore

	// +checklocks:mu
	publishers map[StreamType]McuPublisher
	// +checklocks:mu
	subscribers map[StreamId]McuSubscriber

	// +checklocks:mu
	pendingClientMessages []*ServerMessage
	// +checklocks:mu
	hasPendingChat bool
	// +checklocks:mu
	hasPendingParticipantsUpdate bool

	// +checklocks:mu
	virtualSessions map[*VirtualSession]bool

	seenJoinedLock sync.Mutex
	// +checklocks:seenJoinedLock
	seenJoinedEvents map[PublicSessionId]bool

	responseHandlersLock sync.Mutex
	// +checklocks:responseHandlersLock
	responseHandlers map[string]ResponseHandlerFunc
}

func NewClientSession(hub *Hub, privateId PrivateSessionId, publicId PublicSessionId, data *SessionIdData, backend *Backend, hello *HelloClientMessage, auth *BackendClientAuthResponse) (*ClientSession, error) {
	ctx, closeFunc := context.WithCancel(context.Background())
	s := &ClientSession{
		hub:       hub,
		events:    hub.events,
		privateId: privateId,
		publicId:  publicId,
		data:      data,
		ctx:       ctx,
		closeFunc: closeFunc,

		clientType:    hello.Auth.Type,
		features:      hello.Features,
		userId:        auth.UserId,
		userData:      auth.User,
		parseUserData: parseUserData(auth.User),

		backend: backend,
	}
	if s.clientType == HelloClientTypeInternal {
		s.backendUrl = hello.Auth.internalParams.Backend
		s.parsedBackendUrl = hello.Auth.internalParams.parsedBackend
		if !s.HasFeature(ClientFeatureInternalInCall) {
			s.SetInCall(FlagInCall | FlagWithAudio)
		}
	} else {
		s.backendUrl = hello.Auth.Url
		s.parsedBackendUrl = hello.Auth.parsedUrl
	}

	if err := s.SubscribeEvents(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ClientSession) Context() context.Context {
	return s.ctx
}

func (s *ClientSession) PrivateId() PrivateSessionId {
	return s.privateId
}

func (s *ClientSession) PublicId() PublicSessionId {
	return s.publicId
}

func (s *ClientSession) RoomSessionId() RoomSessionId {
	s.roomSessionIdLock.RLock()
	defer s.roomSessionIdLock.RUnlock()
	return s.roomSessionId
}

func (s *ClientSession) Data() *SessionIdData {
	return s.data
}

func (s *ClientSession) ClientType() ClientType {
	return s.clientType
}

// GetInCall is only used for internal clients.
func (s *ClientSession) GetInCall() int {
	return int(s.inCall.Get())
}

func (s *ClientSession) SetInCall(inCall int) bool {
	if inCall < 0 {
		inCall = 0
	}

	return s.inCall.Set(uint32(inCall))
}

func (s *ClientSession) GetFeatures() []string {
	return s.features
}

func (s *ClientSession) HasFeature(feature string) bool {
	return slices.Contains(s.features, feature)
}

// HasPermission checks if the session has the passed permissions.
func (s *ClientSession) HasPermission(permission Permission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasPermissionLocked(permission)
}

func (s *ClientSession) GetPermissions() []Permission {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Permission, len(s.permissions))
	for p, ok := range s.permissions {
		if ok {
			result = append(result, p)
		}
	}
	return result
}

// HasAnyPermission checks if the session has one of the passed permissions.
func (s *ClientSession) HasAnyPermission(permission ...Permission) bool {
	if len(permission) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasAnyPermissionLocked(permission...)
}

// +checklocks:s.mu
func (s *ClientSession) hasAnyPermissionLocked(permission ...Permission) bool {
	if len(permission) == 0 {
		return false
	}

	return slices.ContainsFunc(permission, s.hasPermissionLocked)
}

// +checklocks:s.mu
func (s *ClientSession) hasPermissionLocked(permission Permission) bool {
	if !s.supportsPermissions {
		// Old-style session that doesn't receive permissions from Nextcloud.
		if result, found := DefaultPermissionOverrides[permission]; found {
			return result
		}
		return true
	}

	if val, found := s.permissions[permission]; found {
		return val
	}
	return false
}

func (s *ClientSession) SetPermissions(permissions []Permission) {
	var p map[Permission]bool
	for _, permission := range permissions {
		if p == nil {
			p = make(map[Permission]bool)
		}
		p[permission] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.supportsPermissions && maps.Equal(s.permissions, p) {
		return
	}

	s.permissions = p
	s.supportsPermissions = true
	log.Printf("Permissions of session %s changed: %s", s.PublicId(), permissions)
}

func (s *ClientSession) Backend() *Backend {
	return s.backend
}

func (s *ClientSession) BackendUrl() string {
	return s.backendUrl
}

func (s *ClientSession) ParsedBackendUrl() *url.URL {
	return s.parsedBackendUrl
}

func (s *ClientSession) ParsedBackendOcsUrl() *url.URL {
	return s.parsedBackendUrl.JoinPath(PathToOcsSignalingBackend)
}

func (s *ClientSession) AuthUserId() string {
	return s.userId
}

func (s *ClientSession) UserId() string {
	userId := s.userId
	if userId == "" {
		if room := s.GetRoom(); room != nil {
			if data := room.GetRoomSessionData(s); data != nil {
				userId = data.UserId
			}
		}
	}
	return userId
}

func (s *ClientSession) UserData() json.RawMessage {
	return s.userData
}

func (s *ClientSession) ParsedUserData() (api.StringMap, error) {
	return s.parseUserData()
}

func (s *ClientSession) SetRoom(room *Room) {
	s.room.Store(room)
	s.onRoomSet(room != nil)
}

func (s *ClientSession) onRoomSet(hasRoom bool) {
	if hasRoom {
		s.roomJoinTime.Store(time.Now().UnixNano())
	} else {
		s.roomJoinTime.Store(0)
	}

	s.seenJoinedLock.Lock()
	defer s.seenJoinedLock.Unlock()
	s.seenJoinedEvents = nil
}

func (s *ClientSession) GetFederationClient() *FederationClient {
	return s.federation.Load()
}

func (s *ClientSession) SetFederationClient(federation *FederationClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.doLeaveRoom(true)
	s.onRoomSet(federation != nil)

	if prev := s.federation.Swap(federation); prev != nil && prev != federation {
		prev.Close()
	}
}

func (s *ClientSession) GetRoom() *Room {
	return s.room.Load()
}

func (s *ClientSession) getRoomJoinTime() time.Time {
	t := s.roomJoinTime.Load()
	if t == 0 {
		return time.Time{}
	}

	return time.Unix(0, t)
}

// +checklocks:s.mu
func (s *ClientSession) releaseMcuObjects() {
	if len(s.publishers) > 0 {
		go func(publishers map[StreamType]McuPublisher) {
			ctx := context.Background()
			for _, publisher := range publishers {
				publisher.Close(ctx)
			}
		}(s.publishers)
		s.publishers = nil
	}
	if len(s.subscribers) > 0 {
		go func(subscribers map[StreamId]McuSubscriber) {
			ctx := context.Background()
			for _, subscriber := range subscribers {
				subscriber.Close(ctx)
			}
		}(s.subscribers)
		s.subscribers = nil
	}
}

func (s *ClientSession) Close() {
	s.closeAndWait(true)
}

func (s *ClientSession) closeAndWait(wait bool) {
	s.closeFunc()
	s.hub.removeSession(s)

	if prev := s.federation.Swap(nil); prev != nil {
		prev.Close()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userId != "" {
		s.events.UnregisterUserListener(s.userId, s.backend, s)
	}
	s.events.UnregisterSessionListener(s.publicId, s.backend, s)
	go func(virtualSessions map[*VirtualSession]bool) {
		for session := range virtualSessions {
			session.Close()
		}
	}(s.virtualSessions)
	s.virtualSessions = nil
	s.releaseMcuObjects()
	s.clearClientLocked(nil)
	s.backend.RemoveSession(s)
}

func (s *ClientSession) SubscribeEvents() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userId != "" {
		if err := s.events.RegisterUserListener(s.userId, s.backend, s); err != nil {
			return err
		}
	}

	return s.events.RegisterSessionListener(s.publicId, s.backend, s)
}

func (s *ClientSession) UpdateRoomSessionId(roomSessionId RoomSessionId) error {
	s.roomSessionIdLock.Lock()
	defer s.roomSessionIdLock.Unlock()

	if s.roomSessionId == roomSessionId {
		return nil
	}

	if err := s.hub.roomSessions.SetRoomSession(s, roomSessionId); err != nil {
		return err
	}

	if roomSessionId != "" {
		if room := s.GetRoom(); room != nil {
			log.Printf("Session %s updated room session id to %s in room %s", s.PublicId(), roomSessionId, room.Id())
		} else if client := s.GetFederationClient(); client != nil {
			log.Printf("Session %s updated room session id to %s in federated room %s", s.PublicId(), roomSessionId, client.RemoteRoomId())
		} else {
			log.Printf("Session %s updated room session id to %s in unknown room", s.PublicId(), roomSessionId)
		}
	} else {
		if room := s.GetRoom(); room != nil {
			log.Printf("Session %s cleared room session id in room %s", s.PublicId(), room.Id())
		} else if client := s.GetFederationClient(); client != nil {
			log.Printf("Session %s cleared room session id in federated room %s", s.PublicId(), client.RemoteRoomId())
		} else {
			log.Printf("Session %s cleared room session id in unknown room", s.PublicId())
		}
	}

	s.roomSessionId = roomSessionId
	return nil
}

func (s *ClientSession) SubscribeRoomEvents(roomid string, roomSessionId RoomSessionId) error {
	s.roomSessionIdLock.Lock()
	defer s.roomSessionIdLock.Unlock()

	if err := s.events.RegisterRoomListener(roomid, s.backend, s); err != nil {
		return err
	}

	if roomSessionId != "" {
		if err := s.hub.roomSessions.SetRoomSession(s, roomSessionId); err != nil {
			s.doUnsubscribeRoomEvents(true)
			return err
		}
	}
	log.Printf("Session %s joined room %s with room session id %s", s.PublicId(), roomid, roomSessionId)
	s.roomSessionId = roomSessionId
	return nil
}

func (s *ClientSession) LeaveCall() {
	s.mu.Lock()
	defer s.mu.Unlock()

	room := s.GetRoom()
	if room == nil {
		return
	}

	log.Printf("Session %s left call %s", s.PublicId(), room.Id())
	s.releaseMcuObjects()
}

func (s *ClientSession) LeaveRoom(notify bool) *Room {
	return s.LeaveRoomWithMessage(notify, nil)
}

func (s *ClientSession) LeaveRoomWithMessage(notify bool, message *ClientMessage) *Room {
	if prev := s.federation.Swap(nil); prev != nil {
		// Session was connected to a federation room.
		if err := prev.Leave(message); err != nil {
			log.Printf("Error leaving room for session %s on federation client %s: %s", s.PublicId(), prev.URL(), err)
			prev.Close()
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.doLeaveRoom(notify)
}

// +checklocks:s.mu
func (s *ClientSession) doLeaveRoom(notify bool) *Room {
	room := s.GetRoom()
	if room == nil {
		return nil
	}

	s.doUnsubscribeRoomEvents(notify)
	s.SetRoom(nil)
	s.releaseMcuObjects()
	room.RemoveSession(s)
	return room
}

func (s *ClientSession) UnsubscribeRoomEvents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.doUnsubscribeRoomEvents(true)
}

func (s *ClientSession) doUnsubscribeRoomEvents(notify bool) {
	room := s.GetRoom()
	if room != nil {
		s.events.UnregisterRoomListener(room.Id(), s.Backend(), s)
	}
	s.hub.roomSessions.DeleteRoomSession(s)

	s.roomSessionIdLock.Lock()
	defer s.roomSessionIdLock.Unlock()
	if notify && room != nil && s.roomSessionId != "" && !s.roomSessionId.IsFederated() {
		// Notify
		go func(sid RoomSessionId) {
			ctx := context.Background()
			request := NewBackendClientRoomRequest(room.Id(), s.userId, sid)
			request.Room.UpdateFromSession(s)
			request.Room.Action = "leave"
			var response api.StringMap
			if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendOcsUrl(), request, &response); err != nil {
				log.Printf("Could not notify about room session %s left room %s: %s", sid, room.Id(), err)
			} else {
				log.Printf("Removed room session %s: %+v", sid, response)
			}
		}(s.roomSessionId)
	}
	s.roomSessionId = ""
}

func (s *ClientSession) ClearClient(client HandlerClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearClientLocked(client)
}

// +checklocks:s.mu
func (s *ClientSession) clearClientLocked(client HandlerClient) {
	if s.client == nil {
		return
	} else if client != nil && s.client != client {
		log.Printf("Trying to clear other client in session %s", s.PublicId())
		return
	}

	prevClient := s.client
	s.client = nil
	prevClient.SetSession(nil)
}

func (s *ClientSession) GetClient() HandlerClient {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getClientUnlocked()
}

// +checklocks:s.mu
func (s *ClientSession) getClientUnlocked() HandlerClient {
	return s.client
}

func (s *ClientSession) SetClient(client HandlerClient) HandlerClient {
	if client == nil {
		panic("Use ClearClient to set the client to nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if client == s.client {
		// No change
		return nil
	}

	client.SetSession(s)
	prev := s.client
	if prev != nil {
		s.clearClientLocked(prev)
	}
	s.client = client
	return prev
}

// +checklocks:s.mu
func (s *ClientSession) sendOffer(client McuClient, sender PublicSessionId, streamType StreamType, offer api.StringMap) {
	offer_message := &AnswerOfferMessage{
		To:       s.PublicId(),
		From:     sender,
		Type:     "offer",
		RoomType: string(streamType),
		Payload:  offer,
		Sid:      client.Sid(),
	}
	offer_data, err := json.Marshal(offer_message)
	if err != nil {
		log.Println("Could not serialize offer", offer_message, err)
		return
	}
	response_message := &ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Sender: &MessageServerMessageSender{
				Type:      "session",
				SessionId: sender,
			},
			Data: offer_data,
		},
	}

	s.sendMessageUnlocked(response_message)
}

// +checklocks:s.mu
func (s *ClientSession) sendCandidate(client McuClient, sender PublicSessionId, streamType StreamType, candidate any) {
	candidate_message := &AnswerOfferMessage{
		To:       s.PublicId(),
		From:     sender,
		Type:     "candidate",
		RoomType: string(streamType),
		Payload: api.StringMap{
			"candidate": candidate,
		},
		Sid: client.Sid(),
	}
	candidate_data, err := json.Marshal(candidate_message)
	if err != nil {
		log.Println("Could not serialize candidate", candidate_message, err)
		return
	}
	response_message := &ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Sender: &MessageServerMessageSender{
				Type:      "session",
				SessionId: sender,
			},
			Data: candidate_data,
		},
	}

	s.sendMessageUnlocked(response_message)
}

// +checklocks:s.mu
func (s *ClientSession) sendMessageUnlocked(message *ServerMessage) bool {
	if c := s.getClientUnlocked(); c != nil {
		if c.SendMessage(message) {
			return true
		}
	}

	s.storePendingMessage(message)
	return true
}

func (s *ClientSession) SendError(e *Error) bool {
	message := &ServerMessage{
		Type:  "error",
		Error: e,
	}
	return s.SendMessage(message)
}

func (s *ClientSession) SendMessage(message *ServerMessage) bool {
	message = s.filterMessage(message)
	if message == nil {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sendMessageUnlocked(message)
}

func (s *ClientSession) SendMessages(messages []*ServerMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, message := range messages {
		s.sendMessageUnlocked(message)
	}
	return true
}

func (s *ClientSession) OnUpdateOffer(client McuClient, offer api.StringMap) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subscribers {
		if sub.Id() == client.Id() {
			s.sendOffer(client, sub.Publisher(), client.StreamType(), offer)
			return
		}
	}
}

func (s *ClientSession) OnIceCandidate(client McuClient, candidate any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subscribers {
		if sub.Id() == client.Id() {
			s.sendCandidate(client, sub.Publisher(), client.StreamType(), candidate)
			return
		}
	}

	for _, pub := range s.publishers {
		if pub.Id() == client.Id() {
			s.sendCandidate(client, s.PublicId(), client.StreamType(), candidate)
			return
		}
	}

	log.Printf("Session %s received candidate %+v for unknown client %s", s.PublicId(), candidate, client.Id())
}

func (s *ClientSession) OnIceCompleted(client McuClient) {
	// TODO(jojo): This causes a JavaScript error when creating a candidate from "null".
	// Figure out a better way to signal this.

	// An empty candidate signals the end of candidates.
	// s.OnIceCandidate(client, nil)
}

func (s *ClientSession) SubscriberSidUpdated(subscriber McuSubscriber) {
}

func (s *ClientSession) PublisherClosed(publisher McuPublisher) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, p := range s.publishers {
		if p == publisher {
			delete(s.publishers, id)
			break
		}
	}
}

func (s *ClientSession) SubscriberClosed(subscriber McuSubscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, sub := range s.subscribers {
		if sub == subscriber {
			delete(s.subscribers, id)
			break
		}
	}
}

type PermissionError struct {
	permission Permission
}

func (e *PermissionError) Permission() Permission {
	return e.permission
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission \"%s\" not found", e.permission)
}

// +checklocks:s.mu
func (s *ClientSession) isSdpAllowedToSendLocked(sdp *sdp.SessionDescription) (MediaType, error) {
	if sdp == nil {
		// Should have already been checked when data was validated.
		return 0, ErrNoSdp
	}

	var mediaTypes MediaType
	mayPublishMedia := s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_MEDIA)
	for _, md := range sdp.MediaDescriptions {
		switch md.MediaName.Media {
		case "audio":
			if !mayPublishMedia && !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_AUDIO) {
				return 0, &PermissionError{PERMISSION_MAY_PUBLISH_AUDIO}
			}

			mediaTypes |= MediaTypeAudio
		case "video":
			if !mayPublishMedia && !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_VIDEO) {
				return 0, &PermissionError{PERMISSION_MAY_PUBLISH_VIDEO}
			}

			mediaTypes |= MediaTypeVideo
		}
	}

	return mediaTypes, nil
}

func (s *ClientSession) IsAllowedToSend(data *MessageClientMessageData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if data != nil && data.RoomType == "screen" {
		if s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_SCREEN) {
			return nil
		}
		return &PermissionError{PERMISSION_MAY_PUBLISH_SCREEN}
	} else if s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_MEDIA) {
		// Client is allowed to publish any media (audio / video).
		return nil
	} else if data != nil && data.Type == "offer" {
		// Check what user is trying to publish and check permissions accordingly.
		if _, err := s.isSdpAllowedToSendLocked(data.offerSdp); err != nil {
			return err
		}

		return nil
	} else {
		// Candidate or unknown event, check if client is allowed to publish any media.
		if s.hasAnyPermissionLocked(PERMISSION_MAY_PUBLISH_AUDIO, PERMISSION_MAY_PUBLISH_VIDEO) {
			return nil
		}

		return fmt.Errorf("permission check failed")
	}
}

func (s *ClientSession) CheckOfferType(streamType StreamType, data *MessageClientMessageData) (MediaType, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.checkOfferTypeLocked(streamType, data)
}

// +checklocks:s.mu
func (s *ClientSession) checkOfferTypeLocked(streamType StreamType, data *MessageClientMessageData) (MediaType, error) {
	if streamType == StreamTypeScreen {
		if !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_SCREEN) {
			return 0, &PermissionError{PERMISSION_MAY_PUBLISH_SCREEN}
		}

		return MediaTypeScreen, nil
	} else if data != nil && data.Type == "offer" {
		mediaTypes, err := s.isSdpAllowedToSendLocked(data.offerSdp)
		if err != nil {
			return 0, err
		}

		return mediaTypes, nil
	}

	return 0, nil
}

func (s *ClientSession) GetOrCreatePublisher(ctx context.Context, mcu Mcu, streamType StreamType, data *MessageClientMessageData) (McuPublisher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mediaTypes, err := s.checkOfferTypeLocked(streamType, data)
	if err != nil {
		return nil, err
	}

	publisher, found := s.publishers[streamType]
	if !found {
		client := s.getClientUnlocked()
		s.mu.Unlock()
		defer s.mu.Lock()

		settings := NewPublisherSettings{
			Bitrate:    data.Bitrate,
			MediaTypes: mediaTypes,

			AudioCodec:  data.AudioCodec,
			VideoCodec:  data.VideoCodec,
			VP9Profile:  data.VP9Profile,
			H264Profile: data.H264Profile,
		}
		if backend := s.Backend(); backend != nil {
			var maxBitrate int
			if streamType == StreamTypeScreen {
				maxBitrate = backend.maxScreenBitrate
			} else {
				maxBitrate = backend.maxStreamBitrate
			}
			if settings.Bitrate <= 0 {
				settings.Bitrate = maxBitrate
			} else if maxBitrate > 0 && settings.Bitrate > maxBitrate {
				settings.Bitrate = maxBitrate
			}
		}
		var err error
		publisher, err = mcu.NewPublisher(ctx, s, s.PublicId(), data.Sid, streamType, settings, client)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.publishers == nil {
			s.publishers = make(map[StreamType]McuPublisher)
		}
		if prev, found := s.publishers[streamType]; found {
			// Another thread created the publisher while we were waiting.
			go func(pub McuPublisher) {
				closeCtx := context.Background()
				pub.Close(closeCtx)
			}(publisher)
			publisher = prev
		} else {
			s.publishers[streamType] = publisher
		}
		log.Printf("Publishing %s as %s for session %s", streamType, publisher.Id(), s.PublicId())
		s.publisherWaiters.Wakeup()
	} else {
		publisher.SetMedia(mediaTypes)
	}

	return publisher, nil
}

// +checklocks:s.mu
func (s *ClientSession) getPublisherLocked(streamType StreamType) McuPublisher {
	return s.publishers[streamType]
}

func (s *ClientSession) GetPublisher(streamType StreamType) McuPublisher {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getPublisherLocked(streamType)
}

func (s *ClientSession) GetOrWaitForPublisher(ctx context.Context, streamType StreamType) McuPublisher {
	s.mu.Lock()
	defer s.mu.Unlock()

	publisher := s.getPublisherLocked(streamType)
	if publisher != nil {
		return publisher
	}

	ch := make(chan struct{}, 1)
	id := s.publisherWaiters.Add(ch)
	defer s.publisherWaiters.Remove(id)

	for {
		s.mu.Unlock()
		select {
		case <-ch:
			s.mu.Lock()
			publisher := s.getPublisherLocked(streamType)
			if publisher != nil {
				return publisher
			}
		case <-ctx.Done():
			s.mu.Lock()
			return nil
		}
	}
}

func (s *ClientSession) GetOrCreateSubscriber(ctx context.Context, mcu Mcu, id PublicSessionId, streamType StreamType) (McuSubscriber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO(jojo): Add method to remove subscribers.

	subscriber, found := s.subscribers[getStreamId(id, streamType)]
	if !found {
		client := s.getClientUnlocked()
		s.mu.Unlock()
		var err error
		subscriber, err = mcu.NewSubscriber(ctx, s, id, streamType, client)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		if s.subscribers == nil {
			s.subscribers = make(map[StreamId]McuSubscriber)
		}
		if prev, found := s.subscribers[getStreamId(id, streamType)]; found {
			// Another thread created the subscriber while we were waiting.
			go func(sub McuSubscriber) {
				closeCtx := context.Background()
				sub.Close(closeCtx)
			}(subscriber)
			subscriber = prev
		} else {
			s.subscribers[getStreamId(id, streamType)] = subscriber
		}
		log.Printf("Subscribing %s from %s as %s in session %s", streamType, id, subscriber.Id(), s.PublicId())
	}

	return subscriber, nil
}

func (s *ClientSession) GetSubscriber(id PublicSessionId, streamType StreamType) McuSubscriber {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.subscribers[getStreamId(id, streamType)]
}

func (s *ClientSession) ProcessAsyncRoomMessage(message *AsyncMessage) {
	s.processAsyncMessage(message)
}

func (s *ClientSession) ProcessAsyncUserMessage(message *AsyncMessage) {
	s.processAsyncMessage(message)
}

func (s *ClientSession) ProcessAsyncSessionMessage(message *AsyncMessage) {
	s.processAsyncMessage(message)
}

func (s *ClientSession) processAsyncMessage(message *AsyncMessage) {
	switch message.Type {
	case "permissions":
		s.SetPermissions(message.Permissions)
		go func() {
			s.mu.Lock()
			defer s.mu.Unlock()

			if !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_MEDIA) {
				if publisher, found := s.publishers[StreamTypeVideo]; found {
					if (publisher.HasMedia(MediaTypeAudio) && !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_AUDIO)) ||
						(publisher.HasMedia(MediaTypeVideo) && !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_VIDEO)) {
						delete(s.publishers, StreamTypeVideo)
						log.Printf("Session %s is no longer allowed to publish media, closing publisher %s", s.PublicId(), publisher.Id())
						go func() {
							publisher.Close(context.Background())
						}()
						return
					}
				}
			}
			if !s.hasPermissionLocked(PERMISSION_MAY_PUBLISH_SCREEN) {
				if publisher, found := s.publishers[StreamTypeScreen]; found {
					delete(s.publishers, StreamTypeScreen)
					log.Printf("Session %s is no longer allowed to publish screen, closing publisher %s", s.PublicId(), publisher.Id())
					go func() {
						publisher.Close(context.Background())
					}()
					return
				}
			}
		}()
		return
	case "message":
		if message.Message.Type == "bye" && message.Message.Bye.Reason == "room_session_reconnected" {
			log.Printf("Closing session %s because same room session %s connected", s.PublicId(), s.RoomSessionId())
			s.LeaveRoom(false)
			defer s.closeAndWait(false)
		}
	case "sendoffer":
		// Process asynchronously to not block other messages received.
		go func() {
			ctx, cancel := context.WithTimeout(s.Context(), s.hub.mcuTimeout)
			defer cancel()

			mc, err := s.GetOrCreateSubscriber(ctx, s.hub.mcu, message.SendOffer.SessionId, StreamType(message.SendOffer.Data.RoomType))
			if err != nil {
				log.Printf("Could not create MCU subscriber for session %s to process sendoffer in %s: %s", message.SendOffer.SessionId, s.PublicId(), err)
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
					Type: "message",
					Message: &ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					log.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
				}
				return
			} else if mc == nil {
				log.Printf("No MCU subscriber found for session %s to process sendoffer in %s", message.SendOffer.SessionId, s.PublicId())
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
					Type: "message",
					Message: &ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					log.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
				}
				return
			}

			mc.SendMessage(s.Context(), nil, message.SendOffer.Data, func(err error, response api.StringMap) {
				if err != nil {
					log.Printf("Could not send MCU message %+v for session %s to %s: %s", message.SendOffer.Data, message.SendOffer.SessionId, s.PublicId(), err)
					if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
						Type: "message",
						Message: &ServerMessage{
							Id:    message.SendOffer.MessageId,
							Type:  "error",
							Error: NewError("processing_failed", "Processing of the message failed, please check server logs."),
						},
					}); err != nil {
						log.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
					}
					return
				} else if response == nil {
					// No response received
					return
				}

				s.hub.sendMcuMessageResponse(s, mc, &MessageClientMessage{
					Recipient: MessageClientMessageRecipient{
						SessionId: message.SendOffer.SessionId,
					},
				}, message.SendOffer.Data, response)
			})
		}()
		return
	}

	serverMessage := s.filterAsyncMessage(message)
	if serverMessage == nil {
		return
	}

	s.SendMessage(serverMessage)
}

// +checklocks:s.mu
func (s *ClientSession) storePendingMessage(message *ServerMessage) {
	if message.IsChatRefresh() {
		if s.hasPendingChat {
			// Only send a single "chat-refresh" message on resume.
			return
		}

		s.hasPendingChat = true
	}
	if !s.hasPendingParticipantsUpdate && message.IsParticipantsUpdate() {
		s.hasPendingParticipantsUpdate = true
	}
	s.pendingClientMessages = append(s.pendingClientMessages, message)
	if len(s.pendingClientMessages) >= warnPendingMessagesCount {
		log.Printf("Session %s has %d pending messages", s.PublicId(), len(s.pendingClientMessages))
	}
}

func filterDisplayNames(events []*EventServerMessageSessionEntry) []*EventServerMessageSessionEntry {
	result := make([]*EventServerMessageSessionEntry, 0, len(events))
	for _, event := range events {
		if len(event.User) == 0 {
			result = append(result, event)
			continue
		}

		var userdata api.StringMap
		if err := json.Unmarshal(event.User, &userdata); err != nil {
			result = append(result, event)
			continue
		}

		if _, found := userdata["displayname"]; !found {
			result = append(result, event)
			continue
		}

		delete(userdata, "displayname")
		if len(userdata) == 0 {
			// No more userdata, no need to serialize empty map.
			e := event.Clone()
			e.User = nil
			result = append(result, e)
			continue
		}

		data, err := json.Marshal(userdata)
		if err != nil {
			result = append(result, event)
			continue
		}

		e := event.Clone()
		e.User = data
		result = append(result, e)
	}
	return result
}

func (s *ClientSession) filterDuplicateJoin(entries []*EventServerMessageSessionEntry) []*EventServerMessageSessionEntry {
	s.seenJoinedLock.Lock()
	defer s.seenJoinedLock.Unlock()

	// Due to the asynchronous events, a session might received a "Joined" event
	// for the same (other) session twice, so filter these out on a per-session
	// level.
	result := make([]*EventServerMessageSessionEntry, 0, len(entries))
	for _, e := range entries {
		if s.seenJoinedEvents[e.SessionId] {
			log.Printf("Session %s got duplicate joined event for %s, ignoring", s.publicId, e.SessionId)
			continue
		}

		if s.seenJoinedEvents == nil {
			s.seenJoinedEvents = make(map[PublicSessionId]bool)
		}
		s.seenJoinedEvents[e.SessionId] = true
		result = append(result, e)
	}
	return result
}

func (s *ClientSession) filterMessage(message *ServerMessage) *ServerMessage {
	switch message.Type {
	case "event":
		switch message.Event.Target {
		case "participants":
			if message.Event.Type == "update" {
				m := message.Event.Update
				users := make(map[any]bool)
				for _, entry := range m.Users {
					users[entry["sessionId"]] = true
				}
				for _, entry := range m.Changed {
					if users[entry["sessionId"]] {
						continue
					}
					m.Users = append(m.Users, entry)
				}
				// TODO(jojo): Only send all users if current session id has
				// changed its "inCall" flag to true.
				m.Changed = nil
			}
		case "room":
			switch message.Event.Type {
			case "join":
				join := s.filterDuplicateJoin(message.Event.Join)
				if len(join) == 0 {
					return nil
				}
				copied := false
				if len(join) != len(message.Event.Join) {
					// Create unique copy of message for only this client.
					copied = true
					message = &ServerMessage{
						Id:   message.Id,
						Type: message.Type,
						Event: &EventServerMessage{
							Type:   message.Event.Type,
							Target: message.Event.Target,
							Join:   join,
						},
					}
				}

				if s.HasPermission(PERMISSION_HIDE_DISPLAYNAMES) {
					if copied {
						message.Event.Join = filterDisplayNames(message.Event.Join)
					} else {
						message = &ServerMessage{
							Id:   message.Id,
							Type: message.Type,
							Event: &EventServerMessage{
								Type:   message.Event.Type,
								Target: message.Event.Target,
								Join:   filterDisplayNames(message.Event.Join),
							},
						}
					}
				}
			case "leave":
				s.seenJoinedLock.Lock()
				defer s.seenJoinedLock.Unlock()

				for _, e := range message.Event.Leave {
					delete(s.seenJoinedEvents, e)
				}
			case "message":
				if message.Event.Message == nil || len(message.Event.Message.Data) == 0 {
					return message
				}

				data, err := message.Event.Message.GetData()
				if data == nil || err != nil {
					return message
				}

				if data.Type == "chat" && data.Chat != nil {
					update := false
					if data.Chat.Refresh && len(data.Chat.Comment) > 0 {
						// New-style chat event, check what the client supports.
						if s.HasFeature(ClientFeatureChatRelay) {
							data.Chat.Refresh = false
						} else {
							data.Chat.Comment = nil
						}
						update = true
					}

					if len(data.Chat.Comment) > 0 && s.HasPermission(PERMISSION_HIDE_DISPLAYNAMES) {
						var comment ChatComment
						if err := json.Unmarshal(data.Chat.Comment, &comment); err != nil {
							return message
						}

						if displayName, found := comment["actorDisplayName"]; found && displayName != "" {
							comment["actorDisplayName"] = ""
							var err error
							if data.Chat.Comment, err = json.Marshal(comment); err != nil {
								return message
							}
							update = true
						}
					}

					if update {
						if encoded, err := json.Marshal(data); err == nil {
							// Create unique copy of message for only this client.
							message = &ServerMessage{
								Id:   message.Id,
								Type: message.Type,
								Event: &EventServerMessage{
									Type:   message.Event.Type,
									Target: message.Event.Target,
									Message: &RoomEventMessage{
										RoomId: message.Event.Message.RoomId,
										Data:   encoded,
									},
								},
							}
						}
					}
				}
			}
		}
	case "message":
		if message.Message != nil && len(message.Message.Data) > 0 && s.HasPermission(PERMISSION_HIDE_DISPLAYNAMES) {
			var data MessageServerMessageData
			if err := json.Unmarshal(message.Message.Data, &data); err != nil {
				return message
			}

			if data.Type == "nickChanged" {
				return nil
			}
		}
	}

	return message
}

func (s *ClientSession) filterAsyncMessage(msg *AsyncMessage) *ServerMessage {
	switch msg.Type {
	case "message":
		if msg.Message == nil {
			log.Printf("Received asynchronous message without payload: %+v", msg)
			return nil
		}

		switch msg.Message.Type {
		case "message":
			if msg.Message.Message != nil {
				if sender := msg.Message.Message.Sender; sender != nil {
					if sender.SessionId == s.PublicId() {
						// Don't send message back to sender (can happen if sent to user or room)
						return nil
					}
					if sender.Type == RecipientTypeCall {
						if room := s.GetRoom(); room == nil || !room.IsSessionInCall(s) {
							// Session is not in call, so discard.
							return nil
						}
					}
				}
			}
		case "control":
			if msg.Message.Control != nil {
				if sender := msg.Message.Control.Sender; sender != nil {
					if sender.SessionId == s.PublicId() {
						// Don't send message back to sender (can happen if sent to user or room)
						return nil
					}
					if sender.Type == RecipientTypeCall {
						if room := s.GetRoom(); room == nil || !room.IsSessionInCall(s) {
							// Session is not in call, so discard.
							return nil
						}
					}
				}
			}
		case "event":
			if msg.Message.Event.Target == "room" {
				// Can happen mostly during tests where an older room async message
				// could be received by a subscriber that joined after it was sent.
				if joined := s.getRoomJoinTime(); joined.IsZero() || msg.SendTime.Before(joined) {
					log.Printf("Message %+v was sent on %s before room was joined on %s, ignoring", msg.Message, msg.SendTime, joined)
					return nil
				}
			}
		}

		return msg.Message
	default:
		log.Printf("Received async message with unsupported type %s: %+v", msg.Type, msg)
		return nil
	}
}

func (s *ClientSession) NotifySessionResumed(client HandlerClient) {
	s.mu.Lock()
	if len(s.pendingClientMessages) == 0 {
		s.mu.Unlock()
		if room := s.GetRoom(); room != nil {
			room.NotifySessionResumed(s)
		}
		return
	}

	messages := s.pendingClientMessages
	hasPendingParticipantsUpdate := s.hasPendingParticipantsUpdate
	s.pendingClientMessages = nil
	s.hasPendingChat = false
	s.hasPendingParticipantsUpdate = false
	s.mu.Unlock()

	log.Printf("Send %d pending messages to session %s", len(messages), s.PublicId())
	// Send through session to handle connection interruptions.
	s.SendMessages(messages)

	if !hasPendingParticipantsUpdate {
		// Only need to send initial participants list update if none was part of the pending messages.
		if room := s.GetRoom(); room != nil {
			room.NotifySessionResumed(s)
		}
	}
}

func (s *ClientSession) AddVirtualSession(session *VirtualSession) {
	s.mu.Lock()
	if s.virtualSessions == nil {
		s.virtualSessions = make(map[*VirtualSession]bool)
	}
	s.virtualSessions[session] = true
	s.mu.Unlock()
}

func (s *ClientSession) RemoveVirtualSession(session *VirtualSession) {
	s.mu.Lock()
	delete(s.virtualSessions, session)
	s.mu.Unlock()
}

func (s *ClientSession) GetVirtualSessions() []*VirtualSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*VirtualSession, 0, len(s.virtualSessions))
	for session := range s.virtualSessions {
		result = append(result, session)
	}
	return result
}

func (s *ClientSession) HandleResponse(id string, handler ResponseHandlerFunc) {
	s.responseHandlersLock.Lock()
	defer s.responseHandlersLock.Unlock()

	if s.responseHandlers == nil {
		s.responseHandlers = make(map[string]ResponseHandlerFunc)
	}

	s.responseHandlers[id] = handler
}

func (s *ClientSession) ClearResponseHandler(id string) {
	s.responseHandlersLock.Lock()
	defer s.responseHandlersLock.Unlock()

	delete(s.responseHandlers, id)
}

func (s *ClientSession) ProcessResponse(message *ClientMessage) bool {
	id := message.Id
	if id == "" {
		return false
	}

	s.responseHandlersLock.Lock()
	cb, found := s.responseHandlers[id]
	defer s.responseHandlersLock.Unlock()

	if !found {
		return false
	}

	return cb(message)
}
