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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type DummySession struct {
	publicId api.PublicSessionId
}

func (s *DummySession) Context() context.Context {
	return context.Background()
}

func (s *DummySession) PrivateId() api.PrivateSessionId {
	return ""
}

func (s *DummySession) PublicId() api.PublicSessionId {
	return s.publicId
}

func (s *DummySession) ClientType() api.ClientType {
	return ""
}

func (s *DummySession) Data() *SessionIdData {
	return nil
}

func (s *DummySession) UserId() string {
	return ""
}

func (s *DummySession) UserData() json.RawMessage {
	return nil
}

func (s *DummySession) ParsedUserData() (api.StringMap, error) {
	return nil, nil
}

func (s *DummySession) Backend() *talk.Backend {
	return nil
}

func (s *DummySession) BackendUrl() string {
	return ""
}

func (s *DummySession) ParsedBackendUrl() *url.URL {
	return nil
}

func (s *DummySession) SetRoom(room *Room) {
}

func (s *DummySession) GetRoom() *Room {
	return nil
}

func (s *DummySession) IsInRoom(id string) bool {
	return false
}

func (s *DummySession) LeaveRoom(notify bool) *Room {
	return nil
}

func (s *DummySession) Close() {
}

func (s *DummySession) HasPermission(permission Permission) bool {
	return false
}

func (s *DummySession) SendError(e *api.Error) bool {
	return false
}

func (s *DummySession) SendMessage(message *api.ServerMessage) bool {
	return false
}

func checkSession(t *testing.T, sessions RoomSessions, sessionId api.PublicSessionId, roomSessionId api.RoomSessionId) Session {
	session := &DummySession{
		publicId: sessionId,
	}
	require.NoError(t, sessions.SetRoomSession(session, roomSessionId))
	if sid, err := sessions.GetSessionId(roomSessionId); assert.NoError(t, err) {
		assert.Equal(t, sessionId, sid)
	}
	return session
}

func testRoomSessions(t *testing.T, sessions RoomSessions) {
	assert := assert.New(t)
	if sid, err := sessions.GetSessionId("unknown"); err == nil {
		assert.Fail("Expected error about invalid room session", "got session id %s", sid)
	} else {
		assert.ErrorIs(err, ErrNoSuchRoomSession)
	}

	s1 := checkSession(t, sessions, "session1", "room1")
	s2 := checkSession(t, sessions, "session2", "room2")

	if sid, err := sessions.GetSessionId("room1"); assert.NoError(err) {
		assert.Equal(s1.PublicId(), sid)
	}

	sessions.DeleteRoomSession(s1)
	if sid, err := sessions.GetSessionId("room1"); err == nil {
		assert.Fail("Expected error about invalid room session", "got session id %s", sid)
	} else {
		assert.ErrorIs(err, ErrNoSuchRoomSession)
	}
	if sid, err := sessions.GetSessionId("room2"); assert.NoError(err) {
		assert.Equal(s2.PublicId(), sid)
	}

	assert.NoError(sessions.SetRoomSession(s1, "room-session"))
	assert.NoError(sessions.SetRoomSession(s2, "room-session"))
	sessions.DeleteRoomSession(s1)
	if sid, err := sessions.GetSessionId("room-session"); assert.NoError(err) {
		assert.Equal(s2.PublicId(), sid)
	}

	assert.NoError(sessions.SetRoomSession(s2, "room-session2"))
	if sid, err := sessions.GetSessionId("room-session"); err == nil {
		assert.Fail("Expected error about invalid room session", "got session id %s", sid)
	} else {
		assert.ErrorIs(err, ErrNoSuchRoomSession)
	}

	if sid, err := sessions.GetSessionId("room-session2"); assert.NoError(err) {
		assert.Equal(s2.PublicId(), sid)
	}
}
