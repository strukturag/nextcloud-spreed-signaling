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
package test

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
)

const (
	pluginVideoRoom = "janus.plugin.videoroom"
	eventWebsocket  = "janus.eventhandler.wsevh"
)

type JanusHandle struct {
	id uint64

	sdp atomic.Value
}

type JanusRoom struct {
	id uint64

	publisher atomic.Pointer[JanusHandle]
}

func (r *JanusRoom) Id() uint64 {
	return r.id
}

type JanusHandler func(room *JanusRoom, body api.StringMap, jsep api.StringMap) (any, *janus.ErrorMsg)

type JanusGateway struct {
	t *testing.T

	sid atomic.Uint64
	tid atomic.Uint64
	hid atomic.Uint64 // +checklocksignore: Atomic
	rid atomic.Uint64 // +checklocksignore: Atomic
	mu  sync.Mutex

	// +checklocks:mu
	sessions map[uint64]*janus.Session
	// +checklocks:mu
	transactions map[uint64]*janus.Transaction
	// +checklocks:mu
	handles map[uint64]*JanusHandle
	// +checklocks:mu
	rooms map[uint64]*JanusRoom
	// +checklocks:mu
	handlers map[string]JanusHandler

	// +checklocks:mu
	attachCount int
	// +checklocks:mu
	joinCount int

	// +checklocks:mu
	handleRooms map[*JanusHandle]*JanusRoom
}

func NewJanusGateway(t *testing.T) *JanusGateway {
	gateway := &JanusGateway{
		t: t,

		sessions:     make(map[uint64]*janus.Session),
		transactions: make(map[uint64]*janus.Transaction),
		handles:      make(map[uint64]*JanusHandle),
		rooms:        make(map[uint64]*JanusRoom),
		handlers:     make(map[string]JanusHandler),

		handleRooms: make(map[*JanusHandle]*JanusRoom),
	}

	t.Cleanup(func() {
		assert := assert.New(t)
		gateway.mu.Lock()
		defer gateway.mu.Unlock()
		assert.Empty(gateway.sessions)
		assert.Empty(gateway.transactions)
		assert.Empty(gateway.handles)
		assert.Empty(gateway.rooms)
		assert.Empty(gateway.handleRooms)
	})

	return gateway
}

func (g *JanusGateway) RegisterHandlers(handlers map[string]JanusHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	maps.Copy(g.handlers, handlers)
}

func (g *JanusGateway) Info(ctx context.Context) (*janus.InfoMsg, error) {
	return &janus.InfoMsg{
		Name:          "TestJanus",
		Version:       1400,
		VersionString: "1.4.0",
		Author:        "struktur AG",
		DataChannels:  true,
		EventHandlers: true,
		FullTrickle:   true,
		Plugins: map[string]janus.PluginInfo{
			pluginVideoRoom: {
				Name:          "Test VideoRoom plugin",
				VersionString: "0.0.0",
				Author:        "struktur AG",
			},
		},
		Events: map[string]janus.PluginInfo{
			eventWebsocket: {
				Name:          "Test Websocket events",
				VersionString: "0.0.0",
				Author:        "struktur AG",
			},
		},
	}, nil
}

func (g *JanusGateway) Create(ctx context.Context) (*janus.Session, error) {
	sid := g.sid.Add(1)
	session := janus.NewSession(sid, g)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions[sid] = session
	return session, nil
}

func (g *JanusGateway) Close() error {
	return nil
}

func (g *JanusGateway) simulateEvent(delay time.Duration, session *janus.Session, handle *JanusHandle, event any) {
	go func() {
		time.Sleep(delay)
		session.Lock()
		h, found := session.Handles[handle.id]
		session.Unlock()
		if found {
			h.Events <- event
		}
	}()
}

// +checklocks:g.mu
func (g *JanusGateway) processMessage(session *janus.Session, handle *JanusHandle, body api.StringMap, jsep api.StringMap) any {
	request := body["request"].(string)
	switch request {
	case "create":
		room := &JanusRoom{
			id: g.rid.Add(1),
		}
		g.rooms[room.id] = room

		return &janus.SuccessMsg{
			PluginData: janus.PluginData{
				Plugin: pluginVideoRoom,
				Data: api.StringMap{
					"room": room.id,
				},
			},
		}
	case "join":
		rid := body["room"].(float64)
		room := g.rooms[uint64(rid)]
		error_code := janus.JANUS_OK
		if body["ptype"] == "subscriber" {
			if strings.Contains(g.t.Name(), "NoSuchRoom") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM
				}
			} else if strings.Contains(g.t.Name(), "AlreadyJoined") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = janus.JANUS_VIDEOROOM_ERROR_ALREADY_JOINED
				}
			} else if strings.Contains(g.t.Name(), "SubscriberTimeout") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED
				}
			}
		}
		if error_code != janus.JANUS_OK {
			return &janus.EventMsg{
				Plugindata: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: api.StringMap{
						"error_code": error_code,
					},
				},
			}
		}

		if room == nil {
			return &janus.EventMsg{
				Plugindata: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: api.StringMap{
						"error_code": janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
					},
				},
			}
		}

		g.handleRooms[handle] = room
		switch body["ptype"] {
		case "publisher":
			if !assert.True(g.t, room.publisher.CompareAndSwap(nil, handle)) {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   janus.JANUS_VIDEOROOM_ERROR_ALREADY_PUBLISHED,
						Reason: "Already publisher in this room",
					},
				}
			}

			return &janus.EventMsg{
				Session: session.Id,
				Handle:  handle.id,
				Plugindata: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: api.StringMap{
						"room": room.id,
					},
				},
			}
		case "subscriber":
			publisher := room.publisher.Load()
			if publisher == nil || publisher.sdp.Load() == nil {
				return &janus.EventMsg{
					Plugindata: janus.PluginData{
						Plugin: pluginVideoRoom,
						Data: api.StringMap{
							"error_code": janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED,
						},
					},
				}
			}

			sdp := publisher.sdp.Load()

			// Simulate "connected" event for subscriber.
			g.simulateEvent(15*time.Millisecond, session, handle, &janus.WebRTCUpMsg{
				Session: session.Id,
				Handle:  handle.id,
			})

			if strings.Contains(g.t.Name(), "CloseEmptyStreams") {
				// Simulate stream update event with no active streams.
				g.simulateEvent(20*time.Millisecond, session, handle, &janus.EventMsg{
					Session: session.Id,
					Handle:  handle.id,
					Plugindata: janus.PluginData{
						Plugin: pluginVideoRoom,
						Data: api.StringMap{
							"videoroom": "updated",
							"streams": []any{
								api.StringMap{
									"type":   "audio",
									"active": false,
								},
							},
						},
					},
				})
			}

			if strings.Contains(g.t.Name(), "SubscriberRoomDestroyed") {
				// Simulate event that subscriber room has been destroyed.
				g.simulateEvent(20*time.Millisecond, session, handle, &janus.EventMsg{
					Session: session.Id,
					Handle:  handle.id,
					Plugindata: janus.PluginData{
						Plugin: pluginVideoRoom,
						Data: api.StringMap{
							"videoroom": "destroyed",
						},
					},
				})
			}

			if strings.Contains(g.t.Name(), "SubscriberUpdateOffer") {
				// Simulate event that subscriber receives new offer.
				g.simulateEvent(20*time.Millisecond, session, handle, &janus.EventMsg{
					Session: session.Id,
					Handle:  handle.id,
					Plugindata: janus.PluginData{
						Plugin: pluginVideoRoom,
						Data: api.StringMap{
							"videoroom":  "event",
							"configured": "ok",
						},
					},
					Jsep: map[string]any{
						"type": "offer",
						"sdp":  mock.MockSdpOfferAudioOnly,
					},
				})
			}

			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "offer",
					"sdp":  sdp.(string),
				},
			}
		}
	case "destroy":
		rid := body["room"].(float64)
		room := g.rooms[uint64(rid)]
		if room == nil {
			return &janus.ErrorMsg{
				Err: janus.ErrorData{
					Code:   janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
					Reason: "Room not found",
				},
			}
		}

		assert.Equal(g.t, room.id, uint64(rid))
		delete(g.rooms, uint64(rid))
		for h, r := range g.handleRooms {
			if r.id == room.id {
				delete(g.handleRooms, h)
			}
		}

		return &janus.SuccessMsg{
			PluginData: janus.PluginData{
				Plugin: pluginVideoRoom,
				Data:   api.StringMap{},
			},
		}
	default:
		var room *JanusRoom
		if roomId, found := body["room"]; found {
			rid := roomId.(float64)
			if room = g.rooms[uint64(rid)]; room == nil {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   janus.JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
						Reason: "Room not found",
					},
				}
			}
		} else {
			if room, found = g.handleRooms[handle]; !found {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   janus.JANUS_VIDEOROOM_ERROR_INVALID_REQUEST,
						Reason: "No joined to a room yet.",
					},
				}
			}
		}

		handler, found := g.handlers[request]
		if found {
			var err *janus.ErrorMsg
			result, err := handler(room, body, jsep)
			if err != nil {
				result = err
			} else {
				switch request {
				case "start":
					g.handleRooms[handle] = room
				case "configure":
					if sdp, found := jsep["sdp"]; found {
						handle.sdp.Store(sdp.(string))
						if !strings.Contains(g.t.Name(), "SubscriberTimeout") {
							// Simulate "connected" event for publisher.
							g.simulateEvent(10*time.Millisecond, session, handle, &janus.WebRTCUpMsg{
								Session: session.Id,
								Handle:  handle.id,
							})
						}
					}
				}
			}
			return result
		}
	}

	return nil
}

func (g *JanusGateway) processRequest(msg api.StringMap) any {
	method, found := msg["janus"]
	if !found {
		return nil
	}

	sid := msg["session_id"].(float64)
	g.mu.Lock()
	defer g.mu.Unlock()
	session := g.sessions[uint64(sid)]
	if session == nil {
		return &janus.ErrorMsg{
			Err: janus.ErrorData{
				Code:   janus.JANUS_ERROR_SESSION_NOT_FOUND,
				Reason: "Session not found",
			},
		}
	}

	switch method {
	case "attach":
		if strings.Contains(g.t.Name(), "AlreadyJoinedAttachError") {
			g.attachCount++
			if g.attachCount == 4 {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   janus.JANUS_ERROR_UNKNOWN,
						Reason: "Fail for test",
					},
				}
			}
		}

		handle := &JanusHandle{
			id: g.hid.Add(1),
		}

		g.handles[handle.id] = handle

		return &janus.SuccessMsg{
			Data: janus.SuccessData{
				ID: handle.id,
			},
		}
	case "detach":
		hid := msg["handle_id"].(float64)
		handle, found := g.handles[uint64(hid)]
		if found {
			delete(g.handles, handle.id)
		}
		if handle == nil {
			return &janus.ErrorMsg{
				Err: janus.ErrorData{
					Code:   janus.JANUS_ERROR_HANDLE_NOT_FOUND,
					Reason: "Handle not found",
				},
			}
		}

		return &janus.AckMsg{}
	case "destroy":
		delete(g.sessions, session.Id)
		return &janus.AckMsg{}
	case "message", "trickle":
		hid := msg["handle_id"].(float64)
		handle, found := g.handles[uint64(hid)]
		if !found {
			return &janus.ErrorMsg{
				Err: janus.ErrorData{
					Code:   janus.JANUS_ERROR_HANDLE_NOT_FOUND,
					Reason: "Handle not found",
				},
			}
		}

		var result any
		switch method {
		case "message":
			body, ok := api.ConvertStringMap(msg["body"])
			assert.True(g.t, ok, "not a string map: %+v", msg["body"])
			if jsepOb, found := msg["jsep"]; found {
				if jsep, ok := api.ConvertStringMap(jsepOb); assert.True(g.t, ok, "not a string map: %+v", jsepOb) {
					result = g.processMessage(session, handle, body, jsep)
				}
			} else {
				result = g.processMessage(session, handle, body, nil)
			}
		case "trickle":
			room, found := g.handleRooms[handle]
			if !found {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   janus.JANUS_VIDEOROOM_ERROR_INVALID_REQUEST,
						Reason: "No joined to a room yet.",
					},
				}
			}

			handler, found := g.handlers[method.(string)]
			if found {
				var err *janus.ErrorMsg
				result, err = handler(room, msg, nil)
				if err != nil {
					result = err
				}
			}
		}

		if ev, ok := result.(*janus.EventMsg); ok {
			if ev.Session == 0 {
				ev.Session = uint64(sid)
			}
			if ev.Handle == 0 {
				ev.Handle = handle.id
			}
		}
		return result
	}

	return nil
}

func (g *JanusGateway) Send(msg api.StringMap, t *janus.Transaction) (uint64, error) {
	tid := g.tid.Add(1)

	data, err := json.Marshal(msg)
	require.NoError(g.t, err)
	err = json.Unmarshal(data, &msg)
	require.NoError(g.t, err)

	go t.Run()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.transactions[tid] = t

	go func() {
		result := g.processRequest(msg)
		if !assert.NotNil(g.t, result, "Unsupported request %+v", msg) {
			result = &janus.ErrorMsg{
				Err: janus.ErrorData{
					Code:   janus.JANUS_ERROR_UNKNOWN,
					Reason: "Not implemented",
				},
			}
		}

		t.Add(result)
	}()

	return tid, nil
}

func (g *JanusGateway) RemoveTransaction(id uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, found := g.transactions[id]; found {
		delete(g.transactions, id)
		t.Quit()
	}
}

func (g *JanusGateway) RemoveSession(session *janus.Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessions, session.Id)
}
