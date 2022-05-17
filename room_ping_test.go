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
)

func NewRoomPingForTest(t *testing.T) (*url.URL, *RoomPing) {
	r := mux.NewRouter()
	registerBackendHandler(t, r)

	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	config, err := getTestConfig(server)
	if err != nil {
		t.Fatal(err)
	}

	backend, err := NewBackendClient(config, 1, "0.0")
	if err != nil {
		t.Fatal(err)
	}

	p, err := NewRoomPing(backend, backend.capabilities)
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	return u, p
}

func TestSingleRoomPing(t *testing.T) {
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
	if err := ping.SendPings(ctx, room1, u, entries1); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 1 {
		t.Errorf("expected one ping request, got %+v", requests)
	} else if len(requests[0].Ping.Entries) != 1 {
		t.Errorf("expected one entry, got %+v", requests[0].Ping.Entries)
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
	if err := ping.SendPings(ctx, room2, u, entries2); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 1 {
		t.Errorf("expected one ping request, got %+v", requests)
	} else if len(requests[0].Ping.Entries) != 1 {
		t.Errorf("expected one entry, got %+v", requests[0].Ping.Entries)
	}
	clearPingRequests(t)

	ping.publishActiveSessions()
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}
}

func TestMultiRoomPing(t *testing.T) {
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
	if err := ping.SendPings(ctx, room1, u, entries1); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}

	room2 := &Room{
		id: "sample-room-2",
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	if err := ping.SendPings(ctx, room2, u, entries2); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}

	ping.publishActiveSessions()
	if requests := getPingRequests(t); len(requests) != 1 {
		t.Errorf("expected one ping request, got %+v", requests)
	} else if len(requests[0].Ping.Entries) != 2 {
		t.Errorf("expected two entries, got %+v", requests[0].Ping.Entries)
	}
}

func TestMultiRoomPing_Separate(t *testing.T) {
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
	if err := ping.SendPings(ctx, room1, u, entries1); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	if err := ping.SendPings(ctx, room1, u, entries2); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}

	ping.publishActiveSessions()
	if requests := getPingRequests(t); len(requests) != 1 {
		t.Errorf("expected one ping request, got %+v", requests)
	} else if len(requests[0].Ping.Entries) != 2 {
		t.Errorf("expected two entries, got %+v", requests[0].Ping.Entries)
	}
}

func TestMultiRoomPing_DeleteRoom(t *testing.T) {
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
	if err := ping.SendPings(ctx, room1, u, entries1); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}

	room2 := &Room{
		id: "sample-room-2",
	}
	entries2 := []BackendPingEntry{
		{
			UserId:    "bar",
			SessionId: "456",
		},
	}
	if err := ping.SendPings(ctx, room2, u, entries2); err != nil {
		t.Error(err)
	}
	if requests := getPingRequests(t); len(requests) != 0 {
		t.Errorf("expected no ping requests, got %+v", requests)
	}

	ping.DeleteRoom(room2)

	ping.publishActiveSessions()
	if requests := getPingRequests(t); len(requests) != 1 {
		t.Errorf("expected one ping request, got %+v", requests)
	} else if len(requests[0].Ping.Entries) != 1 {
		t.Errorf("expected two entries, got %+v", requests[0].Ping.Entries)
	}
}
