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
	"net/url"
	"sync/atomic"

	"go.uber.org/zap"
)

const (
	FLAG_MUTED_SPEAKING  = 1
	FLAG_MUTED_LISTENING = 2
	FLAG_TALKING         = 4
)

type VirtualSession struct {
	log       *zap.Logger
	hub       *Hub
	session   *ClientSession
	privateId string
	publicId  string
	data      *SessionIdData
	room      atomic.Pointer[Room]

	sessionId string
	userId    string
	userData  json.RawMessage
	inCall    Flags
	flags     Flags
	options   *AddSessionOptions

	parseUserData func() (map[string]interface{}, error)
}

func GetVirtualSessionId(session Session, sessionId string) string {
	return session.PublicId() + "|" + sessionId
}

func NewVirtualSession(log *zap.Logger, session *ClientSession, privateId string, publicId string, data *SessionIdData, msg *AddSessionInternalClientMessage) (*VirtualSession, error) {
	result := &VirtualSession{
		log: log.With(
			zap.String("sessionid", publicId),
		),
		hub:       session.hub,
		session:   session,
		privateId: privateId,
		publicId:  publicId,
		data:      data,

		sessionId:     msg.SessionId,
		userId:        msg.UserId,
		userData:      msg.User,
		parseUserData: parseUserData(msg.User),
		options:       msg.Options,
	}

	if err := session.events.RegisterSessionListener(publicId, session.Backend(), result); err != nil {
		return nil, err
	}

	if msg.InCall != nil {
		result.SetInCall(*msg.InCall)
	} else if !session.HasFeature(ClientFeatureInternalInCall) {
		result.SetInCall(FlagInCall | FlagWithPhone)
	}
	if msg.Flags != 0 {
		result.SetFlags(msg.Flags)
	}

	return result, nil
}

func (s *VirtualSession) Context() context.Context {
	return s.session.Context()
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

func (s *VirtualSession) GetInCall() int {
	return int(s.inCall.Get())
}

func (s *VirtualSession) SetInCall(inCall int) bool {
	if inCall < 0 {
		inCall = 0
	}

	return s.inCall.Set(uint32(inCall))
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

func (s *VirtualSession) UserData() json.RawMessage {
	return s.userData
}

func (s *VirtualSession) ParsedUserData() (map[string]interface{}, error) {
	return s.parseUserData()
}

func (s *VirtualSession) SetRoom(room *Room) {
	s.room.Store(room)
	if room != nil {
		if err := s.hub.roomSessions.SetRoomSession(s, s.PublicId()); err != nil {
			s.log.Error("Error adding virtual room session",
				zap.Error(err),
			)
		}
	} else {
		s.hub.roomSessions.DeleteRoomSession(s)
	}
}

func (s *VirtualSession) GetRoom() *Room {
	return s.room.Load()
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

func (s *VirtualSession) Close() {
	s.CloseWithFeedback(nil, nil)
}

func (s *VirtualSession) CloseWithFeedback(session Session, message *ClientMessage) {
	room := s.GetRoom()
	s.session.RemoveVirtualSession(s)
	removed := s.session.hub.removeSession(s)
	if removed && room != nil {
		go s.notifyBackendRemoved(room, session, message)
	}
	s.session.events.UnregisterSessionListener(s.PublicId(), s.session.Backend(), s)
}

func (s *VirtualSession) notifyBackendRemoved(room *Room, session Session, message *ClientMessage) {
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
			s.log.Error("Could not leave virtual session at backend",
				zap.String("virtualsessionid", virtualSessionId),
				zap.String("backend", s.BackendUrl()),
				zap.Error(err),
			)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(NewError("remove_failed", "Could not remove virtual session from backend."))
				session.SendMessage(reply)
			}
			return
		}

		if response.Type == "error" {
			virtualSessionId := GetVirtualSessionId(s.session, s.PublicId())
			if session != nil && message != nil && (response.Error == nil || response.Error.Code != "no_such_room") {
				s.log.Error("Could not leave virtual session at backend",
					zap.String("virtualsessionid", virtualSessionId),
					zap.String("backend", s.BackendUrl()),
					zap.Any("error", response.Error),
				)
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
			s.log.Error("Could not remove virtual session from backend",
				zap.String("backend", s.BackendUrl()),
				zap.Error(err),
			)
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
	return s.flags.Add(flags)
}

func (s *VirtualSession) RemoveFlags(flags uint32) bool {
	return s.flags.Remove(flags)
}

func (s *VirtualSession) SetFlags(flags uint32) bool {
	return s.flags.Set(flags)
}

func (s *VirtualSession) Flags() uint32 {
	return s.flags.Get()
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
		case "event":
			if room := s.GetRoom(); room != nil &&
				message.Message.Event.Target == "roomlist" &&
				message.Message.Event.Type == "disinvite" &&
				message.Message.Event.Disinvite != nil &&
				message.Message.Event.Disinvite.RoomId == room.Id() {
				s.log.Info("Virtual session was disinvited from room, hanging up",
					zap.String("roomid", room.Id()),
				)
				payload := map[string]interface{}{
					"type": "hangup",
					"hangup": map[string]string{
						"reason": "disinvited",
					},
				}
				data, err := json.Marshal(payload)
				if err != nil {
					s.log.Error("could not marshal control payload",
						zap.Any("payload", payload),
						zap.Error(err),
					)
					return
				}

				s.session.ProcessAsyncSessionMessage(&AsyncMessage{
					Type:     "message",
					SendTime: message.SendTime,
					Message: &ServerMessage{
						Type: "control",
						Control: &ControlServerMessage{
							Recipient: &MessageClientMessageRecipient{
								Type:      "session",
								SessionId: s.SessionId(),
								UserId:    s.UserId(),
							},
							Data: data,
						},
					},
				})
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

func (s *VirtualSession) SendError(e *Error) bool {
	return s.session.SendError(e)
}

func (s *VirtualSession) SendMessage(message *ServerMessage) bool {
	return s.session.SendMessage(message)
}
