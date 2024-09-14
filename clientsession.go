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
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/sdp/v3"
	"go.uber.org/zap"
)

var (
	// Warn if a session has 32 or more pending messages.
	warnPendingMessagesCount = 32

	PathToOcsSignalingBackend = "ocs/v2.php/apps/spreed/api/v1/signaling/backend"
)

const (
	FederatedRoomSessionIdPrefix = "federated|"
)

// ResponseHandlerFunc will return "true" has been fully processed.
type ResponseHandlerFunc func(message *ClientMessage) bool

type ClientSession struct {
	log       *zap.Logger
	hub       *Hub
	events    AsyncEvents
	privateId string
	publicId  string
	data      *SessionIdData
	ctx       context.Context
	closeFunc context.CancelFunc

	clientType string
	features   []string
	userId     string
	userData   json.RawMessage

	parseUserData func() (map[string]interface{}, error)

	inCall              Flags
	supportsPermissions bool
	permissions         map[Permission]bool

	backend          *Backend
	backendUrl       string
	parsedBackendUrl *url.URL

	mu sync.Mutex

	client       HandlerClient
	room         atomic.Pointer[Room]
	roomJoinTime atomic.Int64
	federation   atomic.Pointer[FederationClient]

	roomSessionIdLock sync.RWMutex
	roomSessionId     string

	publisherWaiters ChannelWaiters

	publishers  map[StreamType]McuPublisher
	subscribers map[string]McuSubscriber

	pendingClientMessages        []*ServerMessage
	hasPendingChat               bool
	hasPendingParticipantsUpdate bool

	virtualSessions map[*VirtualSession]bool

	seenJoinedLock   sync.Mutex
	seenJoinedEvents map[string]bool

	responseHandlersLock sync.Mutex
	responseHandlers     map[string]ResponseHandlerFunc
}

func NewClientSession(log *zap.Logger, hub *Hub, privateId string, publicId string, data *SessionIdData, backend *Backend, hello *HelloClientMessage, auth *BackendClientAuthResponse) (*ClientSession, error) {
	ctx, closeFunc := context.WithCancel(context.Background())
	s := &ClientSession{
		log: log.With(
			zap.String("sessionid", publicId),
		),
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
	if !strings.Contains(s.backendUrl, "/ocs/v2.php/") {
		backendUrl := s.backendUrl
		if !strings.HasSuffix(backendUrl, "/") {
			backendUrl += "/"
		}
		backendUrl += PathToOcsSignalingBackend
		u, err := url.Parse(backendUrl)
		if err != nil {
			return nil, err
		}

		if strings.Contains(u.Host, ":") && hasStandardPort(u) {
			u.Host = u.Hostname()
		}

		s.backendUrl = backendUrl
		s.parsedBackendUrl = u
	}

	if err := s.SubscribeEvents(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ClientSession) Context() context.Context {
	return s.ctx
}

func (s *ClientSession) PrivateId() string {
	return s.privateId
}

func (s *ClientSession) PublicId() string {
	return s.publicId
}

func (s *ClientSession) RoomSessionId() string {
	s.roomSessionIdLock.RLock()
	defer s.roomSessionIdLock.RUnlock()
	return s.roomSessionId
}

func (s *ClientSession) Data() *SessionIdData {
	return s.data
}

func (s *ClientSession) ClientType() string {
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
	for _, f := range s.features {
		if f == feature {
			return true
		}
	}
	return false
}

// HasPermission checks if the session has the passed permissions.
func (s *ClientSession) HasPermission(permission Permission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasPermissionLocked(permission)
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

func (s *ClientSession) hasAnyPermissionLocked(permission ...Permission) bool {
	if len(permission) == 0 {
		return false
	}

	for _, p := range permission {
		if s.hasPermissionLocked(p) {
			return true
		}
	}
	return false
}

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

func permissionsEqual(a, b map[Permission]bool) bool {
	if a == nil && b == nil {
		return true
	} else if a != nil && b == nil {
		return false
	} else if a == nil && b != nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}

	for k, v1 := range a {
		if v2, found := b[k]; !found || v1 != v2 {
			return false
		}
	}
	return true
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
	if s.supportsPermissions && permissionsEqual(s.permissions, p) {
		return
	}

	s.permissions = p
	s.supportsPermissions = true
	s.log.Info("Permissions of session changed",
		zap.Any("permissions", permissions),
	)
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

func (s *ClientSession) ParsedUserData() (map[string]interface{}, error) {
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
		go func(subscribers map[string]McuSubscriber) {
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

func (s *ClientSession) UpdateRoomSessionId(roomSessionId string) error {
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
			s.log.Info("Session updated room session id",
				zap.String("roomsessionid", roomSessionId),
				zap.String("roomid", room.Id()),
			)
		} else if client := s.GetFederationClient(); client != nil {
			s.log.Info("Session updated room session id",
				zap.String("roomsessionid", roomSessionId),
				zap.String("remoteroomid", client.RemoteRoomId()),
			)
		} else {
			s.log.Info("Session updated room session id",
				zap.String("roomsessionid", roomSessionId),
			)
		}
	} else {
		if room := s.GetRoom(); room != nil {
			s.log.Info("Session cleared room session id",
				zap.String("roomid", room.Id()),
			)
		} else if client := s.GetFederationClient(); client != nil {
			s.log.Info("Session cleared room session id",
				zap.String("remoteroomid", client.RemoteRoomId()),
			)
		} else {
			s.log.Info("Session cleared room session id")
		}
	}

	s.roomSessionId = roomSessionId
	return nil
}

func (s *ClientSession) SubscribeRoomEvents(roomid string, roomSessionId string) error {
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
	s.log.Info("Session joined room",
		zap.String("roomid", roomid),
		zap.String("roomsessionid", roomSessionId),
	)
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

	s.log.Info("Session left call",
		zap.String("roomid", room.Id()),
	)
	s.releaseMcuObjects()
}

func (s *ClientSession) LeaveRoom(notify bool) *Room {
	return s.LeaveRoomWithMessage(notify, nil)
}

func (s *ClientSession) LeaveRoomWithMessage(notify bool, message *ClientMessage) *Room {
	if prev := s.federation.Swap(nil); prev != nil {
		// Session was connected to a federation room.
		if err := prev.Leave(message); err != nil {
			s.log.Error("Error leaving room on federation client",
				zap.String("url", prev.URL()),
				zap.Error(err),
			)
			prev.Close()
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.doLeaveRoom(notify)
}

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
	if notify && room != nil && s.roomSessionId != "" && !strings.HasPrefix(s.roomSessionId, FederatedRoomSessionIdPrefix) {
		// Notify
		go func(sid string) {
			log := s.log.With(
				zap.String("roomsessionid", sid),
				zap.String("roomid", room.Id()),
			)
			ctx := context.Background()
			request := NewBackendClientRoomRequest(room.Id(), s.userId, sid)
			request.Room.UpdateFromSession(s)
			request.Room.Action = "leave"
			var response map[string]interface{}
			if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendUrl(), request, &response); err != nil {
				log.Error("Could not notify about room session left room",
					zap.Error(err),
				)
			} else {
				log.Info("Removed room session",
					zap.Any("response", response),
				)
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

func (s *ClientSession) clearClientLocked(client HandlerClient) {
	if s.client == nil {
		return
	} else if client != nil && s.client != client {
		s.log.Warn("Trying to clear other client in session")
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

func (s *ClientSession) sendOffer(client McuClient, sender string, streamType StreamType, offer map[string]interface{}) {
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
		s.log.Error("Could not serialize offer",
			zap.Any("offer", offer_message),
			zap.Error(err),
		)
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

func (s *ClientSession) sendCandidate(client McuClient, sender string, streamType StreamType, candidate interface{}) {
	candidate_message := &AnswerOfferMessage{
		To:       s.PublicId(),
		From:     sender,
		Type:     "candidate",
		RoomType: string(streamType),
		Payload: map[string]interface{}{
			"candidate": candidate,
		},
		Sid: client.Sid(),
	}
	candidate_data, err := json.Marshal(candidate_message)
	if err != nil {
		s.log.Error("Could not serialize candidate",
			zap.Any("candidate", candidate_message),
			zap.Error(err),
		)
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

func (s *ClientSession) OnUpdateOffer(client McuClient, offer map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subscribers {
		if sub.Id() == client.Id() {
			s.sendOffer(client, sub.Publisher(), client.StreamType(), offer)
			return
		}
	}
}

func (s *ClientSession) OnIceCandidate(client McuClient, candidate interface{}) {
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

	s.log.Warn("Session received candidate for unknown client",
		zap.Any("candidate", candidate),
		zap.String("clientid", client.Id()),
	)
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

		bitrate := data.Bitrate
		if backend := s.Backend(); backend != nil {
			var maxBitrate int
			if streamType == StreamTypeScreen {
				maxBitrate = backend.maxScreenBitrate
			} else {
				maxBitrate = backend.maxStreamBitrate
			}
			if bitrate <= 0 {
				bitrate = maxBitrate
			} else if maxBitrate > 0 && bitrate > maxBitrate {
				bitrate = maxBitrate
			}
		}
		var err error
		publisher, err = mcu.NewPublisher(ctx, s, s.PublicId(), data.Sid, streamType, bitrate, mediaTypes, client)
		if err != nil {
			return nil, err
		}
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
		s.log.Info("Publishing for session",
			zap.Any("streamtype", streamType),
			zap.String("publisherid", publisher.Id()),
		)
		s.publisherWaiters.Wakeup()
	} else {
		publisher.SetMedia(mediaTypes)
	}

	return publisher, nil
}

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

func (s *ClientSession) GetOrCreateSubscriber(ctx context.Context, mcu Mcu, id string, streamType StreamType) (McuSubscriber, error) {
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
			s.subscribers = make(map[string]McuSubscriber)
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
		s.log.Info("Subscribing in session",
			zap.Any("streamtype", streamType),
			zap.String("publisherid", id),
			zap.String("subscriberid", subscriber.Id()),
		)
	}

	return subscriber, nil
}

func (s *ClientSession) GetSubscriber(id string, streamType StreamType) McuSubscriber {
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
						s.log.Info("Session is no longer allowed to publish media, closing publisher",
							zap.String("publisherid", publisher.Id()),
						)
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
					s.log.Info("Session is no longer allowed to publish screen, closing publisher",
						zap.String("publisherid", publisher.Id()),
					)
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
			s.log.Info("Closing session because same room session connected",
				zap.String("roomsessionid", s.RoomSessionId()),
			)
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
				s.log.Error("Could not create MCU subscriber to process sendoffer",
					zap.String("publisherid", message.SendOffer.SessionId),
					zap.Error(err),
				)
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
					Type: "message",
					Message: &ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					s.log.Error("Error sending sendoffer error response",
						zap.String("recipient", message.SendOffer.SessionId),
						zap.Error(err),
					)
				}
				return
			} else if mc == nil {
				s.log.Warn("No MCU subscriber found for session to process sendoffer",
					zap.String("publisher", message.SendOffer.SessionId),
				)
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
					Type: "message",
					Message: &ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					s.log.Error("Error sending sendoffer error response",
						zap.String("recipient", message.SendOffer.SessionId),
						zap.Error(err),
					)
				}
				return
			}

			mc.SendMessage(s.Context(), nil, message.SendOffer.Data, func(err error, response map[string]interface{}) {
				if err != nil {
					s.log.Error("Could not send MCU message for session",
						zap.Any("message", message.SendOffer.Data),
						zap.String("recipient", message.SendOffer.SessionId),
						zap.Error(err),
					)
					if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &AsyncMessage{
						Type: "message",
						Message: &ServerMessage{
							Id:    message.SendOffer.MessageId,
							Type:  "error",
							Error: NewError("processing_failed", "Processing of the message failed, please check server logs."),
						},
					}); err != nil {
						s.log.Error("Error sending sendoffer error response",
							zap.String("recipient", message.SendOffer.SessionId),
							zap.Error(err),
						)
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
		s.log.Warn("Session has pending messages",
			zap.Int("count", len(s.pendingClientMessages)),
		)
	}
}

func filterDisplayNames(events []*EventServerMessageSessionEntry) []*EventServerMessageSessionEntry {
	result := make([]*EventServerMessageSessionEntry, 0, len(events))
	for _, event := range events {
		if len(event.User) == 0 {
			result = append(result, event)
			continue
		}

		var userdata map[string]interface{}
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
			s.log.Debug("Session got duplicate joined event, ignoring",
				zap.String("other", e.SessionId),
			)
			continue
		}

		if s.seenJoinedEvents == nil {
			s.seenJoinedEvents = make(map[string]bool)
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
				users := make(map[string]bool)
				for _, entry := range m.Users {
					users[entry["sessionId"].(string)] = true
				}
				for _, entry := range m.Changed {
					if users[entry["sessionId"].(string)] {
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
				if message.Event.Message == nil || len(message.Event.Message.Data) == 0 || !s.HasPermission(PERMISSION_HIDE_DISPLAYNAMES) {
					return message
				}

				var data RoomEventMessageData
				if err := json.Unmarshal(message.Event.Message.Data, &data); err != nil {
					return message
				}

				if data.Type == "chat" && data.Chat != nil && data.Chat.Comment != nil {
					if displayName, found := (*data.Chat.Comment)["actorDisplayName"]; found && displayName != "" {
						(*data.Chat.Comment)["actorDisplayName"] = ""
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
			s.log.Warn("Received asynchronous message without payload",
				zap.Any("message", msg),
			)
			return nil
		}

		switch msg.Message.Type {
		case "message":
			if msg.Message.Message != nil &&
				msg.Message.Message.Sender != nil &&
				msg.Message.Message.Sender.SessionId == s.PublicId() {
				// Don't send message back to sender (can happen if sent to user or room)
				return nil
			}
		case "control":
			if msg.Message.Control != nil &&
				msg.Message.Control.Sender != nil &&
				msg.Message.Control.Sender.SessionId == s.PublicId() {
				// Don't send message back to sender (can happen if sent to user or room)
				return nil
			}
		case "event":
			if msg.Message.Event.Target == "room" {
				// Can happen mostly during tests where an older room async message
				// could be received by a subscriber that joined after it was sent.
				if joined := s.getRoomJoinTime(); joined.IsZero() || msg.SendTime.Before(joined) {
					s.log.Debug("Message was sent before room was joined, ignoring",
						zap.Any("message", msg.Message),
						zap.Time("sent", msg.SendTime),
						zap.Time("joined", joined),
					)
					return nil
				}
			}
		}

		return msg.Message
	default:
		s.log.Warn("Received async message with unsupported type",
			zap.String("type", msg.Type),
			zap.Any("message", msg),
		)
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

	s.log.Debug("Send pending messages to session",
		zap.Int("count", len(messages)),
	)
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
