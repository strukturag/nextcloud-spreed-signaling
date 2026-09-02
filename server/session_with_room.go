/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
	"sync/atomic"
	"time"
)

type sessionWithRoom struct {
	room               atomic.Pointer[Room]
	pendingCloseRoomId atomic.Pointer[string]
}

func (s *sessionWithRoom) SetRoom(room *Room, joinTime time.Time) {
	s.room.Store(room)
	if room != nil {
		s.pendingCloseRoomId.Store(nil)
	}
}

func (s *sessionWithRoom) GetRoom() *Room {
	return s.room.Load()
}

func (s *sessionWithRoom) IsInRoom(id string) bool {
	room := s.GetRoom()
	return room != nil && room.Id() == id
}

func (s *sessionWithRoom) SetPendingCloseRoom(id string) {
	s.pendingCloseRoomId.Store(&id)
}

func (s *sessionWithRoom) IsPendingCloseRoom(id string) bool {
	p := s.pendingCloseRoomId.Load()
	return p != nil && *p == id
}
