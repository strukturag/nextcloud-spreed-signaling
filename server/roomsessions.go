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
package server

import (
	"context"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/grpc"
)

var (
	ErrNoSuchRoomSession = grpc.ErrNoSuchRoomSession
)

type RoomSessions interface {
	SetRoomSession(session Session, roomSessionId api.RoomSessionId) error
	DeleteRoomSession(session Session)

	// StartJoin marks the room session id as currently joining a room. The
	// returned function must be called once the join has finished (or failed).
	StartJoin(roomSessionId api.RoomSessionId) func()

	GetSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error)
	// WaitForSessionId returns the session id for the room session id, waiting
	// for a join of the room session id that is still in progress.
	WaitForSessionId(ctx context.Context, roomSessionId api.RoomSessionId) (api.PublicSessionId, error)
	// LookupSessionId returns the session id for the room session id from this
	// or other servers. Lookups without a disconnect reason wait for joins of
	// the room session id that are still in progress. Lookups with a disconnect
	// reason never wait, they are performed while a session joins to disconnect
	// previous sessions with the same room session id.
	LookupSessionId(ctx context.Context, roomSessionId api.RoomSessionId, disconnectReason string) (api.PublicSessionId, error)
}
