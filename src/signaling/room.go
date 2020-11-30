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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/nats-io/go-nats"
)

const (
	// Must match values in "Participant.php" from Nextcloud Talk.
	FlagDisconnected = 0
	FlagInCall       = 1
	FlagWithAudio    = 2
	FlagWithVideo    = 4
	FlagWithPhone    = 8
)

var (
	updateActiveSessionsInterval = 10 * time.Second
)

type Room struct {
	id      string
	hub     *Hub
	nats    NatsClient
	backend *Backend

	properties *json.RawMessage

	closeChan chan bool
	mu        *sync.RWMutex
	sessions  map[string]Session

	internalSessions map[Session]bool
	virtualSessions  map[*VirtualSession]bool
	inCallSessions   map[Session]bool
	roomSessionData  map[string]*RoomSessionData

	natsReceiver        chan *nats.Msg
	backendSubscription NatsSubscription

	// Users currently in the room
	users []map[string]interface{}

	// Timestamps of last NATS backend requests for the different types.
	lastNatsRoomRequests map[string]int64
}

func GetSubjectForRoomId(roomId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("room", roomId)
	} else {
		return GetEncodedSubject("room", roomId+"|"+backend.Id())
	}
}

func GetSubjectForBackendRoomId(roomId string, backend *Backend) string {
	if backend == nil || backend.IsCompat() {
		return GetEncodedSubject("backend.room", roomId)
	} else {
		return GetEncodedSubject("backend.room", roomId+"|"+backend.Id())
	}
}

func getRoomIdForBackend(id string, backend *Backend) string {
	if id == "" {
		return ""
	}

	return backend.Id() + "|" + id
}

func NewRoom(roomId string, properties *json.RawMessage, hub *Hub, n NatsClient, backend *Backend) (*Room, error) {
	natsReceiver := make(chan *nats.Msg, 64)
	backendSubscription, err := n.Subscribe(GetSubjectForBackendRoomId(roomId, backend), natsReceiver)
	if err != nil {
		close(natsReceiver)
		return nil, err
	}

	room := &Room{
		id:      roomId,
		hub:     hub,
		nats:    n,
		backend: backend,

		properties: properties,

		closeChan: make(chan bool, 1),
		mu:        &sync.RWMutex{},
		sessions:  make(map[string]Session),

		internalSessions: make(map[Session]bool),
		virtualSessions:  make(map[*VirtualSession]bool),
		inCallSessions:   make(map[Session]bool),
		roomSessionData:  make(map[string]*RoomSessionData),

		natsReceiver:        natsReceiver,
		backendSubscription: backendSubscription,

		lastNatsRoomRequests: make(map[string]int64),
	}
	go room.run()

	return room, nil
}

func (r *Room) Id() string {
	return r.id
}

func (r *Room) Properties() *json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.properties
}

func (r *Room) Backend() *Backend {
	return r.backend
}

func (r *Room) run() {
	ticker := time.NewTicker(updateActiveSessionsInterval)
loop:
	for {
		select {
		case <-r.closeChan:
			break loop
		case msg := <-r.natsReceiver:
			if msg != nil {
				r.processNatsMessage(msg)
			}
		case <-ticker.C:
			r.publishActiveSessions()
		}
	}
}

func (r *Room) doClose() {
	select {
	case r.closeChan <- true:
	default:
	}
}

func (r *Room) unsubscribeBackend() {
	if r.backendSubscription == nil {
		return
	}

	go func(subscription NatsSubscription) {
		subscription.Unsubscribe()
		close(r.natsReceiver)
	}(r.backendSubscription)
	r.backendSubscription = nil
}

func (r *Room) Close() []Session {
	r.hub.removeRoom(r)
	r.doClose()
	r.mu.Lock()
	r.unsubscribeBackend()
	result := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		result = append(result, s)
	}
	r.sessions = nil
	r.mu.Unlock()
	return result
}

func (r *Room) processNatsMessage(message *nats.Msg) {
	var msg NatsMessage
	if err := r.nats.Decode(message, &msg); err != nil {
		log.Printf("Could not decode nats message %+v, %s", message, err)
		return
	}

	switch msg.Type {
	case "room":
		r.processBackendRoomRequest(msg.Room)
	default:
		log.Printf("Unsupported NATS room request with type %s: %+v", msg.Type, msg)
	}
}

func (r *Room) processBackendRoomRequest(message *BackendServerRoomRequest) {
	received := message.ReceivedTime
	if last, found := r.lastNatsRoomRequests[message.Type]; found && last > received {
		if msg, err := json.Marshal(message); err == nil {
			log.Printf("Ignore old NATS backend room request for %s: %s", r.Id(), string(msg))
		} else {
			log.Printf("Ignore old NATS backend room request for %s: %+v", r.Id(), message)
		}
		return
	}

	r.lastNatsRoomRequests[message.Type] = received
	message.room = r
	switch message.Type {
	case "update":
		r.hub.roomUpdated <- message
	case "delete":
		r.notifyInternalRoomDeleted()
		r.hub.roomDeleted <- message
	case "incall":
		r.hub.roomInCall <- message
	case "participants":
		r.hub.roomParticipants <- message
	case "message":
		r.publishRoomMessage(message.Message)
	default:
		log.Printf("Unsupported NATS backend room request with type %s in %s: %+v", message.Type, r.Id(), message)
	}
}

func (r *Room) AddSession(session Session, sessionData *json.RawMessage) []Session {
	var roomSessionData *RoomSessionData
	if sessionData != nil && len(*sessionData) > 0 {
		roomSessionData = &RoomSessionData{}
		if err := json.Unmarshal(*sessionData, roomSessionData); err != nil {
			log.Printf("Error decoding room session data \"%s\": %s", string(*sessionData), err)
			roomSessionData = nil
		}
	}

	sid := session.PublicId()
	r.mu.Lock()
	_, found := r.sessions[sid]
	// Return list of sessions already in the room.
	result := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		if s != session {
			result = append(result, s)
		}
	}
	r.sessions[sid] = session
	var publishUsersChanged bool
	switch session.ClientType() {
	case HelloClientTypeInternal:
		r.internalSessions[session] = true
	case HelloClientTypeVirtual:
		virtualSession, ok := session.(*VirtualSession)
		if !ok {
			delete(r.sessions, sid)
			r.mu.Unlock()
			panic(fmt.Sprintf("Expected a virtual session, got %v", session))
		}
		r.virtualSessions[virtualSession] = true
		publishUsersChanged = true
	}
	if roomSessionData != nil {
		r.roomSessionData[sid] = roomSessionData
		log.Printf("Session %s sent room session data %+v", session.PublicId(), roomSessionData)
	}
	r.mu.Unlock()
	if !found {
		r.PublishSessionJoined(session, roomSessionData)
		if publishUsersChanged {
			r.publishUsersChangedWithInternal()
			if session, ok := session.(*VirtualSession); ok && session.Flags() != 0 {
				r.publishSessionFlagsChanged(session)
			}
		}
	}
	return result
}

func (r *Room) HasSession(session Session) bool {
	r.mu.RLock()
	_, result := r.sessions[session.PublicId()]
	r.mu.RUnlock()
	return result
}

// Returns "true" if there are still clients in the room.
func (r *Room) RemoveSession(session Session) bool {
	r.mu.Lock()
	if _, found := r.sessions[session.PublicId()]; !found {
		r.mu.Unlock()
		return true
	}

	sid := session.PublicId()
	delete(r.sessions, sid)
	delete(r.internalSessions, session)
	if virtualSession, ok := session.(*VirtualSession); ok {
		delete(r.virtualSessions, virtualSession)
	}
	delete(r.inCallSessions, session)
	delete(r.roomSessionData, sid)
	if len(r.sessions) > 0 {
		r.mu.Unlock()
		r.PublishSessionLeft(session)
		return true
	}

	r.hub.removeRoom(r)
	r.unsubscribeBackend()
	r.doClose()
	r.mu.Unlock()
	return false
}

func (r *Room) publish(message *ServerMessage) {
	r.nats.PublishMessage(GetSubjectForRoomId(r.id, r.backend), message)
}

func (r *Room) UpdateProperties(properties *json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if (r.properties == nil && properties == nil) ||
		(r.properties != nil && properties != nil && bytes.Equal(*r.properties, *properties)) {
		// Don't notify if properties didn't change.
		return
	}

	r.properties = properties
	message := &ServerMessage{
		Type: "room",
		Room: &RoomServerMessage{
			RoomId:     r.id,
			Properties: r.properties,
		},
	}
	r.publish(message)
}

func (r *Room) PublishSessionJoined(session Session, sessionData *RoomSessionData) {
	sessionId := session.PublicId()
	if sessionId == "" {
		return
	}

	userid := session.UserId()
	if userid == "" && sessionData != nil {
		userid = sessionData.UserId
	}

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "join",
			Join: []*EventServerMessageSessionEntry{
				&EventServerMessageSessionEntry{
					SessionId: sessionId,
					UserId:    userid,
					User:      session.UserData(),
				},
			},
		},
	}
	r.publish(message)

	if session.ClientType() == HelloClientTypeInternal {
		r.publishUsersChangedWithInternal()
	}
}

func (r *Room) PublishSessionLeft(session Session) {
	sessionId := session.PublicId()
	if sessionId == "" {
		return
	}

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "leave",
			Leave: []string{
				sessionId,
			},
		},
	}
	r.publish(message)

	if session.ClientType() == HelloClientTypeInternal {
		r.publishUsersChangedWithInternal()
	}
}

func (r *Room) addInternalSessions(users []map[string]interface{}) []map[string]interface{} {
	now := time.Now().Unix()
	r.mu.Lock()
	for _, user := range users {
		sessionid, found := user["sessionId"]
		if !found || sessionid == "" {
			continue
		}

		if userid, found := user["userId"]; !found || userid == "" {
			if roomSessionData, found := r.roomSessionData[sessionid.(string)]; found {
				user["userId"] = roomSessionData.UserId
			}
		}
	}
	for session := range r.internalSessions {
		users = append(users, map[string]interface{}{
			"inCall":    FlagInCall | FlagWithAudio,
			"sessionId": session.PublicId(),
			"lastPing":  now,
			"internal":  true,
		})
	}
	for session := range r.virtualSessions {
		users = append(users, map[string]interface{}{
			"inCall":    FlagInCall | FlagWithPhone,
			"sessionId": session.PublicId(),
			"lastPing":  now,
			"virtual":   true,
		})
	}
	r.mu.Unlock()
	return users
}

func (r *Room) filterPermissions(users []map[string]interface{}) []map[string]interface{} {
	for _, user := range users {
		delete(user, "permissions")
	}
	return users
}

func IsInCall(value interface{}) (bool, bool) {
	switch value := value.(type) {
	case bool:
		return value, true
	case float64:
		// Default JSON decoder unmarshals numbers to float64.
		return (int(value) & FlagInCall) == FlagInCall, true
	case int:
		return (value & FlagInCall) == FlagInCall, true
	case json.Number:
		// Expect integer when using numeric JSON decoder.
		if flags, err := value.Int64(); err == nil {
			return (flags & FlagInCall) == FlagInCall, true
		}
		return false, false
	default:
		return false, false
	}
}

func (r *Room) PublishUsersInCallChanged(changed []map[string]interface{}, users []map[string]interface{}) {
	r.users = users
	for _, user := range changed {
		inCallInterface, found := user["inCall"]
		if !found {
			continue
		}
		inCall, ok := IsInCall(inCallInterface)
		if !ok {
			continue
		}

		sessionIdInterface, found := user["sessionId"]
		if !found {
			sessionIdInterface, found = user["sessionid"]
			if !found {
				continue
			}
		}

		sessionId, ok := sessionIdInterface.(string)
		if !ok {
			continue
		}

		session := r.hub.GetSessionByPublicId(sessionId)
		if session == nil {
			continue
		}

		if inCall {
			r.mu.Lock()
			if !r.inCallSessions[session] {
				r.inCallSessions[session] = true
				log.Printf("Session %s joined call %s", session.PublicId(), r.id)
			}
			r.mu.Unlock()
		} else {
			r.mu.Lock()
			delete(r.inCallSessions, session)
			r.mu.Unlock()
			if clientSession, ok := session.(*ClientSession); ok {
				clientSession.LeaveCall()
			}
		}
	}

	changed = r.filterPermissions(changed)
	users = r.filterPermissions(users)

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &RoomEventServerMessage{
				RoomId:  r.id,
				Changed: changed,
				Users:   r.addInternalSessions(users),
			},
		},
	}
	r.publish(message)
}

func (r *Room) PublishUsersChanged(changed []map[string]interface{}, users []map[string]interface{}) {
	changed = r.filterPermissions(changed)
	users = r.filterPermissions(users)

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &RoomEventServerMessage{
				RoomId:  r.id,
				Changed: changed,
				Users:   r.addInternalSessions(users),
			},
		},
	}
	r.publish(message)
}

func (r *Room) getParticipantsUpdateMessage(users []map[string]interface{}) *ServerMessage {
	users = r.filterPermissions(users)

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &RoomEventServerMessage{
				RoomId: r.id,
				Users:  r.addInternalSessions(users),
			},
		},
	}
	return message
}

func (r *Room) NotifySessionResumed(client *Client) {
	message := r.getParticipantsUpdateMessage(r.users)
	if len(message.Event.Update.Users) == 0 {
		return
	}

	client.SendMessage(message)
}

func (r *Room) NotifySessionChanged(session Session) {
	if session.ClientType() != HelloClientTypeVirtual {
		// Only notify if a virtual session has changed.
		return
	}

	virtual, ok := session.(*VirtualSession)
	if !ok {
		return
	}

	r.publishSessionFlagsChanged(virtual)
}

func (r *Room) publishUsersChangedWithInternal() {
	message := r.getParticipantsUpdateMessage(r.users)
	r.publish(message)
}

func (r *Room) publishSessionFlagsChanged(session *VirtualSession) {
	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "participants",
			Type:   "flags",
			Flags: &RoomFlagsServerMessage{
				RoomId:    r.id,
				SessionId: session.PublicId(),
				Flags:     session.Flags(),
			},
		},
	}
	r.publish(message)
}

func (r *Room) publishActiveSessions() {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries := make(map[string][]BackendPingEntry)
	urls := make(map[string]*url.URL)
	for _, session := range r.sessions {
		u := session.BackendUrl()
		if u == "" {
			continue
		}

		var sid string
		switch sess := session.(type) {
		case *ClientSession:
			// Use Nextcloud session id
			sid = sess.RoomSessionId()
		case *VirtualSession:
			// Use our internal generated session id (will be added to Nextcloud).
			sid = sess.PublicId()
		default:
			continue
		}
		if sid == "" {
			continue
		}
		e, found := entries[u]
		if !found {
			p := session.ParsedBackendUrl()
			if p == nil {
				// Should not happen, invalid URLs should get rejected earlier.
				continue
			}
			urls[u] = p
		}

		entries[u] = append(e, BackendPingEntry{
			SessionId: sid,
			UserId:    session.UserId(),
		})
	}
	if len(urls) == 0 {
		return
	}
	for u, e := range entries {
		go func(url *url.URL, entries []BackendPingEntry) {
			ctx, cancel := context.WithTimeout(context.Background(), r.hub.backendTimeout)
			defer cancel()

			request := NewBackendClientPingRequest(r.id, entries)
			var response BackendClientResponse
			if err := r.hub.backend.PerformJSONRequest(ctx, url, request, &response); err != nil {
				log.Printf("Error pinging room %s for active entries %+v: %s", r.id, entries, err)
			}
		}(urls[u], e)
	}
}

func (r *Room) publishRoomMessage(message *BackendRoomMessageRequest) {
	if message == nil || message.Data == nil {
		return
	}

	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "message",
			Message: &RoomEventMessage{
				RoomId: r.id,
				Data:   message.Data,
			},
		},
	}
	r.publish(msg)
}

func (r *Room) notifyInternalRoomDeleted() {
	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "delete",
		},
	}
	for s := range r.internalSessions {
		s.(*ClientSession).SendMessage(msg)
	}
}
