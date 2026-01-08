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
	"sync"
	"time"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/session"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type Session interface {
	Context() context.Context
	PrivateId() api.PrivateSessionId
	PublicId() api.PublicSessionId
	ClientType() api.ClientType
	Data() *session.SessionIdData

	UserId() string
	UserData() json.RawMessage
	ParsedUserData() (api.StringMap, error)

	Backend() *talk.Backend
	BackendUrl() string
	ParsedBackendUrl() *url.URL

	SetRoom(room *Room, joinTime time.Time)
	GetRoom() *Room
	LeaveRoom(notify bool) *Room
	IsInRoom(id string) bool

	Close()

	HasPermission(permission api.Permission) bool

	SendError(e *api.Error) bool
	SendMessage(message *api.ServerMessage) bool
}

type SessionWithInCall interface {
	GetInCall() int
}

func parseUserData(data json.RawMessage) func() (api.StringMap, error) {
	return sync.OnceValues(func() (api.StringMap, error) {
		if len(data) == 0 {
			return nil, nil
		}

		var m api.StringMap
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}

		return m, nil
	})
}
