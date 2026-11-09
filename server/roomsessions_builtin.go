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
	"errors"
	"sync"
	"sync/atomic"

	"github.com/strukturag/nextcloud-spreed-signaling/v2/api"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/v2/log"
)

// pendingRoomSession is a room session id that is currently joining a room.
// The fields are guarded by the lock of BuiltinRoomSessions.
type pendingRoomSession struct {
	// Number of joins in progress.
	count int
	// Closed (and replaced) whenever the room session id was registered for a
	// session, and once all joins have finished.
	changed chan struct{}
}

type BuiltinRoomSessions struct {
	mu sync.RWMutex
	// +checklocks:mu
	sessionIdToRoomSession map[api.PublicSessionId]api.RoomSessionId
	// +checklocks:mu
	roomSessionToSessionid map[api.RoomSessionId]api.PublicSessionId
	// +checklocks:mu
	pending map[api.RoomSessionId]*pendingRoomSession

	clients *grpc.Clients
}

func NewBuiltinRoomSessions(clients *grpc.Clients) (RoomSessions, error) {
	return &BuiltinRoomSessions{
		sessionIdToRoomSession: make(map[api.PublicSessionId]api.RoomSessionId),
		roomSessionToSessionid: make(map[api.RoomSessionId]api.PublicSessionId),
		pending:                make(map[api.RoomSessionId]*pendingRoomSession),

		clients: clients,
	}, nil
}

func (r *BuiltinRoomSessions) StartJoin(roomSessionId api.RoomSessionId) func() {
	if roomSessionId == "" {
		return func() {}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	p, found := r.pending[roomSessionId]
	if !found {
		p = &pendingRoomSession{
			changed: make(chan struct{}),
		}
		r.pending[roomSessionId] = p
	}
	p.count++

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()

			p.count--
			if p.count == 0 {
				close(p.changed)
				delete(r.pending, roomSessionId)
			}
		})
	}
}

func (r *BuiltinRoomSessions) SetRoomSession(session Session, roomSessionId api.RoomSessionId) error {
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
		if p, found := r.pending[roomSessionId]; found {
			// Wake up lookups waiting for the join to finish.
			close(p.changed)
			p.changed = make(chan struct{})
		}
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

func (r *BuiltinRoomSessions) GetSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, found := r.roomSessionToSessionid[roomSessionId]
	if !found {
		return "", ErrNoSuchRoomSession
	}

	return sid, nil
}

func (r *BuiltinRoomSessions) WaitForSessionId(ctx context.Context, roomSessionId api.RoomSessionId) (api.PublicSessionId, error) {
	for {
		r.mu.RLock()
		sid, found := r.roomSessionToSessionid[roomSessionId]
		p := r.pending[roomSessionId]
		var changed <-chan struct{}
		if p != nil {
			changed = p.changed
		}
		r.mu.RUnlock()
		if found {
			return sid, nil
		} else if p == nil {
			return "", ErrNoSuchRoomSession
		}

		// Wait until the room session id was registered or all joins finished,
		// a new join might have started in the meantime.
		select {
		case <-changed:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (r *BuiltinRoomSessions) LookupSessionId(ctx context.Context, roomSessionId api.RoomSessionId, disconnectReason string) (api.PublicSessionId, error) {
	var sid api.PublicSessionId
	var err error
	if disconnectReason == "" {
		sid, err = r.WaitForSessionId(ctx, roomSessionId)
	} else {
		sid, err = r.GetSessionId(roomSessionId)
	}
	if err == nil {
		return sid, nil
	} else if !errors.Is(err, ErrNoSuchRoomSession) {
		return "", err
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
	var lookupErr atomic.Pointer[error]
	logger := log.LoggerFromContext(ctx)
	for _, client := range clients {
		wg.Go(func() {
			sid, err := client.LookupSessionId(lookupctx, roomSessionId, disconnectReason)
			switch {
			case errors.Is(err, context.Canceled):
				return
			case errors.Is(err, ErrNoSuchRoomSession):
				return
			case err != nil:
				logger.Printf("Received error while checking for room session id %s on %s: %s", roomSessionId, client.Target(), err)
				lookupErr.CompareAndSwap(nil, &err)
				return
			}

			cancel() // Cancel pending RPC calls.
			result.Store(sid)
		})
	}
	wg.Wait()

	value := result.Load()
	if value == nil {
		if err := lookupErr.Load(); err != nil {
			// The room session could be connected to a server that failed.
			return "", *err
		}
		return "", ErrNoSuchRoomSession
	}

	return value.(api.PublicSessionId), nil
}
