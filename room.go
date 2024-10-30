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
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Must match values in "Participant.php" from Nextcloud Talk.
	FlagDisconnected = 0
	FlagInCall       = 1
	FlagWithAudio    = 2
	FlagWithVideo    = 4
	FlagWithPhone    = 8
)

type SessionChangeFlag int

const (
	SessionChangeFlags  SessionChangeFlag = 1
	SessionChangeInCall SessionChangeFlag = 2
)

var (
	updateActiveSessionsInterval = 10 * time.Second
)

func init() {
	RegisterRoomStats()
}

type Room struct {
	id      string
	hub     *Hub
	events  AsyncEvents
	backend *Backend

	properties json.RawMessage

	closer   *Closer
	mu       *sync.RWMutex
	sessions map[string]Session

	internalSessions map[*ClientSession]bool
	virtualSessions  map[*VirtualSession]bool
	inCallSessions   map[Session]bool
	roomSessionData  map[string]*RoomSessionData

	statsRoomSessionsCurrent *prometheus.GaugeVec

	// Users currently in the room
	users []map[string]interface{}

	// Timestamps of last backend requests for the different types.
	lastRoomRequests map[string]int64

	transientData *TransientData
}

func getRoomIdForBackend(id string, backend *Backend) string {
	if id == "" {
		return ""
	}

	return backend.Id() + "|" + id
}

func NewRoom(roomId string, properties json.RawMessage, hub *Hub, events AsyncEvents, backend *Backend) (*Room, error) {
	room := &Room{
		id:      roomId,
		hub:     hub,
		events:  events,
		backend: backend,

		properties: properties,

		closer:   NewCloser(),
		mu:       &sync.RWMutex{},
		sessions: make(map[string]Session),

		internalSessions: make(map[*ClientSession]bool),
		virtualSessions:  make(map[*VirtualSession]bool),
		inCallSessions:   make(map[Session]bool),
		roomSessionData:  make(map[string]*RoomSessionData),

		statsRoomSessionsCurrent: statsRoomSessionsCurrent.MustCurryWith(prometheus.Labels{
			"backend": backend.Id(),
			"room":    roomId,
		}),

		lastRoomRequests: make(map[string]int64),

		transientData: NewTransientData(),
	}

	if err := events.RegisterBackendRoomListener(roomId, backend, room); err != nil {
		return nil, err
	}

	go room.run()

	return room, nil
}

func (r *Room) Id() string {
	return r.id
}

func (r *Room) Properties() json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.properties
}

func (r *Room) Backend() *Backend {
	return r.backend
}

func (r *Room) IsEqual(other *Room) bool {
	if r == other {
		return true
	} else if other == nil {
		return false
	} else if r.Id() != other.Id() {
		return false
	}

	b1 := r.Backend()
	b2 := other.Backend()
	if b1 == b2 {
		return true
	} else if b1 == nil && b2 != nil {
		return false
	} else if b1 != nil && b2 == nil {
		return false
	}

	return b1.Id() == b2.Id()
}

func (r *Room) run() {
	ticker := time.NewTicker(updateActiveSessionsInterval)
loop:
	for {
		select {
		case <-r.closer.C:
			break loop
		case <-ticker.C:
			r.publishActiveSessions()
		}
	}
}

func (r *Room) doClose() {
	r.closer.Close()
}

func (r *Room) unsubscribeBackend() {
	r.events.UnregisterBackendRoomListener(r.id, r.backend, r)
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
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeClient})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeInternal})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeVirtual})
	r.mu.Unlock()
	return result
}

func (r *Room) ProcessBackendRoomRequest(message *AsyncMessage) {
	switch message.Type {
	case "room":
		r.processBackendRoomRequestRoom(message.Room)
	case "asyncroom":
		r.processBackendRoomRequestAsyncRoom(message.AsyncRoom)
	default:
		log.Printf("Unsupported backend room request with type %s in %s: %+v", message.Type, r.id, message)
	}
}

func (r *Room) processBackendRoomRequestRoom(message *BackendServerRoomRequest) {
	received := message.ReceivedTime
	if last, found := r.lastRoomRequests[message.Type]; found && last > received {
		if msg, err := json.Marshal(message); err == nil {
			log.Printf("Ignore old backend room request for %s: %s", r.Id(), string(msg))
		} else {
			log.Printf("Ignore old backend room request for %s: %+v", r.Id(), message)
		}
		return
	}

	r.lastRoomRequests[message.Type] = received
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
	case "switchto":
		r.publishSwitchTo(message.SwitchTo)
	case "transient":
		switch message.Transient.Action {
		case TransientActionSet:
			r.SetTransientDataTTL(message.Transient.Key, message.Transient.Value, message.Transient.TTL)
		case TransientActionDelete:
			r.RemoveTransientData(message.Transient.Key)
		default:
			log.Printf("Unsupported transient action in room %s: %+v", r.Id(), message.Transient)
		}
	default:
		log.Printf("Unsupported backend room request with type %s in %s: %+v", message.Type, r.Id(), message)
	}
}

func (r *Room) processBackendRoomRequestAsyncRoom(message *AsyncRoomMessage) {
	switch message.Type {
	case "sessionjoined":
		r.notifySessionJoined(message.SessionId)
		if message.ClientType == HelloClientTypeInternal {
			r.publishUsersChangedWithInternal()
		}
	default:
		log.Printf("Unsupported async room request with type %s in %s: %+v", message.Type, r.Id(), message)
	}
}

func (r *Room) AddSession(session Session, sessionData json.RawMessage) {
	var roomSessionData *RoomSessionData
	if len(sessionData) > 0 {
		roomSessionData = &RoomSessionData{}
		if err := json.Unmarshal(sessionData, roomSessionData); err != nil {
			log.Printf("Error decoding room session data \"%s\": %s", string(sessionData), err)
			roomSessionData = nil
		}
	}

	sid := session.PublicId()
	r.mu.Lock()
	_, found := r.sessions[sid]
	r.sessions[sid] = session
	if !found {
		r.statsRoomSessionsCurrent.With(prometheus.Labels{"clienttype": session.ClientType()}).Inc()
	}
	var publishUsersChanged bool
	switch session.ClientType() {
	case HelloClientTypeInternal:
		clientSession, ok := session.(*ClientSession)
		if !ok {
			delete(r.sessions, sid)
			r.mu.Unlock()
			panic(fmt.Sprintf("Expected a client session, got %v (%T)", session, session))
		}
		r.internalSessions[clientSession] = true
	case HelloClientTypeVirtual:
		virtualSession, ok := session.(*VirtualSession)
		if !ok {
			delete(r.sessions, sid)
			r.mu.Unlock()
			panic(fmt.Sprintf("Expected a virtual session, got %v (%T)", session, session))
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
		if clientSession, ok := session.(*ClientSession); ok {
			r.transientData.AddListener(clientSession)
		}
	}

	// Trigger notifications that the session joined.
	if err := r.events.PublishBackendRoomMessage(r.id, r.backend, &AsyncMessage{
		Type: "asyncroom",
		AsyncRoom: &AsyncRoomMessage{
			Type:       "sessionjoined",
			SessionId:  sid,
			ClientType: session.ClientType(),
		},
	}); err != nil {
		log.Printf("Error publishing joined event for session %s: %s", sid, err)
	}
}

func (r *Room) getOtherSessions(ignoreSessionId string) (Session, []Session) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sessions := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		if s.PublicId() == ignoreSessionId {
			continue
		}

		sessions = append(sessions, s)
	}

	return r.sessions[ignoreSessionId], sessions
}

func (r *Room) notifySessionJoined(sessionId string) {
	session, sessions := r.getOtherSessions(sessionId)
	if len(sessions) == 0 {
		return
	}

	if session != nil && session.ClientType() != HelloClientTypeClient {
		session = nil
	}

	events := make([]*EventServerMessageSessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entry := &EventServerMessageSessionEntry{
			SessionId: s.PublicId(),
			UserId:    s.UserId(),
			User:      s.UserData(),
		}
		if s, ok := s.(*ClientSession); ok {
			entry.RoomSessionId = s.RoomSessionId()
			entry.Federated = s.ClientType() == HelloClientTypeFederation
		}
		events = append(events, entry)
	}

	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "join",
			Join:   events,
		},
	}

	if err := r.events.PublishSessionMessage(sessionId, r.backend, &AsyncMessage{
		Type:    "message",
		Message: msg,
	}); err != nil {
		log.Printf("Error publishing joined events to session %s: %s", sessionId, err)
	}

	// Notify about initial flags of virtual sessions.
	for _, s := range sessions {
		vsess, ok := s.(*VirtualSession)
		if !ok {
			continue
		}

		flags := vsess.Flags()
		if flags == 0 {
			continue
		}

		msg := &ServerMessage{
			Type: "event",
			Event: &EventServerMessage{
				Target: "participants",
				Type:   "flags",
				Flags: &RoomFlagsServerMessage{
					RoomId:    r.id,
					SessionId: vsess.PublicId(),
					Flags:     vsess.Flags(),
				},
			},
		}

		if err := r.events.PublishSessionMessage(sessionId, r.backend, &AsyncMessage{
			Type:    "message",
			Message: msg,
		}); err != nil {
			log.Printf("Error publishing initial flags to session %s: %s", sessionId, err)
		}
	}
}

func (r *Room) HasSession(session Session) bool {
	r.mu.RLock()
	_, result := r.sessions[session.PublicId()]
	r.mu.RUnlock()
	return result
}

func (r *Room) IsSessionInCall(session Session) bool {
	r.mu.RLock()
	_, result := r.inCallSessions[session]
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
	r.statsRoomSessionsCurrent.With(prometheus.Labels{"clienttype": session.ClientType()}).Dec()
	delete(r.sessions, sid)
	if virtualSession, ok := session.(*VirtualSession); ok {
		delete(r.virtualSessions, virtualSession)
		// Handle case where virtual session was also sent by Nextcloud.
		users := make([]map[string]interface{}, 0, len(r.users))
		for _, u := range r.users {
			if u["sessionId"] != sid {
				users = append(users, u)
			}
		}
		if len(users) != len(r.users) {
			r.users = users
		}
	}
	if clientSession, ok := session.(*ClientSession); ok {
		delete(r.internalSessions, clientSession)
		r.transientData.RemoveListener(clientSession)
	}
	delete(r.inCallSessions, session)
	delete(r.roomSessionData, sid)
	if len(r.sessions) > 0 {
		r.mu.Unlock()
		r.PublishSessionLeft(session)
		return true
	}

	r.hub.removeRoom(r)
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeClient})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeInternal})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": HelloClientTypeVirtual})
	r.unsubscribeBackend()
	r.doClose()
	r.mu.Unlock()
	// Still need to publish an event so sessions on other servers get notified.
	r.PublishSessionLeft(session)
	return false
}

func (r *Room) publish(message *ServerMessage) error {
	return r.events.PublishRoomMessage(r.id, r.backend, &AsyncMessage{
		Type:    "message",
		Message: message,
	})
}

func (r *Room) UpdateProperties(properties json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if (len(r.properties) == 0 && len(properties) == 0) ||
		(len(r.properties) > 0 && len(properties) > 0 && bytes.Equal(r.properties, properties)) {
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
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish update properties message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) GetRoomSessionData(session Session) *RoomSessionData {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.roomSessionData[session.PublicId()]
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
				{
					SessionId: sessionId,
					UserId:    userid,
					User:      session.UserData(),
				},
			},
		},
	}
	if session, ok := session.(*ClientSession); ok {
		message.Event.Join[0].RoomSessionId = session.RoomSessionId()
		message.Event.Join[0].Federated = session.ClientType() == HelloClientTypeFederation
	}
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish session joined message in room %s: %s", r.Id(), err)
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
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish session left message in room %s: %s", r.Id(), err)
	}

	if session.ClientType() == HelloClientTypeInternal {
		r.publishUsersChangedWithInternal()
	}
}

func (r *Room) getClusteredInternalSessionsRLocked() (internal map[string]*InternalSessionData, virtual map[string]*VirtualSessionData) {
	if r.hub.rpcClients == nil {
		return nil, nil
	}

	r.mu.RUnlock()
	defer r.mu.RLock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, client := range r.hub.rpcClients.GetClients() {
		wg.Add(1)
		go func(c *GrpcClient) {
			defer wg.Done()

			clientInternal, clientVirtual, err := c.GetInternalSessions(ctx, r.Id(), r.Backend())
			if err != nil {
				log.Printf("Received error while getting internal sessions for %s@%s from %s: %s", r.Id(), r.Backend().Id(), c.Target(), err)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if internal == nil {
				internal = make(map[string]*InternalSessionData, len(clientInternal))
			}
			for sid, s := range clientInternal {
				internal[sid] = s
			}
			if virtual == nil {
				virtual = make(map[string]*VirtualSessionData, len(clientVirtual))
			}
			for sid, s := range clientVirtual {
				virtual[sid] = s
			}
		}(client)
	}
	wg.Wait()

	return
}

func (r *Room) addInternalSessions(users []map[string]interface{}) []map[string]interface{} {
	now := time.Now().Unix()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(users) == 0 && len(r.internalSessions) == 0 && len(r.virtualSessions) == 0 {
		return users
	}

	clusteredInternalSessions, clusteredVirtualSessions := r.getClusteredInternalSessionsRLocked()

	// Local sessions might have changed while waiting for clustered information.
	if len(users) == 0 && len(r.internalSessions) == 0 && len(r.virtualSessions) == 0 {
		return users
	}

	skipSession := make(map[string]bool)
	for _, user := range users {
		sessionid, found := user["sessionId"]
		if !found || sessionid == "" {
			continue
		}

		if userid, found := user["userId"]; !found || userid == "" {
			if roomSessionData, found := r.roomSessionData[sessionid.(string)]; found {
				user["userId"] = roomSessionData.UserId
			} else if sid, ok := sessionid.(string); ok {
				if entry, found := clusteredVirtualSessions[sid]; found {
					user["virtual"] = true
					user["inCall"] = entry.GetInCall()
					skipSession[sid] = true
				} else {
					for session := range r.virtualSessions {
						if session.PublicId() == sid {
							user["virtual"] = true
							user["inCall"] = session.GetInCall()
							skipSession[sid] = true
							break
						}
					}
				}
			}
		}
	}
	for session := range r.internalSessions {
		u := map[string]interface{}{
			"inCall":    session.GetInCall(),
			"sessionId": session.PublicId(),
			"lastPing":  now,
			"internal":  true,
		}
		if f := session.GetFeatures(); len(f) > 0 {
			u["features"] = f
		}
		users = append(users, u)
	}
	for _, session := range clusteredInternalSessions {
		u := map[string]interface{}{
			"inCall":    session.GetInCall(),
			"sessionId": session.GetSessionId(),
			"lastPing":  now,
			"internal":  true,
		}
		if f := session.GetFeatures(); len(f) > 0 {
			u["features"] = f
		}
		users = append(users, u)
	}
	for session := range r.virtualSessions {
		sid := session.PublicId()
		if skipSession[sid] {
			continue
		}
		skipSession[sid] = true
		users = append(users, map[string]interface{}{
			"inCall":    session.GetInCall(),
			"sessionId": sid,
			"lastPing":  now,
			"virtual":   true,
		})
	}
	for sid, session := range clusteredVirtualSessions {
		if skipSession[sid] {
			continue
		}

		users = append(users, map[string]interface{}{
			"inCall":    session.GetInCall(),
			"sessionId": sid,
			"lastPing":  now,
			"virtual":   true,
		})
	}
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
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish incall message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) PublishUsersInCallChangedAll(inCall int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var notify []*ClientSession
	if inCall&FlagInCall != 0 {
		// All connected sessions join the call.
		var joined []string
		for _, session := range r.sessions {
			clientSession, ok := session.(*ClientSession)
			if !ok {
				continue
			}

			if session.ClientType() == HelloClientTypeInternal ||
				session.ClientType() == HelloClientTypeFederation {
				continue
			}

			if !r.inCallSessions[session] {
				r.inCallSessions[session] = true
				joined = append(joined, session.PublicId())
			}
			notify = append(notify, clientSession)
		}

		if len(joined) == 0 {
			return
		}

		log.Printf("Sessions %v joined call %s", joined, r.id)
	} else if len(r.inCallSessions) > 0 {
		// Perform actual leaving asynchronously.
		ch := make(chan *ClientSession, 1)
		go func() {
			for {
				session := <-ch
				if session == nil {
					break
				}

				session.LeaveCall()
			}
		}()

		for _, session := range r.sessions {
			clientSession, ok := session.(*ClientSession)
			if !ok {
				continue
			}

			notify = append(notify, clientSession)
		}

		for session := range r.inCallSessions {
			if clientSession, ok := session.(*ClientSession); ok {
				ch <- clientSession
			}
		}
		close(ch)
		r.inCallSessions = make(map[Session]bool)
	} else {
		// All sessions already left the call, no need to notify.
		return
	}

	inCallMsg := json.RawMessage(strconv.FormatInt(int64(inCall), 10))

	message := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &RoomEventServerMessage{
				RoomId: r.id,
				InCall: inCallMsg,
				All:    true,
			},
		},
	}

	for _, session := range notify {
		if !session.SendMessage(message) {
			log.Printf("Could not send incall message from room %s to %s", r.Id(), session.PublicId())
		}
	}
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
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish users changed message in room %s: %s", r.Id(), err)
	}
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

func (r *Room) NotifySessionResumed(session *ClientSession) {
	message := r.getParticipantsUpdateMessage(r.users)
	if len(message.Event.Update.Users) == 0 {
		return
	}

	session.SendMessage(message)
}

func (r *Room) NotifySessionChanged(session Session, flags SessionChangeFlag) {
	if flags&SessionChangeFlags != 0 && session.ClientType() == HelloClientTypeVirtual {
		// Only notify if a virtual session has changed.
		if virtual, ok := session.(*VirtualSession); ok {
			r.publishSessionFlagsChanged(virtual)
		}
	}

	if flags&SessionChangeInCall != 0 {
		joinLeave := 0
		if clientSession, ok := session.(*ClientSession); ok {
			if clientSession.GetInCall()&FlagInCall != 0 {
				joinLeave = 1
			} else {
				joinLeave = 2
			}
		} else if virtual, ok := session.(*VirtualSession); ok {
			if virtual.GetInCall()&FlagInCall != 0 {
				joinLeave = 1
			} else {
				joinLeave = 2
			}
		}

		if joinLeave != 0 {
			if joinLeave == 1 {
				r.mu.Lock()
				if !r.inCallSessions[session] {
					r.inCallSessions[session] = true
					log.Printf("Session %s joined call %s", session.PublicId(), r.id)
				}
				r.mu.Unlock()
			} else if joinLeave == 2 {
				r.mu.Lock()
				delete(r.inCallSessions, session)
				r.mu.Unlock()
				if clientSession, ok := session.(*ClientSession); ok {
					clientSession.LeaveCall()
				}
			}

			// TODO: Check if we could send a smaller update message with only the changed session.
			r.publishUsersChangedWithInternal()
		}
	}
}

func (r *Room) publishUsersChangedWithInternal() {
	message := r.getParticipantsUpdateMessage(r.users)
	if len(message.Event.Update.Users) == 0 {
		return
	}

	if err := r.publish(message); err != nil {
		log.Printf("Could not publish users changed message in room %s: %s", r.Id(), err)
	}
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
	if err := r.publish(message); err != nil {
		log.Printf("Could not publish flags changed message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) publishActiveSessions() (int, *sync.WaitGroup) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make(map[string][]BackendPingEntry)
	urls := make(map[string]*url.URL)
	for _, session := range r.sessions {
		u := session.BackendUrl()
		if u == "" {
			continue
		}

		var sid string
		var uid string
		switch sess := session.(type) {
		case *ClientSession:
			// Use Nextcloud session id and user id
			sid = sess.RoomSessionId()
			uid = sess.AuthUserId()
		case *VirtualSession:
			// Use our internal generated session id (will be added to Nextcloud).
			sid = sess.PublicId()
			uid = sess.UserId()
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
			UserId:    uid,
		})
	}
	var wg sync.WaitGroup
	if len(urls) == 0 {
		return 0, &wg
	}
	var count int
	for u, e := range entries {
		wg.Add(1)
		count += len(e)
		go func(url *url.URL, entries []BackendPingEntry) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), r.hub.backendTimeout)
			defer cancel()

			if err := r.hub.roomPing.SendPings(ctx, r.id, url, entries); err != nil {
				log.Printf("Error pinging room %s for active entries %+v: %s", r.id, entries, err)
			}
		}(urls[u], e)
	}
	return count, &wg
}

func (r *Room) publishRoomMessage(message *BackendRoomMessageRequest) {
	if message == nil || len(message.Data) == 0 {
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
	if err := r.publish(msg); err != nil {
		log.Printf("Could not publish room message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) publishSwitchTo(message *BackendRoomSwitchToMessageRequest) {
	var wg sync.WaitGroup
	if len(message.SessionsList) > 0 {
		msg := &ServerMessage{
			Type: "event",
			Event: &EventServerMessage{
				Target: "room",
				Type:   "switchto",
				SwitchTo: &EventServerMessageSwitchTo{
					RoomId: message.RoomId,
				},
			},
		}

		for _, sessionId := range message.SessionsList {
			wg.Add(1)
			go func(sessionId string) {
				defer wg.Done()

				if err := r.events.PublishSessionMessage(sessionId, r.backend, &AsyncMessage{
					Type:    "message",
					Message: msg,
				}); err != nil {
					log.Printf("Error publishing switchto event to session %s: %s", sessionId, err)
				}
			}(sessionId)
		}
	}

	if len(message.SessionsMap) > 0 {
		for sessionId, details := range message.SessionsMap {
			wg.Add(1)
			go func(sessionId string, details json.RawMessage) {
				defer wg.Done()

				msg := &ServerMessage{
					Type: "event",
					Event: &EventServerMessage{
						Target: "room",
						Type:   "switchto",
						SwitchTo: &EventServerMessageSwitchTo{
							RoomId:  message.RoomId,
							Details: details,
						},
					},
				}

				if err := r.events.PublishSessionMessage(sessionId, r.backend, &AsyncMessage{
					Type:    "message",
					Message: msg,
				}); err != nil {
					log.Printf("Error publishing switchto event to session %s: %s", sessionId, err)
				}
			}(sessionId, details)
		}
	}
	wg.Wait()
}

func (r *Room) notifyInternalRoomDeleted() {
	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "room",
			Type:   "delete",
		},
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	for s := range r.internalSessions {
		s.SendMessage(msg)
	}
}

func (r *Room) SetTransientData(key string, value interface{}) {
	r.transientData.Set(key, value)
}

func (r *Room) SetTransientDataTTL(key string, value interface{}, ttl time.Duration) {
	r.transientData.SetTTL(key, value, ttl)
}

func (r *Room) RemoveTransientData(key string) {
	r.transientData.Remove(key)
}
