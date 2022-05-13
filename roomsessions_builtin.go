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
	"sync"
)

type BuiltinRoomSessions struct {
	sessionIdToRoomSession map[string]string
	roomSessionToSessionid map[string]string
	mu                     sync.RWMutex
}

func NewBuiltinRoomSessions() (RoomSessions, error) {
	return &BuiltinRoomSessions{
		sessionIdToRoomSession: make(map[string]string),
		roomSessionToSessionid: make(map[string]string),
	}, nil
}

func (r *BuiltinRoomSessions) SetRoomSession(session Session, roomSessionId string) error {
	if roomSessionId == "" {
		r.DeleteRoomSession(session)
		return nil
	}

	if sid := session.PublicId(); sid != "" {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.sessionIdToRoomSession[sid] = roomSessionId
		r.roomSessionToSessionid[roomSessionId] = sid
	}
	return nil
}

func (r *BuiltinRoomSessions) DeleteRoomSession(session Session) {
	if sid := session.PublicId(); sid != "" {
		r.mu.Lock()
		defer r.mu.Unlock()

		if roomSessionId, found := r.sessionIdToRoomSession[sid]; found {
			delete(r.sessionIdToRoomSession, sid)
			if r.roomSessionToSessionid[roomSessionId] == sid {
				delete(r.roomSessionToSessionid, roomSessionId)
			}
		}
	}
}

func (r *BuiltinRoomSessions) GetSessionId(roomSessionId string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, found := r.roomSessionToSessionid[roomSessionId]
	if !found {
		return "", ErrNoSuchRoomSession
	}

	return sid, nil
}
