/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2019 struktur AG
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
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	FLAG_MUTED_SPEAKING  = 1
	FLAG_MUTED_LISTENING = 2
	FLAG_TALKING         = 4
)

type VirtualSession struct {
	hub       *Hub
	session   *ClientSession
	privateId string
	publicId  string
	data      *SessionIdData
	room      unsafe.Pointer

	sessionId string
	userId    string
	userData  *json.RawMessage
	flags     uint32
	options   *AddSessionOptions
}

func GetVirtualSessionId(session *ClientSession, sessionId string) string {
	return session.PublicId() + "|" + sessionId
}

func NewVirtualSession(session *ClientSession, privateId string, publicId string, data *SessionIdData, msg *AddSessionInternalClientMessage) (*VirtualSession, error) {
	result := &VirtualSession{
		hub:       session.hub,
		session:   session,
		privateId: privateId,
		publicId:  publicId,
		data:      data,

		sessionId: msg.SessionId,
		userId:    msg.UserId,
		userData:  msg.User,
		flags:     msg.Flags,
		options:   msg.Options,
	}

	if err := session.events.RegisterSessionListener(publicId, session.Backend(), result); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *VirtualSession) PrivateId() string {
	return s.privateId
}

func (s *VirtualSession) PublicId() string {
	return s.publicId
}

func (s *VirtualSession) ClientType() string {
	return HelloClientTypeVirtual
}

func (s *VirtualSession) Data() *SessionIdData {
	return s.data
}

func (s *VirtualSession) Backend() *Backend {
	return s.session.Backend()
}

func (s *VirtualSession) BackendUrl() string {
	return s.session.BackendUrl()
}

func (s *VirtualSession) ParsedBackendUrl() *url.URL {
	return s.session.ParsedBackendUrl()
}

func (s *VirtualSession) UserId() string {
	return s.userId
}

func (s *VirtualSession) UserData() *json.RawMessage {
	return s.userData
}

func (s *VirtualSession) SetRoom(room *Room) {
	atomic.StorePointer(&s.room, unsafe.Pointer(room))
}

func (s *VirtualSession) GetRoom() *Room {
	return (*Room)(atomic.LoadPointer(&s.room))
}

func (s *VirtualSession) LeaveRoom(notify bool) *Room {
	room := s.GetRoom()
	if room == nil {
		return nil
	}

	s.SetRoom(nil)
	room.RemoveSession(s)
	return room
}

func (s *VirtualSession) IsExpired(now time.Time) bool {
	return false
}

func (s *VirtualSession) Close() {
	s.CloseWithFeedback(nil, nil)
}

func (s *VirtualSession) CloseWithFeedback(session *ClientSession, message *ClientMessage) {
	room := s.GetRoom()
	s.session.RemoveVirtualSession(s)
	removed := s.session.hub.removeSession(s)
	if removed && room != nil {
		go s.notifyBackendRemoved(room, session, message)
	}
	s.session.events.UnregisterSessionListener(s.PublicId(), s.session.Backend(), s)
}

func (s *VirtualSession) notifyBackendRemoved(room *Room, session *ClientSession, message *ClientMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), s.hub.backendTimeout)
	defer cancel()

	if options := s.Options(); options != nil {
		request := NewBackendClientRoomRequest(room.Id(), s.UserId(), s.PublicId())
		request.Room.Action = "leave"
		if options != nil {
			request.Room.ActorId = options.ActorId
			request.Room.ActorType = options.ActorType
		}

		var response BackendClientResponse
		if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendUrl(), request, &response); err != nil {
			virtualSessionId := GetVirtualSessionId(s.session, s.PublicId())
			log.Printf("Could not leave virtual session %s at backend %s: %s", virtualSessionId, s.BackendUrl(), err)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(NewError("remove_failed", "Could not remove virtual session from backend."))
				session.SendMessage(reply)
			}
			return
		}

		if response.Type == "error" {
			virtualSessionId := GetVirtualSessionId(s.session, s.PublicId())
			log.Printf("Could not leave virtual session %s at backend %s: %+v", virtualSessionId, s.BackendUrl(), response.Error)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(NewError("remove_failed", response.Error.Error()))
				session.SendMessage(reply)
			}
			return
		}
	} else {
		request := NewBackendClientSessionRequest(room.Id(), "remove", s.PublicId(), &AddSessionInternalClientMessage{
			UserId: s.userId,
			User:   s.userData,
		})
		var response BackendClientSessionResponse
		err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendUrl(), request, &response)
		if err != nil {
			log.Printf("Could not remove virtual session %s from backend %s: %s", s.PublicId(), s.BackendUrl(), err)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(NewError("remove_failed", "Could not remove virtual session from backend."))
				session.SendMessage(reply)
			}
		}
	}
}

func (s *VirtualSession) HasPermission(permission Permission) bool {
	return true
}

func (s *VirtualSession) Session() *ClientSession {
	return s.session
}

func (s *VirtualSession) SessionId() string {
	return s.sessionId
}

func (s *VirtualSession) AddFlags(flags uint32) bool {
	for {
		old := atomic.LoadUint32(&s.flags)
		if old&flags == flags {
			// Flags already set.
			return false
		}
		newFlags := old | flags
		if atomic.CompareAndSwapUint32(&s.flags, old, newFlags) {
			return true
		}
		// Another thread updated the flags while we were checking, retry.
	}
}

func (s *VirtualSession) RemoveFlags(flags uint32) bool {
	for {
		old := atomic.LoadUint32(&s.flags)
		if old&flags == 0 {
			// Flags not set.
			return false
		}
		newFlags := old & ^flags
		if atomic.CompareAndSwapUint32(&s.flags, old, newFlags) {
			return true
		}
		// Another thread updated the flags while we were checking, retry.
	}
}

func (s *VirtualSession) SetFlags(flags uint32) bool {
	for {
		old := atomic.LoadUint32(&s.flags)
		if old == flags {
			return false
		}

		if atomic.CompareAndSwapUint32(&s.flags, old, flags) {
			return true
		}
	}
}

func (s *VirtualSession) Flags() uint32 {
	return atomic.LoadUint32(&s.flags)
}

func (s *VirtualSession) Options() *AddSessionOptions {
	return s.options
}

func (s *VirtualSession) ProcessAsyncSessionMessage(message *AsyncMessage) {
	if message.Type == "message" && message.Message != nil {
		switch message.Message.Type {
		case "message":
			if message.Message.Message != nil &&
				message.Message.Message.Recipient != nil &&
				message.Message.Message.Recipient.Type == "session" &&
				message.Message.Message.Recipient.SessionId == s.PublicId() {
				// The client should see his session id as recipient.
				message.Message.Message.Recipient = &MessageClientMessageRecipient{
					Type:      "session",
					SessionId: s.SessionId(),
					UserId:    s.UserId(),
				}
				s.session.ProcessAsyncSessionMessage(message)
			}
		case "control":
			if message.Message.Control != nil &&
				message.Message.Control.Recipient != nil &&
				message.Message.Control.Recipient.Type == "session" &&
				message.Message.Control.Recipient.SessionId == s.PublicId() {
				// The client should see his session id as recipient.
				message.Message.Control.Recipient = &MessageClientMessageRecipient{
					Type:      "session",
					SessionId: s.SessionId(),
					UserId:    s.UserId(),
				}
				s.session.ProcessAsyncSessionMessage(message)
			}
		}
	}
}
