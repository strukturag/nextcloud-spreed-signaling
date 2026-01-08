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
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/sdp/v3"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	"github.com/strukturag/nextcloud-spreed-signaling/session"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

var (
	// Warn if a session has 32 or more pending messages.
	warnPendingMessagesCount = 32

	// The "/api/v1/signaling/" URL will be changed to use "v3" as the "signaling-v3"
	// feature is returned by the capabilities endpoint.
	PathToOcsSignalingBackend = "ocs/v2.php/apps/spreed/api/v1/signaling/backend"
)

// ResponseHandlerFunc will return "true" has been fully processed.
type ResponseHandlerFunc func(message *api.ClientMessage) bool

type ClientSession struct {
	logger    log.Logger
	hub       *Hub
	events    events.AsyncEvents
	privateId api.PrivateSessionId
	publicId  api.PublicSessionId
	data      *session.SessionIdData
	ctx       context.Context
	closeFunc context.CancelFunc

	clientType api.ClientType
	features   []string
	userId     string
	userData   json.RawMessage

	parseUserData func() (api.StringMap, error)

	inCall internal.Flags
	// +checklocks:mu
	supportsPermissions bool
	// +checklocks:mu
	permissions map[api.Permission]bool

	backend          *talk.Backend
	backendUrl       string
	parsedBackendUrl *url.URL

	mu      sync.Mutex
	asyncCh events.AsyncChannel

	// +checklocks:mu
	client       ClientWithSession
	room         atomic.Pointer[Room]
	roomJoinTime atomic.Int64
	federation   atomic.Pointer[FederationClient]

	roomSessionIdLock sync.RWMutex
	// +checklocks:roomSessionIdLock
	roomSessionId api.RoomSessionId

	publisherWaiters async.ChannelWaiters // +checklocksignore

	// +checklocks:mu
	publishers map[sfu.StreamType]sfu.Publisher
	// +checklocks:mu
	subscribers map[sfu.StreamId]sfu.Subscriber

	// +checklocks:mu
	pendingClientMessages []*api.ServerMessage
	// +checklocks:mu
	hasPendingChat bool
	// +checklocks:mu
	hasPendingParticipantsUpdate bool

	// +checklocks:mu
	virtualSessions map[*VirtualSession]bool

	filterDuplicateLock sync.Mutex
	// +checklocks:filterDuplicateLock
	seenJoinedEvents map[api.PublicSessionId]bool
	// +checklocks:filterDuplicateLock
	seenFlags map[api.PublicSessionId]uint32

	responseHandlersLock sync.Mutex
	// +checklocks:responseHandlersLock
	responseHandlers map[string]ResponseHandlerFunc
}

func NewClientSession(hub *Hub, privateId api.PrivateSessionId, publicId api.PublicSessionId, data *session.SessionIdData, backend *talk.Backend, hello *api.HelloClientMessage, auth *talk.BackendClientAuthResponse) (*ClientSession, error) {
	ctx := log.NewLoggerContext(context.Background(), hub.logger)
	ctx, closeFunc := context.WithCancel(ctx)
	s := &ClientSession{
		logger:    hub.logger,
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
		asyncCh: make(events.AsyncChannel, events.DefaultAsyncChannelSize),
	}
	if s.clientType == api.HelloClientTypeInternal {
		s.backendUrl = hello.Auth.InternalParams.Backend
		s.parsedBackendUrl = hello.Auth.InternalParams.ParsedBackend
		if !s.HasFeature(api.ClientFeatureInternalInCall) {
			s.SetInCall(FlagInCall | FlagWithAudio)
		}
	} else {
		s.backendUrl = hello.Auth.Url
		s.parsedBackendUrl = hello.Auth.ParsedUrl
	}

	if err := s.SubscribeEvents(); err != nil {
		return nil, err
	}
	go s.run()
	return s, nil
}

func (s *ClientSession) Context() context.Context {
	return s.ctx
}

func (s *ClientSession) PrivateId() api.PrivateSessionId {
	return s.privateId
}

func (s *ClientSession) PublicId() api.PublicSessionId {
	return s.publicId
}

func (s *ClientSession) RoomSessionId() api.RoomSessionId {
	s.roomSessionIdLock.RLock()
	defer s.roomSessionIdLock.RUnlock()
	return s.roomSessionId
}

func (s *ClientSession) Data() *session.SessionIdData {
	return s.data
}

func (s *ClientSession) ClientType() api.ClientType {
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
func (s *ClientSession) HasPermission(permission api.Permission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasPermissionLocked(permission)
}

func (s *ClientSession) GetPermissions() []api.Permission {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]api.Permission, len(s.permissions))
	for p, ok := range s.permissions {
		if ok {
			result = append(result, p)
		}
	}
	return result
}

// HasAnyPermission checks if the session has one of the passed permissions.
func (s *ClientSession) HasAnyPermission(permission ...api.Permission) bool {
	if len(permission) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasAnyPermissionLocked(permission...)
}

// +checklocks:s.mu
func (s *ClientSession) hasAnyPermissionLocked(permission ...api.Permission) bool {
	if len(permission) == 0 {
		return false
	}

	return slices.ContainsFunc(permission, s.hasPermissionLocked)
}

// +checklocks:s.mu
func (s *ClientSession) hasPermissionLocked(permission api.Permission) bool {
	if !s.supportsPermissions {
		// Old-style session that doesn't receive permissions from Nextcloud.
		if result, found := api.DefaultPermissionOverrides[permission]; found {
			return result
		}
		return true
	}

	if val, found := s.permissions[permission]; found {
		return val
	}
	return false
}

func (s *ClientSession) SetPermissions(permissions []api.Permission) {
	var p map[api.Permission]bool
	for _, permission := range permissions {
		if p == nil {
			p = make(map[api.Permission]bool)
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
	s.logger.Printf("Permissions of session %s changed: %s", s.PublicId(), permissions)
}

func (s *ClientSession) Backend() *talk.Backend {
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

func (s *ClientSession) SetRoom(room *Room, joinTime time.Time) {
	s.room.Store(room)
	s.onRoomSet(room != nil, joinTime)
}

func (s *ClientSession) onRoomSet(hasRoom bool, joinTime time.Time) {
	if hasRoom {
		s.roomJoinTime.Store(joinTime.UnixNano())
	} else {
		s.roomJoinTime.Store(0)
	}

	s.filterDuplicateLock.Lock()
	defer s.filterDuplicateLock.Unlock()
	s.seenJoinedEvents = nil
	s.seenFlags = nil
}

func (s *ClientSession) IsInRoom(id string) bool {
	room := s.GetRoom()
	return room != nil && room.Id() == id
}

func (s *ClientSession) GetFederationClient() *FederationClient {
	return s.federation.Load()
}

func (s *ClientSession) SetFederationClient(federation *FederationClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.doLeaveRoom(true)
	s.onRoomSet(federation != nil, time.Now())

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
		go func(publishers map[sfu.StreamType]sfu.Publisher) {
			ctx := context.Background()
			for _, publisher := range publishers {
				publisher.Close(ctx)
			}
		}(s.publishers)
		s.publishers = nil
	}
	if len(s.subscribers) > 0 {
		go func(subscribers map[sfu.StreamId]sfu.Subscriber) {
			ctx := context.Background()
			for _, subscriber := range subscribers {
				subscriber.Close(ctx)
			}
		}(s.subscribers)
		s.subscribers = nil
	}
}

func (s *ClientSession) AsyncChannel() events.AsyncChannel {
	return s.asyncCh
}

func (s *ClientSession) run() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.asyncCh:
			s.processAsyncNatsMessage(msg)
			for count := len(s.asyncCh); count > 0; count-- {
				s.processAsyncNatsMessage(<-s.asyncCh)
			}
		}
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
		if err := s.events.UnregisterUserListener(s.userId, s.backend, s); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
			s.logger.Printf("Error unsubscribing user listener for %s in session %s: %s", s.userId, s.publicId, err)
		}
	}
	if err := s.events.UnregisterSessionListener(s.publicId, s.backend, s); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		s.logger.Printf("Error unsubscribing listener in session %s: %s", s.publicId, err)
	}
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

func (s *ClientSession) UpdateRoomSessionId(roomSessionId api.RoomSessionId) error {
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
			s.logger.Printf("Session %s updated room session id to %s in room %s", s.PublicId(), roomSessionId, room.Id())
		} else if client := s.GetFederationClient(); client != nil {
			s.logger.Printf("Session %s updated room session id to %s in federated room %s", s.PublicId(), roomSessionId, client.RemoteRoomId())
		} else {
			s.logger.Printf("Session %s updated room session id to %s in unknown room", s.PublicId(), roomSessionId)
		}
	} else {
		if room := s.GetRoom(); room != nil {
			s.logger.Printf("Session %s cleared room session id in room %s", s.PublicId(), room.Id())
		} else if client := s.GetFederationClient(); client != nil {
			s.logger.Printf("Session %s cleared room session id in federated room %s", s.PublicId(), client.RemoteRoomId())
		} else {
			s.logger.Printf("Session %s cleared room session id in unknown room", s.PublicId())
		}
	}

	s.roomSessionId = roomSessionId
	return nil
}

func (s *ClientSession) SubscribeRoomEvents(roomid string, roomSessionId api.RoomSessionId) error {
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
	s.logger.Printf("Session %s joined room %s with room session id %s", s.PublicId(), roomid, roomSessionId)
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

	s.logger.Printf("Session %s left call %s", s.PublicId(), room.Id())
	s.releaseMcuObjects()
}

func (s *ClientSession) LeaveRoom(notify bool) *Room {
	return s.LeaveRoomWithMessage(notify, nil)
}

func (s *ClientSession) LeaveRoomWithMessage(notify bool, message *api.ClientMessage) *Room {
	if prev := s.federation.Swap(nil); prev != nil {
		// Session was connected to a federation room.
		if err := prev.Leave(message); err != nil {
			s.logger.Printf("Error leaving room for session %s on federation client %s: %s", s.PublicId(), prev.URL(), err)
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
	s.SetRoom(nil, time.Time{})
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
		if err := s.events.UnregisterRoomListener(room.Id(), s.Backend(), s); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
			s.logger.Printf("Error unsubscribing room listener for %s in session %s: %s", room.Id(), s.publicId, err)
		}
	}
	s.hub.roomSessions.DeleteRoomSession(s)

	s.roomSessionIdLock.Lock()
	defer s.roomSessionIdLock.Unlock()
	if notify && room != nil && s.roomSessionId != "" && !s.roomSessionId.IsFederated() {
		// Notify
		go func(sid api.RoomSessionId) {
			ctx := log.NewLoggerContext(context.Background(), s.logger)
			request := talk.NewBackendClientRoomRequest(room.Id(), s.userId, sid)
			request.Room.UpdateFromSession(s)
			request.Room.Action = "leave"
			var response api.StringMap
			if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendOcsUrl(), request, &response); err != nil {
				s.logger.Printf("Could not notify about room session %s left room %s: %s", sid, room.Id(), err)
			} else {
				s.logger.Printf("Removed room session %s: %+v", sid, response)
			}
		}(s.roomSessionId)
	}
	s.roomSessionId = ""
}

func (s *ClientSession) ClearClient(client ClientWithSession) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearClientLocked(client)
}

// +checklocks:s.mu
func (s *ClientSession) clearClientLocked(client ClientWithSession) {
	if s.client == nil {
		return
	} else if client != nil && s.client != client {
		s.logger.Printf("Trying to clear other client in session %s", s.PublicId())
		return
	}

	prevClient := s.client
	s.client = nil
	prevClient.SetSession(nil)
}

func (s *ClientSession) GetClient() ClientWithSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getClientUnlocked()
}

// +checklocks:s.mu
func (s *ClientSession) getClientUnlocked() ClientWithSession {
	return s.client
}

func (s *ClientSession) SetClient(client ClientWithSession) ClientWithSession {
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
func (s *ClientSession) sendOffer(client sfu.Client, sender api.PublicSessionId, streamType sfu.StreamType, offer api.StringMap) {
	offer_message := &api.AnswerOfferMessage{
		To:       s.PublicId(),
		From:     sender,
		Type:     "offer",
		RoomType: string(streamType),
		Payload:  offer,
		Sid:      client.Sid(),
	}
	offer_data, err := json.Marshal(offer_message)
	if err != nil {
		s.logger.Println("Could not serialize offer", offer_message, err)
		return
	}
	response_message := &api.ServerMessage{
		Type: "message",
		Message: &api.MessageServerMessage{
			Sender: &api.MessageServerMessageSender{
				Type:      "session",
				SessionId: sender,
			},
			Data: offer_data,
		},
	}

	s.sendMessageUnlocked(response_message)
}

// +checklocks:s.mu
func (s *ClientSession) sendCandidate(client sfu.Client, sender api.PublicSessionId, streamType sfu.StreamType, candidate any) {
	candidate_message := &api.AnswerOfferMessage{
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
		s.logger.Println("Could not serialize candidate", candidate_message, err)
		return
	}
	response_message := &api.ServerMessage{
		Type: "message",
		Message: &api.MessageServerMessage{
			Sender: &api.MessageServerMessageSender{
				Type:      "session",
				SessionId: sender,
			},
			Data: candidate_data,
		},
	}

	s.sendMessageUnlocked(response_message)
}

// +checklocks:s.mu
func (s *ClientSession) sendMessageUnlocked(message *api.ServerMessage) bool {
	if c := s.getClientUnlocked(); c != nil {
		if c.SendMessage(message) {
			return true
		}
	}

	s.storePendingMessage(message)
	return true
}

func (s *ClientSession) SendError(e *api.Error) bool {
	message := &api.ServerMessage{
		Type:  "error",
		Error: e,
	}
	return s.SendMessage(message)
}

func (s *ClientSession) SendMessage(message *api.ServerMessage) bool {
	message = s.filterMessage(message)
	if message == nil {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sendMessageUnlocked(message)
}

func (s *ClientSession) SendMessages(messages []*api.ServerMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, message := range messages {
		s.sendMessageUnlocked(message)
	}
	return true
}

func (s *ClientSession) OnUpdateOffer(client sfu.Client, offer api.StringMap) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subscribers {
		if sub.Id() == client.Id() {
			s.sendOffer(client, sub.Publisher(), client.StreamType(), offer)
			return
		}
	}
}

func (s *ClientSession) OnIceCandidate(client sfu.Client, candidate any) {
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

	s.logger.Printf("Session %s received candidate %+v for unknown client %s", s.PublicId(), candidate, client.Id())
}

func (s *ClientSession) OnIceCompleted(client sfu.Client) {
	// TODO(jojo): This causes a JavaScript error when creating a candidate from "null".
	// Figure out a better way to signal this.

	// An empty candidate signals the end of candidates.
	// s.OnIceCandidate(client, nil)
}

func (s *ClientSession) SubscriberSidUpdated(subscriber sfu.Subscriber) {
}

func (s *ClientSession) PublisherClosed(publisher sfu.Publisher) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, p := range s.publishers {
		if p == publisher {
			delete(s.publishers, id)
			break
		}
	}
}

func (s *ClientSession) SubscriberClosed(subscriber sfu.Subscriber) {
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
	permission api.Permission
}

func (e *PermissionError) Permission() api.Permission {
	return e.permission
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission \"%s\" not found", e.permission)
}

// +checklocks:s.mu
func (s *ClientSession) isSdpAllowedToSendLocked(sdp *sdp.SessionDescription) (sfu.MediaType, error) {
	if sdp == nil {
		// Should have already been checked when data was validated.
		return 0, api.ErrNoSdp
	}

	var mediaTypes sfu.MediaType
	mayPublishMedia := s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_MEDIA)
	for _, md := range sdp.MediaDescriptions {
		switch md.MediaName.Media {
		case "audio":
			if !mayPublishMedia && !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_AUDIO) {
				return 0, &PermissionError{api.PERMISSION_MAY_PUBLISH_AUDIO}
			}

			mediaTypes |= sfu.MediaTypeAudio
		case "video":
			if !mayPublishMedia && !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_VIDEO) {
				return 0, &PermissionError{api.PERMISSION_MAY_PUBLISH_VIDEO}
			}

			mediaTypes |= sfu.MediaTypeVideo
		}
	}

	return mediaTypes, nil
}

func (s *ClientSession) IsAllowedToSend(data *api.MessageClientMessageData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case data != nil && data.RoomType == "screen":
		if s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_SCREEN) {
			return nil
		}
		return &PermissionError{api.PERMISSION_MAY_PUBLISH_SCREEN}
	case s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_MEDIA):
		// Client is allowed to publish any media (audio / video).
		return nil
	case data != nil && data.Type == "offer":
		// Check what user is trying to publish and check permissions accordingly.
		if _, err := s.isSdpAllowedToSendLocked(data.OfferSdp); err != nil {
			return err
		}

		return nil
	default:
		// Candidate or unknown event, check if client is allowed to publish any media.
		if s.hasAnyPermissionLocked(api.PERMISSION_MAY_PUBLISH_AUDIO, api.PERMISSION_MAY_PUBLISH_VIDEO) {
			return nil
		}

		return errors.New("permission check failed")
	}
}

func (s *ClientSession) CheckOfferType(streamType sfu.StreamType, data *api.MessageClientMessageData) (sfu.MediaType, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.checkOfferTypeLocked(streamType, data)
}

// +checklocks:s.mu
func (s *ClientSession) checkOfferTypeLocked(streamType sfu.StreamType, data *api.MessageClientMessageData) (sfu.MediaType, error) {
	if streamType == sfu.StreamTypeScreen {
		if !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_SCREEN) {
			return 0, &PermissionError{api.PERMISSION_MAY_PUBLISH_SCREEN}
		}

		return sfu.MediaTypeScreen, nil
	} else if data != nil && data.Type == "offer" {
		mediaTypes, err := s.isSdpAllowedToSendLocked(data.OfferSdp)
		if err != nil {
			return 0, err
		}

		return mediaTypes, nil
	}

	return 0, nil
}

func (s *ClientSession) GetOrCreatePublisher(ctx context.Context, mcu sfu.SFU, streamType sfu.StreamType, data *api.MessageClientMessageData) (sfu.Publisher, error) {
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

		settings := sfu.NewPublisherSettings{
			Bitrate:    data.Bitrate,
			MediaTypes: mediaTypes,

			AudioCodec:  data.AudioCodec,
			VideoCodec:  data.VideoCodec,
			VP9Profile:  data.VP9Profile,
			H264Profile: data.H264Profile,
		}
		if backend := s.Backend(); backend != nil {
			var maxBitrate api.Bandwidth
			if streamType == sfu.StreamTypeScreen {
				maxBitrate = backend.MaxScreenBitrate()
			} else {
				maxBitrate = backend.MaxStreamBitrate()
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
			s.publishers = make(map[sfu.StreamType]sfu.Publisher)
		}
		if prev, found := s.publishers[streamType]; found {
			// Another thread created the publisher while we were waiting.
			go func(pub sfu.Publisher) {
				closeCtx := context.Background()
				pub.Close(closeCtx)
			}(publisher)
			publisher = prev
		} else {
			s.publishers[streamType] = publisher
		}
		s.logger.Printf("Publishing %s as %s for session %s", streamType, publisher.Id(), s.PublicId())
		s.publisherWaiters.Wakeup()
	} else {
		publisher.SetMedia(mediaTypes)
	}

	return publisher, nil
}

// +checklocks:s.mu
func (s *ClientSession) getPublisherLocked(streamType sfu.StreamType) sfu.Publisher {
	return s.publishers[streamType]
}

func (s *ClientSession) GetPublisher(streamType sfu.StreamType) sfu.Publisher {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getPublisherLocked(streamType)
}

func (s *ClientSession) GetOrWaitForPublisher(ctx context.Context, streamType sfu.StreamType) sfu.Publisher {
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

func (s *ClientSession) GetOrCreateSubscriber(ctx context.Context, mcu sfu.SFU, id api.PublicSessionId, streamType sfu.StreamType) (sfu.Subscriber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO(jojo): Add method to remove subscribers.

	subscriber, found := s.subscribers[sfu.GetStreamId(id, streamType)]
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
			s.subscribers = make(map[sfu.StreamId]sfu.Subscriber)
		}
		if prev, found := s.subscribers[sfu.GetStreamId(id, streamType)]; found {
			// Another thread created the subscriber while we were waiting.
			go func(sub sfu.Subscriber) {
				closeCtx := context.Background()
				sub.Close(closeCtx)
			}(subscriber)
			subscriber = prev
		} else {
			s.subscribers[sfu.GetStreamId(id, streamType)] = subscriber
		}
		s.logger.Printf("Subscribing %s from %s as %s in session %s", streamType, id, subscriber.Id(), s.PublicId())
	}

	return subscriber, nil
}

func (s *ClientSession) GetSubscriber(id api.PublicSessionId, streamType sfu.StreamType) sfu.Subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.subscribers[sfu.GetStreamId(id, streamType)]
}

func (s *ClientSession) processAsyncNatsMessage(msg *nats.Msg) {
	var message events.AsyncMessage
	if err := nats.Decode(msg, &message); err != nil {
		s.logger.Printf("Could not decode NATS message %+v: %s", msg, err)
		return
	}

	s.processAsyncMessage(&message)
}

func (s *ClientSession) processAsyncMessage(message *events.AsyncMessage) {
	switch message.Type {
	case "permissions":
		s.SetPermissions(message.Permissions)
		go func() {
			s.mu.Lock()
			defer s.mu.Unlock()

			if !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_MEDIA) {
				if publisher, found := s.publishers[sfu.StreamTypeVideo]; found {
					if (publisher.HasMedia(sfu.MediaTypeAudio) && !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_AUDIO)) ||
						(publisher.HasMedia(sfu.MediaTypeVideo) && !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_VIDEO)) {
						delete(s.publishers, sfu.StreamTypeVideo)
						s.logger.Printf("Session %s is no longer allowed to publish media, closing publisher %s", s.PublicId(), publisher.Id())
						go func() {
							publisher.Close(context.Background())
						}()
						return
					}
				}
			}
			if !s.hasPermissionLocked(api.PERMISSION_MAY_PUBLISH_SCREEN) {
				if publisher, found := s.publishers[sfu.StreamTypeScreen]; found {
					delete(s.publishers, sfu.StreamTypeScreen)
					s.logger.Printf("Session %s is no longer allowed to publish screen, closing publisher %s", s.PublicId(), publisher.Id())
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
			s.logger.Printf("Closing session %s because same room session %s connected", s.PublicId(), s.RoomSessionId())
			s.LeaveRoom(false)
			defer s.closeAndWait(false)
		}
	case "sendoffer":
		// Process asynchronously to not block other messages received.
		go func() {
			ctx, cancel := context.WithTimeout(s.Context(), s.hub.mcuTimeout)
			defer cancel()

			mc, err := s.GetOrCreateSubscriber(ctx, s.hub.mcu, message.SendOffer.SessionId, sfu.StreamType(message.SendOffer.Data.RoomType))
			if err != nil {
				s.logger.Printf("Could not create MCU subscriber for session %s to process sendoffer in %s: %s", message.SendOffer.SessionId, s.PublicId(), err)
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &events.AsyncMessage{
					Type: "message",
					Message: &api.ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: api.NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					s.logger.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
				}
				return
			} else if mc == nil {
				s.logger.Printf("No MCU subscriber found for session %s to process sendoffer in %s", message.SendOffer.SessionId, s.PublicId())
				if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &events.AsyncMessage{
					Type: "message",
					Message: &api.ServerMessage{
						Id:    message.SendOffer.MessageId,
						Type:  "error",
						Error: api.NewError("client_not_found", "No MCU client found to send message to."),
					},
				}); err != nil {
					s.logger.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
				}
				return
			}

			mc.SendMessage(s.Context(), nil, message.SendOffer.Data, func(err error, response api.StringMap) {
				if err != nil {
					s.logger.Printf("Could not send MCU message %+v for session %s to %s: %s", message.SendOffer.Data, message.SendOffer.SessionId, s.PublicId(), err)
					if err := s.events.PublishSessionMessage(message.SendOffer.SessionId, s.backend, &events.AsyncMessage{
						Type: "message",
						Message: &api.ServerMessage{
							Id:    message.SendOffer.MessageId,
							Type:  "error",
							Error: api.NewError("processing_failed", "Processing of the message failed, please check server logs."),
						},
					}); err != nil {
						s.logger.Printf("Error sending sendoffer error response to %s: %s", message.SendOffer.SessionId, err)
					}
					return
				} else if response == nil {
					// No response received
					return
				}

				s.hub.sendMcuMessageResponse(s, mc, &api.MessageClientMessage{
					Recipient: api.MessageClientMessageRecipient{
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
func (s *ClientSession) storePendingMessage(message *api.ServerMessage) {
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
		s.logger.Printf("Session %s has %d pending messages", s.PublicId(), len(s.pendingClientMessages))
	}
}

func filterDisplayNames(events []api.EventServerMessageSessionEntry) []api.EventServerMessageSessionEntry {
	result := make([]api.EventServerMessageSessionEntry, 0, len(events))
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

// +checklocks:s.filterDuplicateLock
func (s *ClientSession) filterUnknownLeave(entries []api.PublicSessionId) []api.PublicSessionId {
	idx := slices.IndexFunc(entries, func(e api.PublicSessionId) bool {
		return !s.seenJoinedEvents[e] // +checklocksignore
	})
	if idx == -1 {
		return entries
	} else if idx+1 == len(entries) {
		// Simple case: all entries filtered.
		s.logger.Printf("Session %s got unknown leave events for %+v", s.publicId, entries)
		return nil
	}

	// Filter remaining entries.
	filtered := []api.PublicSessionId{
		entries[idx],
	}
	result := append([]api.PublicSessionId{}, entries[:idx]...)
	for _, e := range entries[idx+1:] {
		if s.seenJoinedEvents[e] {
			result = append(result, e)
		} else {
			filtered = append(filtered, e)
		}
	}
	s.logger.Printf("Session %s got unknown leave events for %+v", s.publicId, filtered)
	return result
}

func (s *ClientSession) filterDuplicateJoin(entries []api.EventServerMessageSessionEntry) []api.EventServerMessageSessionEntry {
	s.filterDuplicateLock.Lock()
	defer s.filterDuplicateLock.Unlock()

	// Due to the asynchronous events, a session might received a "Joined" event
	// for the same (other) session twice, so filter these out on a per-session
	// level.
	result := make([]api.EventServerMessageSessionEntry, 0, len(entries))
	for _, e := range entries {
		if s.seenJoinedEvents[e.SessionId] {
			s.logger.Printf("Session %s got duplicate joined event for %s, ignoring", s.publicId, e.SessionId)
			continue
		}

		if s.seenJoinedEvents == nil {
			s.seenJoinedEvents = make(map[api.PublicSessionId]bool)
		}
		s.seenJoinedEvents[e.SessionId] = true
		result = append(result, e)
	}
	return result
}

func (s *ClientSession) filterDuplicateFlags(message *api.RoomFlagsServerMessage) bool {
	if message == nil {
		return true
	}

	s.filterDuplicateLock.Lock()
	defer s.filterDuplicateLock.Unlock()

	// Due to the asynchronous events, a session might received a "flags" event
	// for the same (other) session twice, so filter these out on a per-session
	// level.
	if prev, found := s.seenFlags[message.SessionId]; found && prev == message.Flags {
		s.logger.Printf("Session %s got duplicate flags event for %s, ignoring", s.publicId, message.SessionId)
		return true
	}

	if s.seenFlags == nil {
		s.seenFlags = make(map[api.PublicSessionId]uint32)
	}
	s.seenFlags[message.SessionId] = message.Flags
	return false
}

func (s *ClientSession) filterMessage(message *api.ServerMessage) *api.ServerMessage {
	switch message.Type {
	case "event":
		switch message.Event.Target {
		case "participants":
			switch message.Event.Type {
			case "update":
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
			case "flags":
				if s.filterDuplicateFlags(message.Event.Flags) {
					return nil
				}
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
					message = &api.ServerMessage{
						Id:   message.Id,
						Type: message.Type,
						Event: &api.EventServerMessage{
							Type:   message.Event.Type,
							Target: message.Event.Target,
							Join:   join,
						},
					}
				}

				if s.HasPermission(api.PERMISSION_HIDE_DISPLAYNAMES) {
					if copied {
						message.Event.Join = filterDisplayNames(message.Event.Join)
					} else {
						message = &api.ServerMessage{
							Id:   message.Id,
							Type: message.Type,
							Event: &api.EventServerMessage{
								Type:   message.Event.Type,
								Target: message.Event.Target,
								Join:   filterDisplayNames(message.Event.Join),
							},
						}
					}
				}
			case "leave":
				s.filterDuplicateLock.Lock()
				defer s.filterDuplicateLock.Unlock()

				leave := s.filterUnknownLeave(message.Event.Leave)
				if len(leave) == 0 {
					return nil
				}

				for _, e := range message.Event.Leave {
					delete(s.seenJoinedEvents, e)
					delete(s.seenFlags, e)
				}

				if len(leave) != len(message.Event.Leave) {
					message = &api.ServerMessage{
						Id:   message.Id,
						Type: message.Type,
						Event: &api.EventServerMessage{
							Type:   message.Event.Type,
							Target: message.Event.Target,
							Leave:  leave,
						},
					}
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
						if s.HasFeature(api.ClientFeatureChatRelay) {
							data.Chat.Refresh = false
						} else {
							data.Chat.Comment = nil
						}
						update = true
					}

					if len(data.Chat.Comment) > 0 && s.HasPermission(api.PERMISSION_HIDE_DISPLAYNAMES) {
						var comment api.ChatComment
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
							message = &api.ServerMessage{
								Id:   message.Id,
								Type: message.Type,
								Event: &api.EventServerMessage{
									Type:   message.Event.Type,
									Target: message.Event.Target,
									Message: &api.RoomEventMessage{
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
		if message.Message != nil && len(message.Message.Data) > 0 && s.HasPermission(api.PERMISSION_HIDE_DISPLAYNAMES) {
			var data api.MessageServerMessageData
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

func (s *ClientSession) filterAsyncMessage(msg *events.AsyncMessage) *api.ServerMessage {
	switch msg.Type {
	case "message":
		if msg.Message == nil {
			s.logger.Printf("Received asynchronous message without payload: %+v", msg)
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
					if sender.Type == api.RecipientTypeCall {
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
					if sender.Type == api.RecipientTypeCall {
						if room := s.GetRoom(); room == nil || !room.IsSessionInCall(s) {
							// Session is not in call, so discard.
							return nil
						}
					}
				}
			}
		case "event":
			if msg.Message.Event.Target == "room" || msg.Message.Event.Target == "participants" {
				// Can happen mostly during tests where an older room async message
				// could be received by a subscriber that joined after it was sent.
				if joined := s.getRoomJoinTime(); joined.IsZero() || msg.SendTime.Before(joined) {
					s.logger.Printf("Message %+v was sent on %s before session %s join room on %s, ignoring", msg.Message, msg.SendTime, s.publicId, joined)
					return nil
				}
			}
		}

		return msg.Message
	default:
		s.logger.Printf("Received async message with unsupported type %s: %+v", msg.Type, msg)
		return nil
	}
}

func (s *ClientSession) NotifySessionResumed(client ClientWithSession) {
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

	s.logger.Printf("Send %d pending messages to session %s", len(messages), s.PublicId())
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

func (s *ClientSession) ProcessResponse(message *api.ClientMessage) bool {
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
