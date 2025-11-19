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
	"errors"
	"sync"
	"sync/atomic"
)

type BuiltinRoomSessions struct {
	mu sync.RWMutex
	// +checklocks:mu
	sessionIdToRoomSession map[PublicSessionId]RoomSessionId
	// +checklocks:mu
	roomSessionToSessionid map[RoomSessionId]PublicSessionId

	clients *GrpcClients
}

func NewBuiltinRoomSessions(clients *GrpcClients) (RoomSessions, error) {
	return &BuiltinRoomSessions{
		sessionIdToRoomSession: make(map[PublicSessionId]RoomSessionId),
		roomSessionToSessionid: make(map[RoomSessionId]PublicSessionId),

		clients: clients,
	}, nil
}

func (r *BuiltinRoomSessions) SetRoomSession(session Session, roomSessionId RoomSessionId) error {
	if roomSessionId == "" {
		r.DeleteRoomSession(session)
		return nil
	}

	if sid := session.PublicId(); sid != "" {
		r.mu.Lock()
		defer r.mu.Unlock()

		if prev, found := r.sessionIdToRoomSession[sid]; found {
			if prev == roomSessionId {
				return nil
			}

			delete(r.roomSessionToSessionid, prev)
		}

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

func (r *BuiltinRoomSessions) GetSessionId(roomSessionId RoomSessionId) (PublicSessionId, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, found := r.roomSessionToSessionid[roomSessionId]
	if !found {
		return "", ErrNoSuchRoomSession
	}

	return sid, nil
}

func (r *BuiltinRoomSessions) LookupSessionId(ctx context.Context, roomSessionId RoomSessionId, disconnectReason string) (PublicSessionId, error) {
	sid, err := r.GetSessionId(roomSessionId)
	if err == nil {
		return sid, nil
	}

	if r.clients == nil {
		return "", ErrNoSuchRoomSession
	}

	clients := r.clients.GetClients()
	if len(clients) == 0 {
		return "", ErrNoSuchRoomSession
	}

	lookupctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var result atomic.Value
	logger := LoggerFromContext(ctx)
	for _, client := range clients {
		wg.Add(1)
		go func(client *GrpcClient) {
			defer wg.Done()

			sid, err := client.LookupSessionId(lookupctx, roomSessionId, disconnectReason)
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				logger.Printf("Received error while checking for room session id %s on %s: %s", roomSessionId, client.Target(), err)
				return
			} else if sid == "" {
				logger.Printf("Received empty session id for room session id %s from %s", roomSessionId, client.Target())
				return
			}

			cancel() // Cancel pending RPC calls.
			result.Store(sid)
		}(client)
	}
	wg.Wait()

	value := result.Load()
	if value == nil {
		return "", ErrNoSuchRoomSession
	}

	return value.(PublicSessionId), nil
}
