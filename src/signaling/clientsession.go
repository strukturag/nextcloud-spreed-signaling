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
	"log"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/nats-io/go-nats"
)

var (
	// Sessions expire 30 seconds after the connection closed.
	sessionExpireDuration = 30 * time.Second

	// Warn if a session has 32 or more pending messages.
	warnPendingMessagesCount = 32

	PathToOcsSignalingBackend = "ocs/v2.php/apps/spreed/api/v1/signaling/backend"
)

type ClientSession struct {
	running   int32
	hub       *Hub
	privateId string
	publicId  string
	data      *SessionIdData

	clientType string
	features   []string
	userId     string
	userData   *json.RawMessage

	supportsPermissions bool
	permissions         map[Permission]bool

	backend          *Backend
	backendUrl       string
	parsedBackendUrl *url.URL

	natsReceiver chan *nats.Msg
	stopRun      chan bool
	runStopped   chan bool
	expires      time.Time

	mu sync.Mutex

	client        *Client
	room          unsafe.Pointer
	roomSessionId string

	userSubscription    NatsSubscription
	sessionSubscription NatsSubscription
	roomSubscription    NatsSubscription

	publishers  map[string]McuPublisher
	subscribers map[string]McuSubscriber

	pendingClientMessages        []*ServerMessage
	hasPendingChat               bool
	hasPendingParticipantsUpdate bool

	virtualSessions map[*VirtualSession]bool
}

func NewClientSession(hub *Hub, privateId string, publicId string, data *SessionIdData, backend *Backend, hello *HelloClientMessage, auth *BackendClientAuthResponse) (*ClientSession, error) {
	s := &ClientSession{
		hub:       hub,
		privateId: privateId,
		publicId:  publicId,
		data:      data,

		clientType: hello.Auth.Type,
		features:   hello.Features,
		userId:     auth.UserId,
		userData:   auth.User,

		backend: backend,

		natsReceiver: make(chan *nats.Msg, 64),
		stopRun:      make(chan bool, 1),
		runStopped:   make(chan bool, 1),
	}
	if s.clientType == HelloClientTypeInternal {
		s.backendUrl = hello.Auth.internalParams.Backend
		s.parsedBackendUrl = hello.Auth.internalParams.parsedBackend
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
		if u, err := url.Parse(backendUrl); err != nil {
			return nil, err
		} else {
			s.backendUrl = backendUrl
			s.parsedBackendUrl = u
		}
	}

	if err := s.SubscribeNats(hub.nats); err != nil {
		return nil, err
	}
	atomic.StoreInt32(&s.running, 1)
	go s.run()
	return s, nil
}

func (s *ClientSession) PrivateId() string {
	return s.privateId
}

func (s *ClientSession) PublicId() string {
	return s.publicId
}

func (s *ClientSession) RoomSessionId() string {
	return s.roomSessionId
}

func (s *ClientSession) Data() *SessionIdData {
	return s.data
}

func (s *ClientSession) ClientType() string {
	return s.clientType
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

func (s *ClientSession) HasPermission(permission Permission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.supportsPermissions {
		// Old-style session that doesn't receive permissions from Nextcloud.
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

func (s *ClientSession) UserId() string {
	return s.userId
}

func (s *ClientSession) UserData() *json.RawMessage {
	return s.userData
}

func (s *ClientSession) run() {
loop:
	for {
		select {
		case msg := <-s.natsReceiver:
			s.processClientMessage(msg)
		case <-s.stopRun:
			break loop
		}
	}
	s.runStopped <- true
}

func (s *ClientSession) StartExpire() {
	// The hub mutex must be held when calling this method.
	s.expires = time.Now().Add(sessionExpireDuration)
	s.hub.expiredSessions[s] = true
}

func (s *ClientSession) StopExpire() {
	// The hub mutex must be held when calling this method.
	delete(s.hub.expiredSessions, s)
}

func (s *ClientSession) IsExpired(now time.Time) bool {
	return now.After(s.expires)
}

func (s *ClientSession) SetRoom(room *Room) {
	atomic.StorePointer(&s.room, unsafe.Pointer(room))
}

func (s *ClientSession) GetRoom() *Room {
	return (*Room)(atomic.LoadPointer(&s.room))
}

func (s *ClientSession) releaseMcuObjects() {
	if len(s.publishers) > 0 {
		go func(publishers map[string]McuPublisher) {
			ctx := context.TODO()
			for _, publisher := range publishers {
				publisher.Close(ctx)
			}
		}(s.publishers)
		s.publishers = nil
	}
	if len(s.subscribers) > 0 {
		go func(subscribers map[string]McuSubscriber) {
			ctx := context.TODO()
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
	s.hub.removeSession(s)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userSubscription != nil {
		s.userSubscription.Unsubscribe()
		s.userSubscription = nil
	}
	if s.sessionSubscription != nil {
		s.sessionSubscription.Unsubscribe()
		s.sessionSubscription = nil
	}
	go func(virtualSessions map[*VirtualSession]bool) {
		for session, _ := range virtualSessions {
			session.Close()
		}
	}(s.virtualSessions)
	s.virtualSessions = nil
	s.releaseMcuObjects()
	s.clearClientLocked(nil)
	if atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		s.stopRun <- true
		// Only wait if called from outside the Session goroutine.
		if wait {
			s.mu.Unlock()
			// Wait for Session goroutine to stop
			<-s.runStopped
			s.mu.Lock()
		}
	}
}

func GetSubjectForUserId(userId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("user", userId)
	} else {
		return GetEncodedSubject("user", userId+"|"+backend.Id())
	}
}

func (s *ClientSession) SubscribeNats(n NatsClient) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.userId != "" {
		if s.userSubscription, err = n.Subscribe(GetSubjectForUserId(s.userId, s.backend), s.natsReceiver); err != nil {
			return err
		}
	}

	if s.sessionSubscription, err = n.Subscribe("session."+s.publicId, s.natsReceiver); err != nil {
		return err
	}

	return nil
}

func (s *ClientSession) SubscribeRoomNats(n NatsClient, roomid string, roomSessionId string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.roomSubscription, err = n.Subscribe(GetSubjectForRoomId(roomid, s.Backend()), s.natsReceiver); err != nil {
		return err
	}

	if roomSessionId != "" {
		if err = s.hub.roomSessions.SetRoomSession(s, roomSessionId); err != nil {
			s.doUnsubscribeRoomNats(true)
			return err
		}
	}
	log.Printf("Session %s joined room %s with room session id %s\n", s.PublicId(), roomid, roomSessionId)
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

	log.Printf("Session %s left call %s\n", s.PublicId(), room.Id())
	s.releaseMcuObjects()
}

func (s *ClientSession) LeaveRoom(notify bool) *Room {
	s.mu.Lock()
	defer s.mu.Unlock()

	room := s.GetRoom()
	if room == nil {
		return nil
	}

	s.doUnsubscribeRoomNats(notify)
	s.SetRoom(nil)
	s.releaseMcuObjects()
	room.RemoveSession(s)
	return room
}

func (s *ClientSession) UnsubscribeRoomNats() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.doUnsubscribeRoomNats(true)
}

func (s *ClientSession) doUnsubscribeRoomNats(notify bool) {
	if s.roomSubscription != nil {
		s.roomSubscription.Unsubscribe()
		s.roomSubscription = nil
	}
	s.hub.roomSessions.DeleteRoomSession(s)
	room := s.GetRoom()
	if notify && room != nil && s.roomSessionId != "" {
		// Notify
		go func(sid string) {
			ctx := context.Background()
			request := NewBackendClientRoomRequest(room.Id(), s.UserId(), sid)
			request.Room.Action = "leave"
			var response map[string]interface{}
			if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendUrl(), request, &response); err != nil {
				log.Printf("Could not notify about room session %s left room %s: %s", sid, room.Id(), err)
			} else {
				log.Printf("Removed room session %s: %+v", sid, response)
			}
		}(s.roomSessionId)
	}
	s.roomSessionId = ""
}

func (s *ClientSession) ClearClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearClientLocked(client)
}

func (s *ClientSession) clearClientLocked(client *Client) {
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

func (s *ClientSession) GetClient() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getClientUnlocked()
}

func (s *ClientSession) getClientUnlocked() *Client {
	return s.client
}

func (s *ClientSession) SetClient(client *Client) *Client {
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

func (s *ClientSession) sendCandidate(client McuClient, sender string, streamType string, candidate interface{}) {
	candidate_message := &AnswerOfferMessage{
		To:       s.PublicId(),
		From:     sender,
		Type:     "candidate",
		RoomType: streamType,
		Payload: map[string]interface{}{
			"candidate": candidate,
		},
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
			Data: (*json.RawMessage)(&candidate_data),
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

func (s *ClientSession) SendMessage(message *ServerMessage) bool {
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

	log.Printf("Session %s received candidate %+v for unknown client %s", s.PublicId(), candidate, client.Id())
}

func (s *ClientSession) OnIceCompleted(client McuClient) {
	// TODO(jojo): This causes a JavaScript error when creating a candidate from "null".
	// Figure out a better way to signal this.

	// An empty candidate signals the end of candidates.
	// s.OnIceCandidate(client, nil)
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

func (s *ClientSession) GetOrCreatePublisher(ctx context.Context, mcu Mcu, streamType string) (McuPublisher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	publisher, found := s.publishers[streamType]
	if !found {
		client := s.getClientUnlocked()
		s.mu.Unlock()
		var err error
		publisher, err = mcu.NewPublisher(ctx, s, s.PublicId(), streamType, client)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		if s.publishers == nil {
			s.publishers = make(map[string]McuPublisher)
		}
		if prev, found := s.publishers[streamType]; found {
			// Another thread created the publisher while we were waiting.
			go func(pub McuPublisher) {
				closeCtx := context.TODO()
				pub.Close(closeCtx)
			}(publisher)
			publisher = prev
		} else {
			s.publishers[streamType] = publisher
		}
		log.Printf("Publishing %s as %s for session %s", streamType, publisher.Id(), s.PublicId())
	}

	return publisher, nil
}

func (s *ClientSession) GetPublisher(streamType string) McuPublisher {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.publishers[streamType]
}

func (s *ClientSession) GetOrCreateSubscriber(ctx context.Context, mcu Mcu, id string, streamType string) (McuSubscriber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO(jojo): Add method to remove subscribers.

	subscriber, found := s.subscribers[id+"|"+streamType]
	if !found {
		s.mu.Unlock()
		var err error
		subscriber, err = mcu.NewSubscriber(ctx, s, id, streamType)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		if s.subscribers == nil {
			s.subscribers = make(map[string]McuSubscriber)
		}
		if prev, found := s.subscribers[id+"|"+streamType]; found {
			// Another thread created the subscriber while we were waiting.
			go func(sub McuSubscriber) {
				closeCtx := context.TODO()
				sub.Close(closeCtx)
			}(subscriber)
			subscriber = prev
		} else {
			s.subscribers[id+"|"+streamType] = subscriber
		}
		log.Printf("Subscribing %s from %s as %s in session %s", streamType, id, subscriber.Id(), s.PublicId())
	}

	return subscriber, nil
}

func (s *ClientSession) GetSubscriber(id string, streamType string) McuSubscriber {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.subscribers[id+"|"+streamType]
}

func (s *ClientSession) processClientMessage(msg *nats.Msg) {
	var message NatsMessage
	if err := s.hub.nats.Decode(msg, &message); err != nil {
		log.Printf("Could not decode NATS message %+v for session %s: %s", *msg, s.PublicId(), err)
		return
	}

	switch message.Type {
	case "permissions":
		s.SetPermissions(message.Permissions)
		return
	case "message":
		if message.Message.Type == "bye" && message.Message.Bye.Reason == "room_session_reconnected" {
			s.mu.Lock()
			roomSessionId := s.RoomSessionId()
			s.mu.Unlock()
			log.Printf("Closing session %s because same room session %s connected", s.PublicId(), roomSessionId)
			s.LeaveRoom(false)
			defer s.closeAndWait(false)
		}
	}

	serverMessage := s.processNatsMessage(&message)
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
		log.Printf("Session %s has %d pending messages", s.PublicId(), len(s.pendingClientMessages))
	}
}

func (s *ClientSession) processNatsMessage(msg *NatsMessage) *ServerMessage {
	switch msg.Type {
	case "message":
		if msg.Message == nil {
			log.Printf("Received NATS message without payload: %+v\n", msg)
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
			if msg.Message.Event.Target == "participants" &&
				msg.Message.Event.Type == "update" {
				m := msg.Message.Event.Update
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
		}

		return msg.Message
	default:
		log.Printf("Received NATS message with unsupported type %s: %+v\n", msg.Type, msg)
		return nil
	}
}

func (s *ClientSession) NotifySessionResumed(client *Client) {
	s.mu.Lock()
	if len(s.pendingClientMessages) == 0 {
		s.mu.Unlock()
		if room := s.GetRoom(); room != nil {
			room.NotifySessionResumed(client)
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
			room.NotifySessionResumed(client)
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
