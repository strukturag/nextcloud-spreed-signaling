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
)

type Permission string

var (
	PERMISSION_MAY_PUBLISH_MEDIA  Permission = "publish-media"
	PERMISSION_MAY_PUBLISH_AUDIO  Permission = "publish-audio"
	PERMISSION_MAY_PUBLISH_VIDEO  Permission = "publish-video"
	PERMISSION_MAY_PUBLISH_SCREEN Permission = "publish-screen"
	PERMISSION_MAY_CONTROL        Permission = "control"
	PERMISSION_TRANSIENT_DATA     Permission = "transient-data"
	PERMISSION_HIDE_DISPLAYNAMES  Permission = "hide-displaynames"

	// DefaultPermissionOverrides contains permission overrides for users where
	// no permissions have been set by the server. If a permission is not set in
	// this map, it's assumed the user has that permission.
	DefaultPermissionOverrides = map[Permission]bool{
		PERMISSION_HIDE_DISPLAYNAMES: false,
	}
)

type Session interface {
	Context() context.Context
	PrivateId() string
	PublicId() string
	ClientType() string
	Data() *SessionIdData

	UserId() string
	UserData() json.RawMessage
	ParsedUserData() (map[string]interface{}, error)

	Backend() *Backend
	BackendUrl() string
	ParsedBackendUrl() *url.URL

	SetRoom(room *Room)
	GetRoom() *Room
	LeaveRoom(notify bool) *Room

	Close()

	HasPermission(permission Permission) bool

	SendError(e *Error) bool
	SendMessage(message *ServerMessage) bool
}

func parseUserData(data json.RawMessage) func() (map[string]interface{}, error) {
	return sync.OnceValues(func() (map[string]interface{}, error) {
		if len(data) == 0 {
			return nil, nil
		}

		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}

		return m, nil
	})
}
