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
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

type pingEntries struct {
	url *url.URL

	entries map[string][]BackendPingEntry
}

func newPingEntries(url *url.URL, roomId string, entries []BackendPingEntry) *pingEntries {
	return &pingEntries{
		url: url,
		entries: map[string][]BackendPingEntry{
			roomId: entries,
		},
	}
}

func (e *pingEntries) Add(roomId string, entries []BackendPingEntry) {
	if existing, found := e.entries[roomId]; found {
		e.entries[roomId] = append(existing, entries...)
	} else {
		e.entries[roomId] = entries
	}
}

func (e *pingEntries) RemoveRoom(roomId string) {
	delete(e.entries, roomId)
}

// RoomPing sends ping requests for active sessions in rooms. It evaluates the
// capabilities of the Nextcloud server to determine if sessions from different
// rooms can be grouped together.
//
// For that, all ping requests across rooms of enabled instances are combined
// and sent out batched every "updateActiveSessionsInterval" seconds.
type RoomPing struct {
	log    *zap.Logger
	mu     sync.Mutex
	closer *Closer

	backend      *BackendClient
	capabilities *Capabilities

	entries map[string]*pingEntries
}

func NewRoomPing(log *zap.Logger, backend *BackendClient, capabilities *Capabilities) (*RoomPing, error) {
	result := &RoomPing{
		log:          log,
		closer:       NewCloser(),
		backend:      backend,
		capabilities: capabilities,
	}

	return result, nil
}

func (p *RoomPing) Start() {
	go p.run()
}

func (p *RoomPing) Stop() {
	p.closer.Close()
}

func (p *RoomPing) run() {
	ticker := time.NewTicker(updateActiveSessionsInterval)
loop:
	for {
		select {
		case <-p.closer.C:
			break loop
		case <-ticker.C:
			p.publishActiveSessions()
		}
	}
}

func (p *RoomPing) getAndClearEntries() map[string]*pingEntries {
	p.mu.Lock()
	defer p.mu.Unlock()

	entries := p.entries
	p.entries = nil
	return entries
}

func (p *RoomPing) publishEntries(entries *pingEntries, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	limit, _, found := p.capabilities.GetIntegerConfig(ctx, entries.url, ConfigGroupSignaling, ConfigKeySessionPingLimit)
	if !found || limit <= 0 {
		// Limit disabled while waiting for the next iteration, fallback to sending
		// one request per room.
		for roomId, e := range entries.entries {
			ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
			defer cancel2()

			if err := p.sendPingsDirect(ctx2, roomId, entries.url, e); err != nil {
				p.log.Error("Error pinging room for active entries",
					zap.String("roomid", roomId),
					zap.Any("entries", e),
					zap.Error(err),
				)
			}
		}
		return
	}

	var allEntries []BackendPingEntry
	for _, e := range entries.entries {
		allEntries = append(allEntries, e...)
	}
	p.sendPingsCombined(entries.url, allEntries, limit, timeout)
}

func (p *RoomPing) publishActiveSessions() {
	var timeout time.Duration
	if p.backend.hub != nil {
		timeout = p.backend.hub.backendTimeout
	} else {
		// Running from tests.
		timeout = time.Second * time.Duration(defaultBackendTimeoutSeconds)
	}
	entries := p.getAndClearEntries()
	var wg sync.WaitGroup
	wg.Add(len(entries))
	for _, e := range entries {
		go func(e *pingEntries) {
			defer wg.Done()
			p.publishEntries(e, timeout)
		}(e)
	}
	wg.Wait()
}

func (p *RoomPing) sendPingsDirect(ctx context.Context, roomId string, url *url.URL, entries []BackendPingEntry) error {
	request := NewBackendClientPingRequest(roomId, entries)
	var response BackendClientResponse
	return p.backend.PerformJSONRequest(ctx, url, request, &response)
}

func (p *RoomPing) sendPingsCombined(url *url.URL, entries []BackendPingEntry, limit int, timeout time.Duration) {
	total := len(entries)
	for idx := 0; idx < total; idx += limit {
		end := idx + limit
		if end > total {
			end = total
		}
		tosend := entries[idx:end]

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		request := NewBackendClientPingRequest("", tosend)
		var response BackendClientResponse
		if err := p.backend.PerformJSONRequest(ctx, url, request, &response); err != nil {
			p.log.Error("Error sending combined ping session entries",
				zap.Any("entries", tosend),
				zap.Stringer("url", url),
				zap.Error(err),
			)
		}
	}
}

func (p *RoomPing) SendPings(ctx context.Context, roomId string, url *url.URL, entries []BackendPingEntry) error {
	limit, _, found := p.capabilities.GetIntegerConfig(ctx, url, ConfigGroupSignaling, ConfigKeySessionPingLimit)
	if !found || limit <= 0 {
		// Old-style Nextcloud or session limit not configured. Perform one request
		// per room. Don't queue to avoid sending all ping requests to old-style
		// instances at the same time but distribute across the interval.
		return p.sendPingsDirect(ctx, roomId, url, entries)
	}

	key := url.String()

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, found := p.entries[key]; found {
		existing.Add(roomId, entries)
		return nil
	}

	if p.entries == nil {
		p.entries = make(map[string]*pingEntries)
	}

	p.entries[key] = newPingEntries(url, roomId, entries)
	return nil
}

func (p *RoomPing) DeleteRoom(roomId string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, entries := range p.entries {
		entries.RemoveRoom(roomId)
	}
}
