/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func NewRoomPingForTest(t *testing.T) (*url.URL, *RoomPing) {
	require := require.New(t)
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	config, err := getTestConfig(server)
	require.NoError(err)

	backend, err := NewBackendClient(config, 1, "0.0", nil)
	require.NoError(err)

	p, err := NewRoomPing(backend, backend.capabilities)
	require.NoError(err)

	u, err := url.Parse(server.URL + "/" + PathToOcsSignalingBackend)
	require.NoError(err)

	return u, p
}

func TestSingleRoomPing(t *testing.T) {
	CatchLogForTest(t)
	assert := assert.New(t)
	u, ping := NewRoomPingForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	room1 := &Room{
		id: "sample-room-1",
	}
	entries1 := []BackendPingEntry{
		{
			UserId:    "foo",
			SessionId: "123",
		},
	}
	assert.NoError(ping.SendPings(ctx, room1.Id(), u, entries1))
	if requests := getPingRequests(t); assert.Len(requests, 1) {
		assert.Len(requests[0].Ping.Entries, 1)
	}
	clearPingRequests(t)

	room2 := &Room{
		id: "sample-room-2",
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	assert.NoError(ping.SendPings(ctx, room2.Id(), u, entries2))
	if requests := getPingRequests(t); assert.Len(requests, 1) {
		assert.Len(requests[0].Ping.Entries, 1)
	}
	clearPingRequests(t)

	ping.publishActiveSessions()
	assert.Empty(getPingRequests(t))
}

func TestMultiRoomPing(t *testing.T) {
	CatchLogForTest(t)
	assert := assert.New(t)
	u, ping := NewRoomPingForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	room1 := &Room{
		id: "sample-room-1",
	}
	entries1 := []BackendPingEntry{
		{
			UserId:    "foo",
			SessionId: "123",
		},
	}
	assert.NoError(ping.SendPings(ctx, room1.Id(), u, entries1))
	assert.Empty(getPingRequests(t))

	room2 := &Room{
		id: "sample-room-2",
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	assert.NoError(ping.SendPings(ctx, room2.Id(), u, entries2))
	assert.Empty(getPingRequests(t))

	ping.publishActiveSessions()
	if requests := getPingRequests(t); assert.Len(requests, 1) {
		assert.Len(requests[0].Ping.Entries, 2)
	}
}

func TestMultiRoomPing_Separate(t *testing.T) {
	CatchLogForTest(t)
	assert := assert.New(t)
	u, ping := NewRoomPingForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	room1 := &Room{
		id: "sample-room-1",
	}
	entries1 := []BackendPingEntry{
		{
			UserId:    "foo",
			SessionId: "123",
		},
	}
	assert.NoError(ping.SendPings(ctx, room1.Id(), u, entries1))
	assert.Empty(getPingRequests(t))
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	assert.NoError(ping.SendPings(ctx, room1.Id(), u, entries2))
	assert.Empty(getPingRequests(t))

	ping.publishActiveSessions()
	if requests := getPingRequests(t); assert.Len(requests, 1) {
		assert.Len(requests[0].Ping.Entries, 2)
	}
}

func TestMultiRoomPing_DeleteRoom(t *testing.T) {
	CatchLogForTest(t)
	assert := assert.New(t)
	u, ping := NewRoomPingForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	room1 := &Room{
		id: "sample-room-1",
	}
	entries1 := []BackendPingEntry{
		{
			UserId:    "foo",
			SessionId: "123",
		},
	}
	assert.NoError(ping.SendPings(ctx, room1.Id(), u, entries1))
	assert.Empty(getPingRequests(t))

	room2 := &Room{
		id: "sample-room-2",
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	assert.NoError(ping.SendPings(ctx, room2.Id(), u, entries2))
	assert.Empty(getPingRequests(t))

	ping.DeleteRoom(room2.Id())

	ping.publishActiveSessions()
	if requests := getPingRequests(t); assert.Len(requests, 1) {
		assert.Len(requests[0].Ping.Entries, 1)
	}
}
