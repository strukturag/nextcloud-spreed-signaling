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
	"errors"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async/events"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
	"github.com/strukturag/nextcloud-spreed-signaling/session"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

const (
	FLAG_MUTED_SPEAKING  = 1
	FLAG_MUTED_LISTENING = 2
	FLAG_TALKING         = 4
)

type VirtualSession struct {
	logger    log.Logger
	hub       *Hub
	session   *ClientSession
	privateId api.PrivateSessionId
	publicId  api.PublicSessionId
	data      *session.SessionIdData
	ctx       context.Context
	closeFunc context.CancelFunc
	room      atomic.Pointer[Room]

	sessionId api.PublicSessionId
	userId    string
	userData  json.RawMessage
	inCall    internal.Flags
	flags     internal.Flags
	options   *api.AddSessionOptions

	parseUserData func() (api.StringMap, error)

	asyncCh events.AsyncChannel
}

func GetVirtualSessionId(session Session, sessionId api.PublicSessionId) api.PublicSessionId {
	return session.PublicId() + "|" + sessionId
}

func NewVirtualSession(session *ClientSession, privateId api.PrivateSessionId, publicId api.PublicSessionId, data *session.SessionIdData, msg *api.AddSessionInternalClientMessage) (*VirtualSession, error) {
	ctx := log.NewLoggerContext(session.Context(), session.hub.logger)
	ctx, closeFunc := context.WithCancel(ctx)

	result := &VirtualSession{
		logger:    session.hub.logger,
		hub:       session.hub,
		session:   session,
		privateId: privateId,
		publicId:  publicId,
		data:      data,
		ctx:       ctx,
		closeFunc: closeFunc,

		sessionId:     msg.SessionId,
		userId:        msg.UserId,
		userData:      msg.User,
		parseUserData: parseUserData(msg.User),
		options:       msg.Options,

		asyncCh: make(events.AsyncChannel, events.DefaultAsyncChannelSize),
	}

	if err := session.events.RegisterSessionListener(publicId, session.Backend(), result); err != nil {
		return nil, err
	}

	if msg.InCall != nil {
		result.SetInCall(*msg.InCall)
	} else if !session.HasFeature(api.ClientFeatureInternalInCall) {
		result.SetInCall(FlagInCall | FlagWithPhone)
	}
	if msg.Flags != 0 {
		result.SetFlags(msg.Flags)
	}

	go result.run()
	return result, nil
}

func (s *VirtualSession) Context() context.Context {
	return s.ctx
}

func (s *VirtualSession) PrivateId() api.PrivateSessionId {
	return s.privateId
}

func (s *VirtualSession) PublicId() api.PublicSessionId {
	return s.publicId
}

func (s *VirtualSession) ClientType() api.ClientType {
	return api.HelloClientTypeVirtual
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

func (s *VirtualSession) Data() *session.SessionIdData {
	return s.data
}

func (s *VirtualSession) Backend() *talk.Backend {
	return s.session.Backend()
}

func (s *VirtualSession) BackendUrl() string {
	return s.session.BackendUrl()
}

func (s *VirtualSession) ParsedBackendUrl() *url.URL {
	return s.session.ParsedBackendUrl()
}

func (s *VirtualSession) ParsedBackendOcsUrl() *url.URL {
	return s.session.ParsedBackendOcsUrl()
}

func (s *VirtualSession) UserId() string {
	return s.userId
}

func (s *VirtualSession) UserData() json.RawMessage {
	return s.userData
}

func (s *VirtualSession) ParsedUserData() (api.StringMap, error) {
	return s.parseUserData()
}

func (s *VirtualSession) SetRoom(room *Room, joinTime time.Time) {
	s.room.Store(room)
	if room != nil {
		if err := s.hub.roomSessions.SetRoomSession(s, api.RoomSessionId(s.PublicId())); err != nil {
			s.logger.Printf("Error adding virtual room session %s: %s", s.PublicId(), err)
		}
	} else {
		s.hub.roomSessions.DeleteRoomSession(s)
	}
}

func (s *VirtualSession) GetRoom() *Room {
	return s.room.Load()
}

func (s *VirtualSession) IsInRoom(id string) bool {
	room := s.GetRoom()
	return room != nil && room.Id() == id
}

func (s *VirtualSession) LeaveRoom(notify bool) *Room {
	room := s.GetRoom()
	if room == nil {
		return nil
	}

	s.SetRoom(nil, time.Time{})
	room.RemoveSession(s)
	return room
}

func (s *VirtualSession) AsyncChannel() events.AsyncChannel {
	return s.asyncCh
}

func (s *VirtualSession) run() {
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

func (s *VirtualSession) Close() {
	s.CloseWithFeedback(nil, nil)
}

func (s *VirtualSession) CloseWithFeedback(session Session, message *api.ClientMessage) {
	s.closeFunc()

	room := s.GetRoom()
	s.session.RemoveVirtualSession(s)
	removed := s.session.hub.removeSession(s)
	if removed && room != nil {
		go s.notifyBackendRemoved(room, session, message)
	}
	if err := s.session.events.UnregisterSessionListener(s.PublicId(), s.session.Backend(), s); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		s.logger.Printf("Error unsubscribing listener for session %s: %s", s.publicId, err)
	}
}

func (s *VirtualSession) notifyBackendRemoved(room *Room, session Session, message *api.ClientMessage) {
	ctx := log.NewLoggerContext(context.Background(), s.logger)
	ctx, cancel := context.WithTimeout(ctx, s.hub.backendTimeout)
	defer cancel()

	if options := s.Options(); options != nil && options.ActorId != "" && options.ActorType != "" {
		request := talk.NewBackendClientRoomRequest(room.Id(), s.UserId(), api.RoomSessionId(s.PublicId()))
		request.Room.Action = "leave"
		request.Room.ActorId = options.ActorId
		request.Room.ActorType = options.ActorType

		var response talk.BackendClientResponse
		if err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendOcsUrl(), request, &response); err != nil {
			virtualSessionId := GetVirtualSessionId(s.session, s.PublicId())
			s.logger.Printf("Could not leave virtual session %s at backend %s: %s", virtualSessionId, s.BackendUrl(), err)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(api.NewError("remove_failed", "Could not remove virtual session from backend."))
				session.SendMessage(reply)
			}
			return
		}

		if response.Type == "error" {
			virtualSessionId := GetVirtualSessionId(s.session, s.PublicId())
			if session != nil && message != nil && (response.Error == nil || response.Error.Code != "no_such_room") {
				s.logger.Printf("Could not leave virtual session %s at backend %s: %+v", virtualSessionId, s.BackendUrl(), response.Error)
				reply := message.NewErrorServerMessage(api.NewError("remove_failed", response.Error.Error()))
				session.SendMessage(reply)
			}
			return
		}
	} else {
		request := talk.NewBackendClientSessionRequest(room.Id(), "remove", s.PublicId(), &api.AddSessionInternalClientMessage{
			UserId: s.userId,
			User:   s.userData,
		})
		var response talk.BackendClientSessionResponse
		err := s.hub.backend.PerformJSONRequest(ctx, s.ParsedBackendOcsUrl(), request, &response)
		if err != nil {
			s.logger.Printf("Could not remove virtual session %s from backend %s: %s", s.PublicId(), s.BackendUrl(), err)
			if session != nil && message != nil {
				reply := message.NewErrorServerMessage(api.NewError("remove_failed", "Could not remove virtual session from backend."))
				session.SendMessage(reply)
			}
		}
	}
}

func (s *VirtualSession) HasPermission(permission api.Permission) bool {
	return true
}

func (s *VirtualSession) Session() *ClientSession {
	return s.session
}

func (s *VirtualSession) SessionId() api.PublicSessionId {
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

func (s *VirtualSession) Options() *api.AddSessionOptions {
	return s.options
}

func (s *VirtualSession) processAsyncNatsMessage(msg *nats.Msg) {
	var message events.AsyncMessage
	if err := nats.Decode(msg, &message); err != nil {
		s.logger.Printf("Could not decode NATS message %+v: %s", msg, err)
		return
	}

	s.processAsyncMessage(&message)
}

func (s *VirtualSession) processAsyncMessage(message *events.AsyncMessage) {
	if message.Type == "message" && message.Message != nil {
		switch message.Message.Type {
		case "message":
			if message.Message.Message != nil &&
				message.Message.Message.Recipient != nil &&
				message.Message.Message.Recipient.Type == "session" &&
				message.Message.Message.Recipient.SessionId == s.PublicId() {
				// The client should see his session id as recipient.
				message.Message.Message.Recipient = &api.MessageClientMessageRecipient{
					Type:      "session",
					SessionId: s.SessionId(),
					UserId:    s.UserId(),
				}
				s.session.processAsyncMessage(message)
			}
		case "event":
			if room := s.GetRoom(); room != nil &&
				message.Message.Event.Target == "roomlist" &&
				message.Message.Event.Type == "disinvite" &&
				message.Message.Event.Disinvite != nil &&
				message.Message.Event.Disinvite.RoomId == room.Id() {
				s.logger.Printf("Virtual session %s was disinvited from room %s, hanging up", s.PublicId(), room.Id())
				payload := api.StringMap{
					"type": "hangup",
					"hangup": map[string]string{
						"reason": "disinvited",
					},
				}
				data, err := json.Marshal(payload)
				if err != nil {
					s.logger.Printf("could not marshal control payload %+v: %s", payload, err)
					return
				}

				s.session.processAsyncMessage(&events.AsyncMessage{
					Type:     "message",
					SendTime: message.SendTime,
					Message: &api.ServerMessage{
						Type: "control",
						Control: &api.ControlServerMessage{
							Recipient: &api.MessageClientMessageRecipient{
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
				message.Message.Control.Recipient = &api.MessageClientMessageRecipient{
					Type:      "session",
					SessionId: s.SessionId(),
					UserId:    s.UserId(),
				}
				s.session.processAsyncMessage(message)
			}
		}
	}
}

func (s *VirtualSession) SendError(e *api.Error) bool {
	return s.session.SendError(e)
}

func (s *VirtualSession) SendMessage(message *api.ServerMessage) bool {
	return s.session.SendMessage(message)
}
