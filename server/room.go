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
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
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
	updateRoomBandwidthInterval  = 1 * time.Second
)

func init() {
	RegisterRoomStats()
}

type Room struct {
	id      string
	logger  log.Logger
	hub     *Hub
	events  events.AsyncEvents
	backend *talk.Backend

	// +checklocks:mu
	properties json.RawMessage

	closer  *internal.Closer
	mu      *sync.RWMutex
	asyncCh events.AsyncChannel
	// +checklocks:mu
	sessions map[api.PublicSessionId]Session
	// +checklocks:mu
	internalSessions map[*ClientSession]bool
	// +checklocks:mu
	virtualSessions map[*VirtualSession]bool
	// +checklocks:mu
	inCallSessions map[Session]bool
	// +checklocks:mu
	roomSessionData map[api.PublicSessionId]*talk.RoomSessionData

	// +checklocks:mu
	statsRoomSessionsCurrent *prometheus.GaugeVec
	// +checklocks:mu
	statsCallSessionsCurrent *prometheus.GaugeVec
	// +checklocks:mu
	statsCallSessionsTotal *prometheus.CounterVec
	// +checklocks:mu
	statsCallRoomsTotal prometheus.Counter

	// Users currently in the room
	users []api.StringMap

	// Timestamps of last backend requests for the different types.
	lastRoomRequests map[string]int64

	transientData *api.TransientData

	allPublishersCount    atomic.Uint32
	localPublishersCount  atomic.Uint32
	localSubscribersCount atomic.Uint32
	localBandwidth        atomic.Pointer[sfu.ClientBandwidthInfo]
	bandwidthConfigured   bool
}

func getRoomIdForBackend(id string, backend *talk.Backend) string {
	if id == "" {
		return ""
	}

	return backend.Id() + "|" + id
}

func NewRoom(roomId string, properties json.RawMessage, hub *Hub, asyncEvents events.AsyncEvents, backend *talk.Backend) (*Room, error) {
	room := &Room{
		id:      roomId,
		logger:  hub.logger,
		hub:     hub,
		events:  asyncEvents,
		backend: backend,

		properties: properties,

		closer:   internal.NewCloser(),
		mu:       &sync.RWMutex{},
		asyncCh:  make(events.AsyncChannel, events.DefaultAsyncChannelSize),
		sessions: make(map[api.PublicSessionId]Session),

		internalSessions: make(map[*ClientSession]bool),
		virtualSessions:  make(map[*VirtualSession]bool),
		inCallSessions:   make(map[Session]bool),
		roomSessionData:  make(map[api.PublicSessionId]*talk.RoomSessionData),

		statsRoomSessionsCurrent: statsRoomSessionsCurrent.MustCurryWith(prometheus.Labels{
			"backend": backend.Id(),
			"room":    roomId,
		}),
		statsCallSessionsCurrent: statsCallSessionsCurrent.MustCurryWith(prometheus.Labels{
			"backend": backend.Id(),
			"room":    roomId,
		}),
		statsCallSessionsTotal: statsCallSessionsTotal.MustCurryWith(prometheus.Labels{
			"backend": backend.Id(),
		}),
		statsCallRoomsTotal: statsCallRoomsTotal.WithLabelValues(backend.Id()),

		lastRoomRequests: make(map[string]int64),

		transientData: api.NewTransientData(),
	}

	if err := asyncEvents.RegisterBackendRoomListener(roomId, backend, room); err != nil {
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

func (r *Room) Backend() *talk.Backend {
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

func (r *Room) AsyncChannel() events.AsyncChannel {
	return r.asyncCh
}

func (r *Room) run() {
	sessionsTicker := time.NewTicker(updateActiveSessionsInterval)
	bandwidtTicker := time.NewTicker(updateRoomBandwidthInterval)

loop:
	for {
		select {
		case <-r.closer.C:
			break loop
		case msg := <-r.asyncCh:
			r.processAsyncNatsMessage(msg)
			for count := len(r.asyncCh); count > 0; count-- {
				r.processAsyncNatsMessage(<-r.asyncCh)
			}
		case <-sessionsTicker.C:
			r.publishActiveSessions()
		case <-bandwidtTicker.C:
			r.updateBandwidth()
		}
	}
}

func (r *Room) doClose() {
	r.closer.Close()
}

func (r *Room) unsubscribeBackend() {
	if err := r.events.UnregisterBackendRoomListener(r.id, r.backend, r); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		r.logger.Printf("Error unsubscribing room listener in %s: %s", r.id, err)
	}
}

func (r *Room) Close() []Session {
	r.hub.removeRoom(r)
	r.doClose()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unsubscribeBackend()
	result := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		result = append(result, s)
	}
	r.sessions = nil
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeClient)})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeFederation)})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeInternal)})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeVirtual)})
	r.clearInCallStats()
	return result
}

func (r *Room) processAsyncNatsMessage(msg *nats.Msg) {
	var message events.AsyncMessage
	if err := nats.Decode(msg, &message); err != nil {
		r.logger.Printf("Could not decode NATS message %+v: %s", msg, err)
		return
	}

	r.processAsyncMessage(&message)
}

func (r *Room) processAsyncMessage(message *events.AsyncMessage) {
	switch message.Type {
	case "room":
		r.processBackendRoomRequestRoom(message.Room)
	case "asyncroom":
		r.processBackendRoomRequestAsyncRoom(message.AsyncRoom)
	default:
		r.logger.Printf("Unsupported backend room request with type %s in %s: %+v", message.Type, r.id, message)
	}
}

func (r *Room) processBackendRoomRequestRoom(message *talk.BackendServerRoomRequest) {
	received := message.ReceivedTime
	if last, found := r.lastRoomRequests[message.Type]; found && last > received {
		if msg, err := json.Marshal(message); err == nil {
			r.logger.Printf("Ignore old backend room request for %s: %s", r.Id(), string(msg))
		} else {
			r.logger.Printf("Ignore old backend room request for %s: %+v", r.Id(), message)
		}
		return
	}

	r.lastRoomRequests[message.Type] = received
	message.RoomId = r.Id()
	message.Backend = r.Backend()
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
		case talk.TransientActionSet:
			if message.Transient.TTL == 0 {
				r.doSetTransientData(message.Transient.Key, message.Transient.Value)
			} else {
				r.doSetTransientDataTTL(message.Transient.Key, message.Transient.Value, message.Transient.TTL)
			}
		case talk.TransientActionDelete:
			r.doRemoveTransientData(message.Transient.Key)
		default:
			r.logger.Printf("Unsupported transient action in room %s: %+v", r.Id(), message.Transient)
		}
	default:
		r.logger.Printf("Unsupported backend room request with type %s in %s: %+v", message.Type, r.Id(), message)
	}
}

func (r *Room) processBackendRoomRequestAsyncRoom(message *events.AsyncRoomMessage) {
	switch message.Type {
	case "sessionjoined":
		r.notifySessionJoined(message.SessionId)
		if message.ClientType == api.HelloClientTypeInternal {
			r.publishUsersChangedWithInternal()
		}
	default:
		r.logger.Printf("Unsupported async room request with type %s in %s: %+v", message.Type, r.Id(), message)
	}
}

func (r *Room) AddSession(session Session, sessionData json.RawMessage) {
	var roomSessionData *talk.RoomSessionData
	if len(sessionData) > 0 {
		roomSessionData = &talk.RoomSessionData{}
		if err := json.Unmarshal(sessionData, roomSessionData); err != nil {
			r.logger.Printf("Error decoding room session data \"%s\": %s", string(sessionData), err)
			roomSessionData = nil
		}
	}

	sid := session.PublicId()
	r.mu.Lock()
	isFirst := len(r.sessions) == 0
	_, found := r.sessions[sid]
	r.sessions[sid] = session
	if !found {
		r.statsRoomSessionsCurrent.With(prometheus.Labels{"clienttype": string(session.ClientType())}).Inc()
	}
	var publishUsersChanged bool
	switch session.ClientType() {
	case api.HelloClientTypeInternal:
		clientSession, ok := session.(*ClientSession)
		if !ok {
			delete(r.sessions, sid)
			r.mu.Unlock()
			panic(fmt.Sprintf("Expected a client session, got %v (%T)", session, session))
		}
		r.internalSessions[clientSession] = true
	case api.HelloClientTypeVirtual:
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
		r.logger.Printf("Session %s sent room session data %+v", session.PublicId(), roomSessionData)
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
		if isFirst {
			r.fetchInitialTransientData()
		}
	}

	// Trigger notifications that the session joined.
	if err := r.events.PublishBackendRoomMessage(r.id, r.backend, &events.AsyncMessage{
		Type: "asyncroom",
		AsyncRoom: &events.AsyncRoomMessage{
			Type:       "sessionjoined",
			SessionId:  sid,
			ClientType: session.ClientType(),
		},
	}); err != nil {
		r.logger.Printf("Error publishing joined event for session %s: %s", sid, err)
	}
}

func (r *Room) getOtherSessions(ignoreSessionId api.PublicSessionId) (Session, []Session) {
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

func (r *Room) notifySessionJoined(sessionId api.PublicSessionId) {
	session, sessions := r.getOtherSessions(sessionId)
	if len(sessions) == 0 {
		return
	}

	if session != nil && session.ClientType() != api.HelloClientTypeClient {
		session = nil
	}

	joinEvents := make([]api.EventServerMessageSessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entry := api.EventServerMessageSessionEntry{
			SessionId: s.PublicId(),
			UserId:    s.UserId(),
			User:      s.UserData(),
		}
		if s, ok := s.(*ClientSession); ok {
			entry.Features = s.GetFeatures()
			entry.RoomSessionId = s.RoomSessionId()
			entry.Federated = s.ClientType() == api.HelloClientTypeFederation
		}
		joinEvents = append(joinEvents, entry)
	}

	msg := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "room",
			Type:   "join",
			Join:   joinEvents,
		},
	}

	if err := r.events.PublishSessionMessage(sessionId, r.backend, &events.AsyncMessage{
		Type:    "message",
		Message: msg,
	}); err != nil {
		r.logger.Printf("Error publishing joined events to session %s: %s", sessionId, err)
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

		msg := &api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "participants",
				Type:   "flags",
				Flags: &api.RoomFlagsServerMessage{
					RoomId:    r.id,
					SessionId: vsess.PublicId(),
					Flags:     vsess.Flags(),
				},
			},
		}

		if err := r.events.PublishSessionMessage(sessionId, r.backend, &events.AsyncMessage{
			Type:    "message",
			Message: msg,
		}); err != nil {
			r.logger.Printf("Error publishing initial flags to session %s: %s", sessionId, err)
		}
	}
}

func (r *Room) HasSession(session Session) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, result := r.sessions[session.PublicId()]
	return result
}

func (r *Room) IsSessionInCall(session Session) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.inCallSessions[session]
}

// +checklocks:r.mu
func (r *Room) clearInCallStats() {
	r.statsCallSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeClient)})
	r.statsCallSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeFederation)})
	r.statsCallSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeInternal)})
	r.statsCallSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeVirtual)})
}

func (r *Room) addSessionToCall(session Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.addSessionToCallLocked(session)
}

// +checklocks:r.mu
func (r *Room) addSessionToCallLocked(session Session) bool {
	if r.inCallSessions[session] {
		return false
	}

	if len(r.inCallSessions) == 0 {
		r.statsCallRoomsTotal.Inc()
	}
	r.inCallSessions[session] = true
	r.statsCallSessionsCurrent.WithLabelValues(string(session.ClientType())).Inc()
	r.statsCallSessionsTotal.WithLabelValues(string(session.ClientType())).Inc()
	return true
}

func (r *Room) removeSessionFromCall(session Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.removeSessionFromCallLocked(session)
}

// +checklocks:r.mu
func (r *Room) removeSessionFromCallLocked(session Session) bool {
	if !r.inCallSessions[session] {
		return false
	}

	delete(r.inCallSessions, session)
	if len(r.inCallSessions) == 0 {
		r.clearInCallStats()
	} else {
		r.statsCallSessionsCurrent.WithLabelValues(string(session.ClientType())).Dec()
	}
	return true
}

// Returns "true" if there are still clients in the room.
func (r *Room) RemoveSession(session Session) bool {
	r.mu.Lock()
	if _, found := r.sessions[session.PublicId()]; !found {
		r.mu.Unlock()
		return true
	}

	sid := session.PublicId()
	r.statsRoomSessionsCurrent.With(prometheus.Labels{"clienttype": string(session.ClientType())}).Dec()
	delete(r.sessions, sid)
	if virtualSession, ok := session.(*VirtualSession); ok {
		delete(r.virtualSessions, virtualSession)
		// Handle case where virtual session was also sent by Nextcloud.
		users := make([]api.StringMap, 0, len(r.users))
		for _, u := range r.users {
			if value, found := api.GetStringMapString[api.PublicSessionId](u, "sessionId"); !found || value != sid {
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
	r.removeSessionFromCallLocked(session)
	delete(r.roomSessionData, sid)
	if len(r.sessions) > 0 {
		r.mu.Unlock()
		if err := r.RemoveTransientData(api.TransientSessionDataPrefix + string(sid)); err != nil {
			r.logger.Printf("Error removing transient data for session %s", sid)
		}
		r.PublishSessionLeft(session)
		return true
	}

	r.hub.removeRoom(r)
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeClient)})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeInternal)})
	r.statsRoomSessionsCurrent.Delete(prometheus.Labels{"clienttype": string(api.HelloClientTypeVirtual)})
	r.unsubscribeBackend()
	r.doClose()
	r.mu.Unlock()
	if err := r.RemoveTransientData(api.TransientSessionDataPrefix + string(sid)); err != nil {
		r.logger.Printf("Error removing transient data for session %s", sid)
	}
	// Still need to publish an event so sessions on other servers get notified.
	r.PublishSessionLeft(session)
	return false
}

func (r *Room) publish(message *api.ServerMessage) error {
	return r.events.PublishRoomMessage(r.id, r.backend, &events.AsyncMessage{
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
	message := &api.ServerMessage{
		Type: "room",
		Room: &api.RoomServerMessage{
			RoomId:     r.id,
			Properties: r.properties,
		},
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish update properties message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) GetRoomSessionData(session Session) *talk.RoomSessionData {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.roomSessionData[session.PublicId()]
}

func (r *Room) PublishSessionJoined(session Session, sessionData *talk.RoomSessionData) {
	sessionId := session.PublicId()
	if sessionId == "" {
		return
	}

	userid := session.UserId()
	if userid == "" && sessionData != nil {
		userid = sessionData.UserId
	}

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "room",
			Type:   "join",
			Join: []api.EventServerMessageSessionEntry{
				{
					SessionId: sessionId,
					UserId:    userid,
					User:      session.UserData(),
				},
			},
		},
	}
	if session, ok := session.(*ClientSession); ok {
		message.Event.Join[0].Features = session.GetFeatures()
		message.Event.Join[0].RoomSessionId = session.RoomSessionId()
		message.Event.Join[0].Federated = session.ClientType() == api.HelloClientTypeFederation
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish session joined message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) PublishSessionLeft(session Session) {
	sessionId := session.PublicId()
	if sessionId == "" {
		return
	}

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "room",
			Type:   "leave",
			Leave: []api.PublicSessionId{
				sessionId,
			},
		},
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish session left message in room %s: %s", r.Id(), err)
	}

	if session.ClientType() == api.HelloClientTypeInternal {
		r.publishUsersChangedWithInternal()
	}
}

// +checklocksread:r.mu
func (r *Room) getClusteredInternalSessionsRLocked() (internal map[api.PublicSessionId]*grpc.InternalSessionData, virtual map[api.PublicSessionId]*grpc.VirtualSessionData) {
	if r.hub.rpcClients == nil {
		return nil, nil
	}

	r.mu.RUnlock()
	defer r.mu.RLock()
	ctx := log.NewLoggerContext(context.Background(), r.logger)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, client := range r.hub.rpcClients.GetClients() {
		wg.Add(1)
		go func(c *grpc.Client) {
			defer wg.Done()

			clientInternal, clientVirtual, err := c.GetInternalSessions(ctx, r.Id(), r.Backend().Urls())
			if err != nil {
				r.logger.Printf("Received error while getting internal sessions for %s@%s from %s: %s", r.Id(), r.Backend().Id(), c.Target(), err)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if internal == nil {
				internal = make(map[api.PublicSessionId]*grpc.InternalSessionData, len(clientInternal))
			}
			maps.Copy(internal, clientInternal)
			if virtual == nil {
				virtual = make(map[api.PublicSessionId]*grpc.VirtualSessionData, len(clientVirtual))
			}
			maps.Copy(virtual, clientVirtual)
		}(client)
	}
	wg.Wait()

	return
}

func (r *Room) addInternalSessions(users []api.StringMap) []api.StringMap {
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

	skipSession := make(map[api.PublicSessionId]bool)
	for _, user := range users {
		sessionid, found := api.GetStringMapString[api.PublicSessionId](user, "sessionId")
		if !found || sessionid == "" {
			continue
		}

		if userid, found := user["userId"]; !found || userid == "" {
			if roomSessionData, found := r.roomSessionData[sessionid]; found {
				user["userId"] = roomSessionData.UserId
			} else if entry, found := clusteredVirtualSessions[sessionid]; found {
				user["virtual"] = true
				user["inCall"] = entry.GetInCall()
				skipSession[sessionid] = true
			} else {
				for session := range r.virtualSessions {
					if session.PublicId() == sessionid {
						user["virtual"] = true
						user["inCall"] = session.GetInCall()
						skipSession[sessionid] = true
						break
					}
				}
			}
		}
	}
	for session := range r.internalSessions {
		u := api.StringMap{
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
		u := api.StringMap{
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
		users = append(users, api.StringMap{
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

		users = append(users, api.StringMap{
			"inCall":    session.GetInCall(),
			"sessionId": sid,
			"lastPing":  now,
			"virtual":   true,
		})
	}
	return users
}

func (r *Room) filterPermissions(users []api.StringMap) []api.StringMap {
	for _, user := range users {
		delete(user, "permissions")
	}
	return users
}

func IsInCall(value any) (bool, bool) {
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

func (r *Room) PublishUsersInCallChanged(changed []api.StringMap, users []api.StringMap) {
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

		sessionId, found := api.GetStringMapString[api.PublicSessionId](user, "sessionId")
		if !found {
			sessionId, found = api.GetStringMapString[api.PublicSessionId](user, "sessionid")
			if !found {
				continue
			}
		}

		session := r.hub.GetSessionByPublicId(sessionId)
		if session == nil {
			continue
		}

		if inCall {
			if r.addSessionToCall(session) {
				r.logger.Printf("Session %s joined call %s", session.PublicId(), r.id)
			}
		} else {
			r.removeSessionFromCall(session)
			if clientSession, ok := session.(*ClientSession); ok {
				clientSession.LeaveCall()
			}
		}
	}

	changed = r.filterPermissions(changed)
	users = r.filterPermissions(users)

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &api.RoomEventServerMessage{
				RoomId:  r.id,
				Changed: changed,
				Users:   r.addInternalSessions(users),
			},
		},
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish incall message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) PublishUsersInCallChangedAll(inCall int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var notify []*ClientSession
	if inCall&FlagInCall != 0 {
		// All connected sessions join the call.
		var joined []api.PublicSessionId
		for _, session := range r.sessions {
			clientSession, ok := session.(*ClientSession)
			if !ok {
				continue
			}

			if session.ClientType() == api.HelloClientTypeInternal ||
				session.ClientType() == api.HelloClientTypeFederation {
				continue
			}

			if r.addSessionToCallLocked(session) {
				joined = append(joined, session.PublicId())
			}
			notify = append(notify, clientSession)
		}

		if len(joined) == 0 {
			return
		}

		r.logger.Printf("Sessions %v joined call %s", joined, r.id)
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
		clear(r.inCallSessions)
		r.clearInCallStats()
	} else {
		// All sessions already left the call, no need to notify.
		return
	}

	inCallMsg := json.RawMessage(strconv.FormatInt(int64(inCall), 10))

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &api.RoomEventServerMessage{
				RoomId: r.id,
				InCall: inCallMsg,
				All:    true,
			},
		},
	}

	for _, session := range notify {
		if !session.SendMessage(message) {
			r.logger.Printf("Could not send incall message from room %s to %s", r.Id(), session.PublicId())
		}
	}
}

func (r *Room) PublishUsersChanged(changed []api.StringMap, users []api.StringMap) {
	changed = r.filterPermissions(changed)
	users = r.filterPermissions(users)

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &api.RoomEventServerMessage{
				RoomId:  r.id,
				Changed: changed,
				Users:   r.addInternalSessions(users),
			},
		},
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish users changed message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) getParticipantsUpdateMessage(users []api.StringMap) *api.ServerMessage {
	users = r.filterPermissions(users)

	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "participants",
			Type:   "update",
			Update: &api.RoomEventServerMessage{
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
	if flags&SessionChangeFlags != 0 && session.ClientType() == api.HelloClientTypeVirtual {
		// Only notify if a virtual session has changed.
		if virtual, ok := session.(*VirtualSession); ok {
			r.publishSessionFlagsChanged(virtual)
		}
	}

	if flags&SessionChangeInCall != 0 {
		joinLeave := 0
		if session, ok := session.(SessionWithInCall); ok {
			if session.GetInCall()&FlagInCall != 0 {
				joinLeave = 1
			} else {
				joinLeave = 2
			}
		}

		if joinLeave != 0 {
			switch joinLeave {
			case 1:
				if r.addSessionToCall(session) {
					r.logger.Printf("Session %s joined call %s", session.PublicId(), r.id)
				}
			case 2:
				r.removeSessionFromCall(session)
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
		r.logger.Printf("Could not publish users changed message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) publishSessionFlagsChanged(session *VirtualSession) {
	message := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "participants",
			Type:   "flags",
			Flags: &api.RoomFlagsServerMessage{
				RoomId:    r.id,
				SessionId: session.PublicId(),
				Flags:     session.Flags(),
			},
		},
	}
	if err := r.publish(message); err != nil {
		r.logger.Printf("Could not publish flags changed message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) publishActiveSessions() (int, *sync.WaitGroup) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make(map[string][]talk.BackendPingEntry)
	urls := make(map[string]*url.URL)
	for _, session := range r.sessions {
		u := session.BackendUrl()
		if u == "" {
			continue
		}

		u += PathToOcsSignalingBackend
		parsed, err := url.Parse(u)
		if err != nil {
			r.logger.Printf("Could not parse backend url %s: %s", u, err)
			continue
		}

		var changed bool
		if parsed, changed = internal.CanonicalizeUrl(parsed); changed {
			u = parsed.String()
		}

		parsedBackendUrl := parsed

		var sid api.RoomSessionId
		var uid string
		switch sess := session.(type) {
		case *ClientSession:
			// Use Nextcloud session id and user id
			sid = sess.RoomSessionId()
			uid = sess.AuthUserId()
		case *VirtualSession:
			// Use our internal generated session id (will be added to Nextcloud).
			sid = api.RoomSessionId(sess.PublicId())
			uid = sess.UserId()
		default:
			continue
		}
		if sid == "" {
			continue
		}
		e, found := entries[u]
		if !found {
			if parsedBackendUrl == nil {
				// Should not happen, invalid URLs should get rejected earlier.
				continue
			}
			urls[u] = parsedBackendUrl
		}

		entries[u] = append(e, talk.BackendPingEntry{
			SessionId: sid,
			UserId:    uid,
		})
	}
	var wg sync.WaitGroup
	if len(urls) == 0 {
		return 0, &wg
	}
	var count int
	ctx := log.NewLoggerContext(context.Background(), r.logger)
	for u, e := range entries {
		wg.Add(1)
		count += len(e)
		go func(url *url.URL, entries []talk.BackendPingEntry) {
			defer wg.Done()
			sendCtx, cancel := context.WithTimeout(ctx, r.hub.backendTimeout)
			defer cancel()

			if err := r.hub.roomPing.SendPings(sendCtx, r.id, url, entries); err != nil {
				r.logger.Printf("Error pinging room %s for active entries %+v: %s", r.id, entries, err)
			}
		}(urls[u], e)
	}
	return count, &wg
}

func (r *Room) publishRoomMessage(message *talk.BackendRoomMessageRequest) {
	if message == nil || len(message.Data) == 0 {
		return
	}

	msg := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
			Target: "room",
			Type:   "message",
			Message: &api.RoomEventMessage{
				RoomId: r.id,
				Data:   message.Data,
			},
		},
	}
	if err := r.publish(msg); err != nil {
		r.logger.Printf("Could not publish room message in room %s: %s", r.Id(), err)
	}
}

func (r *Room) publishSwitchTo(message *talk.BackendRoomSwitchToMessageRequest) {
	var wg sync.WaitGroup
	if len(message.SessionsList) > 0 {
		msg := &api.ServerMessage{
			Type: "event",
			Event: &api.EventServerMessage{
				Target: "room",
				Type:   "switchto",
				SwitchTo: &api.EventServerMessageSwitchTo{
					RoomId: message.RoomId,
				},
			},
		}

		for _, sessionId := range message.SessionsList {
			wg.Add(1)
			go func(sessionId api.PublicSessionId) {
				defer wg.Done()

				if err := r.events.PublishSessionMessage(sessionId, r.backend, &events.AsyncMessage{
					Type:    "message",
					Message: msg,
				}); err != nil {
					r.logger.Printf("Error publishing switchto event to session %s: %s", sessionId, err)
				}
			}(sessionId)
		}
	}

	if len(message.SessionsMap) > 0 {
		for sessionId, details := range message.SessionsMap {
			wg.Add(1)
			go func(sessionId api.PublicSessionId, details json.RawMessage) {
				defer wg.Done()

				msg := &api.ServerMessage{
					Type: "event",
					Event: &api.EventServerMessage{
						Target: "room",
						Type:   "switchto",
						SwitchTo: &api.EventServerMessageSwitchTo{
							RoomId:  message.RoomId,
							Details: details,
						},
					},
				}

				if err := r.events.PublishSessionMessage(sessionId, r.backend, &events.AsyncMessage{
					Type:    "message",
					Message: msg,
				}); err != nil {
					r.logger.Printf("Error publishing switchto event to session %s: %s", sessionId, err)
				}
			}(sessionId, details)
		}
	}
	wg.Wait()
}

func (r *Room) notifyInternalRoomDeleted() {
	msg := &api.ServerMessage{
		Type: "event",
		Event: &api.EventServerMessage{
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

func (r *Room) SetTransientData(key string, value any) error {
	if value == nil {
		return r.RemoveTransientData(key)
	}

	return r.events.PublishBackendRoomMessage(r.Id(), r.Backend(), &events.AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "transient",
			Transient: &talk.BackendRoomTransientRequest{
				Action: talk.TransientActionSet,
				Key:    key,
				Value:  value,
			},
		},
	})
}

func (r *Room) doSetTransientData(key string, value any) {
	r.transientData.Set(key, value)
}

func (r *Room) SetTransientDataTTL(key string, value any, ttl time.Duration) error {
	if value == nil {
		return r.RemoveTransientData(key)
	} else if ttl == 0 {
		return r.SetTransientData(key, value)
	}

	return r.events.PublishBackendRoomMessage(r.Id(), r.Backend(), &events.AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "transient",
			Transient: &talk.BackendRoomTransientRequest{
				Action: talk.TransientActionSet,
				Key:    key,
				Value:  value,
				TTL:    ttl,
			},
		},
	})
}

func (r *Room) doSetTransientDataTTL(key string, value any, ttl time.Duration) {
	r.transientData.SetTTL(key, value, ttl)
}

func (r *Room) RemoveTransientData(key string) error {
	return r.events.PublishBackendRoomMessage(r.Id(), r.Backend(), &events.AsyncMessage{
		Type: "room",
		Room: &talk.BackendServerRoomRequest{
			Type: "transient",
			Transient: &talk.BackendRoomTransientRequest{
				Action: talk.TransientActionDelete,
				Key:    key,
			},
		},
	})
}

func (r *Room) doRemoveTransientData(key string) {
	r.transientData.Remove(key)
}

func (r *Room) fetchInitialTransientData() {
	if r.hub.rpcClients == nil {
		return
	}

	ctx := log.NewLoggerContext(context.Background(), r.logger)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	// +checklocks:mu
	var initial api.TransientDataEntries
	for _, client := range r.hub.rpcClients.GetClients() {
		wg.Add(1)
		go func(c *grpc.Client) {
			defer wg.Done()

			data, err := c.GetTransientData(ctx, r.Id(), r.Backend())
			if err != nil {
				r.logger.Printf("Received error while getting transient data for %s@%s from %s: %s", r.Id(), r.Backend().Id(), c.Target(), err)
				return
			} else if len(data) == 0 {
				return
			}

			r.logger.Printf("Received initial transient data %+v from %s", data, c.Target())
			mu.Lock()
			defer mu.Unlock()
			if initial == nil {
				initial = make(api.TransientDataEntries)
			}
			maps.Copy(initial, data)
		}(client)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(initial) > 0 {
		r.transientData.SetInitial(initial)
	}
}

// Bandwidth returns information on the local streams.
func (r *Room) Bandwidth() (uint32, uint32, *sfu.ClientBandwidthInfo) {
	return r.localPublishersCount.Load(), r.localSubscribersCount.Load(), r.localBandwidth.Load()
}

func (r *Room) getLocalBandwidth() (uint32, uint32, *sfu.ClientBandwidthInfo, []SessionWithBandwidth) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var publishers uint32
	var subscribers uint32
	var bandwidth *sfu.ClientBandwidthInfo
	var publisherSessions []SessionWithBandwidth
	for _, session := range r.sessions {
		if s, ok := session.(SessionWithBandwidth); ok {
			pub, sub, bw := s.Bandwidth()
			if bw != nil {
				if bandwidth == nil {
					bandwidth = &sfu.ClientBandwidthInfo{}
				}

				bandwidth.Received += bw.Received
				bandwidth.Sent += bw.Sent
			}
			publishers += pub
			subscribers += sub
			if pub > 0 {
				publisherSessions = append(publisherSessions, s)
			}
		}
	}

	r.localPublishersCount.Store(publishers)
	r.localSubscribersCount.Store(subscribers)
	r.localBandwidth.Store(bandwidth)
	return publishers, subscribers, bandwidth, publisherSessions
}

func (r *Room) getRemoteBandwidth() (uint32, uint32, *sfu.ClientBandwidthInfo) {
	if r.hub.rpcClients == nil {
		return 0, 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup

	var publishers atomic.Uint32
	var subscribers atomic.Uint32
	var bandwidth *sfu.ClientBandwidthInfo

	for _, client := range r.hub.rpcClients.GetClients() {
		wg.Add(1)
		go func(c *grpc.Client) {
			defer wg.Done()

			pub, sub, bw, err := c.GetRoomBandwidth(ctx, r.id, r.backend.Urls())
			if err != nil {
				r.logger.Printf("Received error while getting bandwidth for %s@%s from %s: %s", r.Id(), r.Backend().Id(), c.Target(), err)
				return
			}

			publishers.Add(pub)
			subscribers.Add(sub)

			if bw != nil {
				mu.Lock()
				defer mu.Unlock()

				if bandwidth == nil {
					bandwidth = bw
				} else {
					bandwidth.Received += bw.Received
					bandwidth.Sent += bw.Sent
				}
			}
		}(client)
	}
	wg.Wait()

	return publishers.Load(), subscribers.Load(), bandwidth
}

func (r *Room) GetNextPublisherBandwidth(streamType sfu.StreamType) api.Bandwidth {
	if streamType == sfu.StreamTypeScreen {
		return r.backend.MaxScreenBitrate()
	}

	maxStreamBitrate := r.backend.MaxStreamBitrate()
	// bandwidthPerRoom is the maximum incoming bandwidth per room.
	bandwidthPerRoom := r.backend.BandwidthPerRoom()
	// minPublisherBandwidth is the minimum bandwidth per publisher.
	minPublisherBandwidth := r.backend.MinPublisherBandwidth()
	// maxPublisherBandwidth is the maximum bandwidth per publisher.
	maxPublisherBandwidth := r.backend.MaxPublisherBandwidth()
	if bandwidthPerRoom == 0 || minPublisherBandwidth == 0 || maxPublisherBandwidth == 0 {
		return maxStreamBitrate
	}

	perPublisher := api.BandwidthFromBits(bandwidthPerRoom.Bits() / max(uint64(r.allPublishersCount.Load()+1), 2))
	if maxStreamBitrate > 0 && perPublisher > maxStreamBitrate {
		perPublisher = maxStreamBitrate
	}
	perPublisher = min(maxPublisherBandwidth, perPublisher)
	perPublisher = max(minPublisherBandwidth, perPublisher)
	return perPublisher
}

func (r *Room) updateBandwidth() *sync.WaitGroup {
	var wg sync.WaitGroup
	// bandwidthPerRoom is the maximum incoming bandwidth per room.
	bandwidthPerRoom := r.backend.BandwidthPerRoom()
	// minPublisherBandwidth is the minimum bandwidth per publisher.
	minPublisherBandwidth := r.backend.MinPublisherBandwidth()
	// maxPublisherBandwidth is the maximum bandwidth per publisher.
	maxPublisherBandwidth := r.backend.MaxPublisherBandwidth()
	if bandwidthPerRoom == 0 || minPublisherBandwidth == 0 || maxPublisherBandwidth == 0 {
		if !r.bandwidthConfigured {
			return &wg
		}

		// Reset bandwidths to default.
		r.bandwidthConfigured = false
		bitrate := r.backend.MaxStreamBitrate()
		if bitrate == 0 {
			return &wg
		}

		_, _, _, publisherSessions := r.getLocalBandwidth()
		for _, session := range publisherSessions {
			wg.Add(1)
			go func() {
				defer wg.Done()

				ctx, cancel := context.WithTimeout(context.Background(), r.hub.mcuTimeout)
				defer cancel()

				if err := session.UpdatePublisherBandwidth(ctx, sfu.StreamTypeVideo, bitrate); err != nil {
					r.logger.Printf("Could not update bandwidth of %s publisher in %s: %s", sfu.StreamTypeVideo, session.PublicId(), err)
				}
			}()
		}
		return &wg
	}

	publishers, subscribers, bandwidth, publisherSessions := r.getLocalBandwidth()
	if remotePublishers, remoteSubscribers, remote := r.getRemoteBandwidth(); remote != nil {
		if bandwidth == nil {
			bandwidth = &sfu.ClientBandwidthInfo{
				Received: remote.Received,
				Sent:     remote.Sent,
			}
		} else {
			bandwidth = &sfu.ClientBandwidthInfo{
				Received: bandwidth.Received + remote.Received,
				Sent:     bandwidth.Sent + remote.Sent,
			}
		}
		publishers += remotePublishers
		subscribers += remoteSubscribers
	}

	r.allPublishersCount.Store(publishers)
	if publishers != 0 || subscribers != 0 || bandwidth != nil {
		perPublisher := api.BandwidthFromBits(bandwidthPerRoom.Bits() / max(uint64(publishers), 2))
		if maxBitrate := r.Backend().MaxStreamBitrate(); maxBitrate > 0 && perPublisher > maxBitrate {
			perPublisher = maxBitrate
		}
		perPublisher = min(maxPublisherBandwidth, perPublisher)
		perPublisher = max(minPublisherBandwidth, perPublisher)
		r.logger.Printf("Bandwidth in room %s for %d pub / %d sub: %+v (max %s)", r.Id(), publishers, subscribers, bandwidth, perPublisher)

		if perPublisher != 0 {
			r.bandwidthConfigured = true
			for _, session := range publisherSessions {
				wg.Add(1)
				go func() {
					defer wg.Done()

					ctx, cancel := context.WithTimeout(context.Background(), r.hub.mcuTimeout)
					defer cancel()

					if err := session.UpdatePublisherBandwidth(ctx, sfu.StreamTypeVideo, perPublisher); err != nil {
						r.logger.Printf("Could not update bandwidth of %s publisher in %s: %s", sfu.StreamTypeVideo, session.PublicId(), err)
					}
				}()
			}
		}
	}
	return &wg
}
