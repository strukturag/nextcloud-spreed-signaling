/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/notedit/janus-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
)

func TestMcuJanusStats(t *testing.T) {
	t.Parallel()
	collectAndLint(t, janusMcuStats...)
}

type TestJanusHandle struct {
	id uint64

	sdp atomic.Value
}

type TestJanusRoom struct {
	id uint64

	publisher atomic.Pointer[TestJanusHandle]
}

type TestJanusHandler func(room *TestJanusRoom, body api.StringMap, jsep api.StringMap) (any, *janus.ErrorMsg)

type TestJanusGateway struct {
	t *testing.T

	sid atomic.Uint64
	tid atomic.Uint64
	hid atomic.Uint64 // +checklocksignore: Atomic
	rid atomic.Uint64 // +checklocksignore: Atomic
	mu  sync.Mutex

	// +checklocks:mu
	sessions map[uint64]*JanusSession
	// +checklocks:mu
	transactions map[uint64]*transaction
	// +checklocks:mu
	handles map[uint64]*TestJanusHandle
	// +checklocks:mu
	rooms map[uint64]*TestJanusRoom
	// +checklocks:mu
	handlers map[string]TestJanusHandler

	// +checklocks:mu
	attachCount int
	// +checklocks:mu
	joinCount int

	// +checklocks:mu
	handleRooms map[*TestJanusHandle]*TestJanusRoom
}

func NewTestJanusGateway(t *testing.T) *TestJanusGateway {
	gateway := &TestJanusGateway{
		t: t,

		sessions:     make(map[uint64]*JanusSession),
		transactions: make(map[uint64]*transaction),
		handles:      make(map[uint64]*TestJanusHandle),
		rooms:        make(map[uint64]*TestJanusRoom),
		handlers:     make(map[string]TestJanusHandler),

		handleRooms: make(map[*TestJanusHandle]*TestJanusRoom),
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

func (g *TestJanusGateway) registerHandlers(handlers map[string]TestJanusHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	maps.Copy(g.handlers, handlers)
}

func (g *TestJanusGateway) Info(ctx context.Context) (*InfoMsg, error) {
	return &InfoMsg{
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

func (g *TestJanusGateway) Create(ctx context.Context) (*JanusSession, error) {
	sid := g.sid.Add(1)
	session := &JanusSession{
		Id:      sid,
		Handles: make(map[uint64]*JanusHandle),
		gateway: g,
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions[sid] = session
	return session, nil
}

func (g *TestJanusGateway) Close() error {
	return nil
}

func (g *TestJanusGateway) simulateEvent(delay time.Duration, session *JanusSession, handle *TestJanusHandle, event any) {
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
func (g *TestJanusGateway) processMessage(session *JanusSession, handle *TestJanusHandle, body api.StringMap, jsep api.StringMap) any {
	request := body["request"].(string)
	switch request {
	case "create":
		room := &TestJanusRoom{
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
		error_code := JANUS_OK
		if body["ptype"] == "subscriber" {
			if strings.Contains(g.t.Name(), "NoSuchRoom") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM
				}
			} else if strings.Contains(g.t.Name(), "AlreadyJoined") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = JANUS_VIDEOROOM_ERROR_ALREADY_JOINED
				}
			} else if strings.Contains(g.t.Name(), "SubscriberTimeout") {
				g.joinCount++
				if g.joinCount == 1 {
					error_code = JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED
				}
			}
		}
		if error_code != JANUS_OK {
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
						"error_code": JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
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
						Code:   JANUS_VIDEOROOM_ERROR_ALREADY_PUBLISHED,
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
							"error_code": JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED,
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
					Code:   JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
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
		var room *TestJanusRoom
		if roomId, found := body["room"]; found {
			rid := roomId.(float64)
			if room = g.rooms[uint64(rid)]; room == nil {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM,
						Reason: "Room not found",
					},
				}
			}
		} else {
			if room, found = g.handleRooms[handle]; !found {
				return &janus.ErrorMsg{
					Err: janus.ErrorData{
						Code:   JANUS_VIDEOROOM_ERROR_INVALID_REQUEST,
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

func (g *TestJanusGateway) processRequest(msg api.StringMap) any {
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
				Code:   JANUS_ERROR_SESSION_NOT_FOUND,
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
						Code:   JANUS_ERROR_UNKNOWN,
						Reason: "Fail for test",
					},
				}
			}
		}

		handle := &TestJanusHandle{
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
					Code:   JANUS_ERROR_HANDLE_NOT_FOUND,
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
					Code:   JANUS_ERROR_HANDLE_NOT_FOUND,
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
						Code:   JANUS_VIDEOROOM_ERROR_INVALID_REQUEST,
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

func (g *TestJanusGateway) send(msg api.StringMap, t *transaction) (uint64, error) {
	tid := g.tid.Add(1)

	data, err := json.Marshal(msg)
	require.NoError(g.t, err)
	err = json.Unmarshal(data, &msg)
	require.NoError(g.t, err)

	go t.run()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.transactions[tid] = t

	go func() {
		result := g.processRequest(msg)
		if !assert.NotNil(g.t, result, "Unsupported request %+v", msg) {
			result = &janus.ErrorMsg{
				Err: janus.ErrorData{
					Code:   JANUS_ERROR_UNKNOWN,
					Reason: "Not implemented",
				},
			}
		}

		t.add(result)
	}()

	return tid, nil
}

func (g *TestJanusGateway) removeTransaction(id uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, found := g.transactions[id]; found {
		delete(g.transactions, id)
		t.quit()
	}
}

func (g *TestJanusGateway) removeSession(session *JanusSession) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessions, session.Id)
}

func newMcuJanusForTesting(t *testing.T) (*mcuJanus, *TestJanusGateway) {
	gateway := NewTestJanusGateway(t)

	config := goconf.NewConfigFile()
	if strings.Contains(t.Name(), "Filter") {
		config.AddOption("mcu", "blockedcandidates", "192.0.0.0/24, 192.168.0.0/16")
	}
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	mcu, err := NewMcuJanus(ctx, "", config)
	require.NoError(t, err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	mcuJanus := mcu.(*mcuJanus)
	mcuJanus.createJanusGateway = func(ctx context.Context, wsURL string, listener GatewayListener) (JanusGatewayInterface, error) {
		return gateway, nil
	}
	require.NoError(t, mcu.Start(ctx))
	return mcuJanus, gateway
}

type TestMcuListener struct {
	id api.PublicSessionId
}

func (t *TestMcuListener) PublicId() api.PublicSessionId {
	return t.id
}

func (t *TestMcuListener) OnUpdateOffer(client McuClient, offer api.StringMap) {

}

func (t *TestMcuListener) OnIceCandidate(client McuClient, candidate any) {

}

func (t *TestMcuListener) OnIceCompleted(client McuClient) {

}

func (t *TestMcuListener) SubscriberSidUpdated(subscriber McuSubscriber) {

}

func (t *TestMcuListener) PublisherClosed(publisher McuPublisher) {

}

func (t *TestMcuListener) SubscriberClosed(subscriber McuSubscriber) {

}

type TestMcuController struct {
	id api.PublicSessionId
}

func (c *TestMcuController) PublisherId() api.PublicSessionId {
	return c.id
}

func (c *TestMcuController) StartPublishing(ctx context.Context, publisher McuRemotePublisherProperties) error {
	// TODO: Check parameters?
	return nil
}

func (c *TestMcuController) StopPublishing(ctx context.Context, publisher McuRemotePublisherProperties) error {
	// TODO: Check parameters?
	return nil
}

func (c *TestMcuController) GetStreams(ctx context.Context) ([]PublisherStream, error) {
	streams := []PublisherStream{
		{
			Mid:    "0",
			Mindex: 0,
			Type:   "audio",
			Codec:  "opus",
		},
	}
	return streams, nil
}

type TestMcuInitiator struct {
	country string
}

func (i *TestMcuInitiator) Country() string {
	return i.country
}

func Test_JanusPublisherFilterOffer(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				// The SDP received by Janus will be filtered from blocked candidates.
				if sdpValue, found := jsep["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(mock.MockSdpOfferAudioOnlyNoFilter, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}

			return &janus.EventMsg{
				Jsep: api.StringMap{
					"sdp": mock.MockSdpAnswerAudioOnly,
				},
			}, nil
		},
		"trickle": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	// Send offer containing candidates that will be blocked / filtered.
	data := &api.MessageClientMessageData{
		Type: "offer",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioOnly,
		},
	}
	require.NoError(data.CheckValid())

	var wg sync.WaitGroup
	wg.Add(1)
	pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		if assert.NoError(err) {
			if sdpValue, found := m["sdp"]; assert.True(found) {
				sdpText, ok := sdpValue.(string)
				if assert.True(ok) {
					assert.Equal(mock.MockSdpAnswerAudioOnly, strings.ReplaceAll(sdpText, "\r\n", "\n"))
				}
			}
		}
	})
	wg.Wait()

	data = &api.MessageClientMessageData{
		Type: "candidate",
		Payload: api.StringMap{
			"candidate": api.StringMap{
				"candidate": "candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		assert.ErrorContains(err, "filtered")
		assert.Empty(m)
	})
	wg.Wait()

	data = &api.MessageClientMessageData{
		Type: "candidate",
		Payload: api.StringMap{
			"candidate": api.StringMap{
				"candidate": "candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		assert.NoError(err)
		assert.Empty(m)
	})
	wg.Wait()
}

func Test_JanusSubscriberFilterAnswer(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"start": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				// The SDP received by Janus will be filtered from blocked candidates.
				if sdpValue, found := jsep["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(mock.MockSdpAnswerAudioOnlyNoFilter, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}

			return &janus.EventMsg{
				Plugindata: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: api.StringMap{
						"room":      room.id,
						"started":   true,
						"videoroom": "event",
					},
				},
			}, nil
		},
		"trickle": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, StreamTypeVideo, initiator2)
	require.NoError(err)
	defer sub.Close(context.Background())

	// Send answer containing candidates that will be blocked / filtered.
	data := &api.MessageClientMessageData{
		Type: "answer",
		Payload: api.StringMap{
			"sdp": mock.MockSdpAnswerAudioOnly,
		},
	}
	require.NoError(data.CheckValid())

	var wg sync.WaitGroup
	wg.Add(1)
	sub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		if assert.NoError(err) {
			assert.Empty(m)
		}
	})
	wg.Wait()

	data = &api.MessageClientMessageData{
		Type: "candidate",
		Payload: api.StringMap{
			"candidate": api.StringMap{
				"candidate": "candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	sub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		assert.ErrorContains(err, "filtered")
		assert.Empty(m)
	})
	wg.Wait()

	data = &api.MessageClientMessageData{
		Type: "candidate",
		Payload: api.StringMap{
			"candidate": api.StringMap{
				"candidate": "candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	sub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer wg.Done()

		assert.NoError(err)
		assert.Empty(m)
	})
	wg.Wait()
}

func Test_JanusPublisherGetStreamsAudioOnly(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				if sdpValue, found := jsep["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(mock.MockSdpOfferAudioOnly, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}

			return &janus.EventMsg{
				Jsep: api.StringMap{
					"sdp": mock.MockSdpAnswerAudioOnly,
				},
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	data := &api.MessageClientMessageData{
		Type: "offer",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioOnly,
		},
	}
	require.NoError(data.CheckValid())

	done := make(chan struct{})
	pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer close(done)

		if assert.NoError(err) {
			if sdpValue, found := m["sdp"]; assert.True(found) {
				sdpText, ok := sdpValue.(string)
				if assert.True(ok) {
					assert.Equal(mock.MockSdpAnswerAudioOnly, strings.ReplaceAll(sdpText, "\r\n", "\n"))
				}
			}
		}
	})
	<-done

	if sb, ok := pub.(*mcuJanusPublisher); assert.True(ok, "expected publisher with streams support, got %T", pub) {
		if streams, err := sb.GetStreams(ctx); assert.NoError(err) {
			if assert.Len(streams, 1) {
				stream := streams[0]
				assert.Equal("audio", stream.Type)
				assert.Equal("audio", stream.Mid)
				assert.Equal(0, stream.Mindex)
				assert.False(stream.Disabled)
				assert.Equal("opus", stream.Codec)
				assert.False(stream.Stereo)
				assert.False(stream.Fec)
				assert.False(stream.Dtx)
			}
		}
	}
}

func Test_JanusPublisherGetStreamsAudioVideo(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				_, found := jsep["sdp"]
				assert.True(found)
			}

			return &janus.EventMsg{
				Jsep: api.StringMap{
					"sdp": mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	data := &api.MessageClientMessageData{
		Type: "offer",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}
	require.NoError(data.CheckValid())

	// Defer sending of offer / answer so "GetStreams" will wait.
	go func() {
		done := make(chan struct{})
		pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
			defer close(done)

			if assert.NoError(err) {
				if sdpValue, found := m["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(mock.MockSdpAnswerAudioAndVideo, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}
		})
		<-done
	}()

	if sb, ok := pub.(*mcuJanusPublisher); assert.True(ok, "expected publisher with streams support, got %T", pub) {
		if streams, err := sb.GetStreams(ctx); assert.NoError(err) {
			if assert.Len(streams, 2) {
				stream := streams[0]
				assert.Equal("audio", stream.Type)
				assert.Equal("audio", stream.Mid)
				assert.Equal(0, stream.Mindex)
				assert.False(stream.Disabled)
				assert.Equal("opus", stream.Codec)
				assert.False(stream.Stereo)
				assert.False(stream.Fec)
				assert.False(stream.Dtx)

				stream = streams[1]
				assert.Equal("video", stream.Type)
				assert.Equal("video", stream.Mid)
				assert.Equal(1, stream.Mindex)
				assert.False(stream.Disabled)
				assert.Equal("H264", stream.Codec)
				assert.Equal("4d0028", stream.ProfileH264)
			}
		}
	}
}

type mockBandwidthStats struct {
	incoming uint64
	outgoing uint64
}

func (s *mockBandwidthStats) SetBandwidth(incoming uint64, outgoing uint64) {
	s.incoming = incoming
	s.outgoing = outgoing
}

func Test_JanusPublisherSubscriber(t *testing.T) {
	t.Parallel()

	stats := &mockBandwidthStats{}
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Bandwidth for unknown handles is ignored.
	mcu.UpdateBandwidth(1234, "video", api.BandwidthFromBytes(100), api.BandwidthFromBytes(200))
	mcu.updateBandwidthStats(stats)
	assert.EqualValues(0, stats.incoming)
	assert.EqualValues(0, stats.outgoing)

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	janusPub, ok := pub.(*mcuJanusPublisher)
	require.True(ok)

	assert.Nil(mcu.Bandwidth())
	assert.Nil(janusPub.Bandwidth())
	mcu.UpdateBandwidth(janusPub.Handle(), "video", api.BandwidthFromBytes(1000), api.BandwidthFromBytes(2000))
	if bw := janusPub.Bandwidth(); assert.NotNil(bw) {
		assert.Equal(api.BandwidthFromBytes(1000), bw.Sent)
		assert.Equal(api.BandwidthFromBytes(2000), bw.Received)
	}
	if bw := mcu.Bandwidth(); assert.NotNil(bw) {
		assert.Equal(api.BandwidthFromBytes(1000), bw.Sent)
		assert.Equal(api.BandwidthFromBytes(2000), bw.Received)
	}
	mcu.updateBandwidthStats(stats)
	assert.EqualValues(2000, stats.incoming)
	assert.EqualValues(1000, stats.outgoing)

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, StreamTypeVideo, initiator2)
	require.NoError(err)
	defer sub.Close(context.Background())

	janusSub, ok := sub.(*mcuJanusSubscriber)
	require.True(ok)

	assert.Nil(janusSub.Bandwidth())
	mcu.UpdateBandwidth(janusSub.Handle(), "video", api.BandwidthFromBytes(3000), api.BandwidthFromBytes(4000))
	if bw := janusSub.Bandwidth(); assert.NotNil(bw) {
		assert.Equal(api.BandwidthFromBytes(3000), bw.Sent)
		assert.Equal(api.BandwidthFromBytes(4000), bw.Received)
	}
	if bw := mcu.Bandwidth(); assert.NotNil(bw) {
		assert.Equal(api.BandwidthFromBytes(4000), bw.Sent)
		assert.Equal(api.BandwidthFromBytes(6000), bw.Received)
	}
	assert.EqualValues(2000, stats.incoming)
	assert.EqualValues(1000, stats.outgoing)
	mcu.updateBandwidthStats(stats)
	assert.EqualValues(6000, stats.incoming)
	assert.EqualValues(4000, stats.outgoing)
}

func Test_JanusSubscriberPublisher(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	ready := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		time.Sleep(100 * time.Millisecond)
		pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
		if !assert.NoError(err) {
			return
		}

		defer func() {
			<-ready
			pub.Close(context.Background())
		}()
	}()

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, StreamTypeVideo, initiator2)
	require.NoError(err)
	defer sub.Close(context.Background())
	close(ready)
	<-done
}

func Test_JanusSubscriberRequestOffer(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	var originalOffer atomic.Value

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				if sdp, found := jsep["sdp"]; assert.True(found) {
					originalOffer.Store(strings.ReplaceAll(sdp.(string), "\r\n", "\n"))
				}
			}

			return &janus.EventMsg{
				Jsep: api.StringMap{
					"sdp": mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, StreamTypeVideo, initiator2)
	require.NoError(err)
	defer sub.Close(context.Background())

	go func() {
		data := &api.MessageClientMessageData{
			Type: "offer",
			Payload: api.StringMap{
				"sdp": mock.MockSdpOfferAudioAndVideo,
			},
		}
		if !assert.NoError(data.CheckValid()) {
			return
		}

		done := make(chan struct{})
		pub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
			defer close(done)

			if assert.NoError(err) {
				if sdpValue, found := m["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(mock.MockSdpAnswerAudioAndVideo, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}
		})
		<-done
	}()

	data := &api.MessageClientMessageData{
		Type: "requestoffer",
	}
	require.NoError(data.CheckValid())

	done := make(chan struct{})
	sub.SendMessage(ctx, &api.MessageClientMessage{}, data, func(err error, m api.StringMap) {
		defer close(done)

		if assert.NoError(err) {
			if sdpValue, found := m["sdp"]; assert.True(found) {
				sdpText, ok := sdpValue.(string)
				if assert.True(ok) {
					if sdp := originalOffer.Load(); assert.NotNil(sdp) {
						assert.Equal(sdp.(string), strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}
		}
	})
	<-done
}

func Test_JanusRemotePublisher(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	var added atomic.Int32
	var removed atomic.Int32

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"add_remote_publisher": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			assert.Nil(jsep)
			if streams := body["streams"].([]any); assert.Len(streams, 1) {
				if stream, ok := api.ConvertStringMap(streams[0]); assert.True(ok, "not a string map: %+v", streams[0]) {
					assert.Equal("0", stream["mid"])
					assert.EqualValues(0, stream["mindex"])
					assert.Equal("audio", stream["type"])
					assert.Equal("opus", stream["codec"])
				}
			}
			added.Add(1)
			return &janus.SuccessMsg{
				PluginData: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: api.StringMap{
						"id":        12345,
						"port":      10000,
						"rtcp_port": 10001,
					},
				},
			}, nil
		},
		"remove_remote_publisher": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			assert.Nil(jsep)
			removed.Add(1)
			return &janus.SuccessMsg{
				PluginData: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data:   api.StringMap{},
				},
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	listener1 := &TestMcuListener{
		id: "publisher-id",
	}

	controller := &TestMcuController{
		id: listener1.id,
	}

	pub, err := mcu.NewRemotePublisher(ctx, listener1, controller, StreamTypeVideo)
	require.NoError(err)
	defer pub.Close(context.Background())

	assert.EqualValues(1, added.Load())
	assert.EqualValues(0, removed.Load())

	listener2 := &TestMcuListener{
		id: "subscriber-id",
	}

	sub, err := mcu.NewRemoteSubscriber(ctx, listener2, pub)
	require.NoError(err)
	defer sub.Close(context.Background())

	pub.Close(context.Background())

	assert.EqualValues(1, added.Load())
	// The publisher is ref-counted, and still referenced by the subscriber.
	assert.EqualValues(0, removed.Load())

	sub.Close(context.Background())

	assert.EqualValues(1, added.Load())
	assert.EqualValues(1, removed.Load())
}

type mockJanusStats struct {
	called atomic.Bool

	mu sync.Mutex
	// +checklocks:mu
	value map[StreamType]int
}

func (s *mockJanusStats) Value(streamType StreamType) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.value[streamType]
}

func (s *mockJanusStats) IncSubscriber(streamType StreamType) {
	s.called.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.value == nil {
		s.value = make(map[StreamType]int)
	}
	s.value[streamType]++
}

func (s *mockJanusStats) DecSubscriber(streamType StreamType) {
	s.called.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.value == nil {
		s.value = make(map[StreamType]int)
	}
	s.value[streamType]--
}

func Test_JanusSubscriberNoSuchRoom(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	MustSucceed2(t, client2.RunUntilError, ctx, "processing_failed") // nolint

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)
}

func test_JanusSubscriberAlreadyJoined(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	if strings.Contains(t.Name(), "AttachError") {
		MustSucceed2(t, client2.RunUntilError, ctx, "processing_failed") // nolint

		require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
			Type:      "session",
			SessionId: hello1.Hello.SessionId,
		}, api.MessageClientMessageData{
			Type:     "requestoffer",
			RoomType: "video",
		}))
	}

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)
}

func Test_JanusSubscriberAlreadyJoined(t *testing.T) {
	t.Parallel()
	test_JanusSubscriberAlreadyJoined(t)
}

func Test_JanusSubscriberAlreadyJoinedAttachError(t *testing.T) {
	t.Parallel()
	test_JanusSubscriberAlreadyJoined(t)
}

func Test_JanusSubscriberTimeout(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	oldTimeout := mcu.settings.timeout.Swap(100 * int64(time.Millisecond))

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	MustSucceed2(t, client2.RunUntilError, ctx, "processing_failed") // nolint

	mcu.settings.timeout.Store(oldTimeout)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)
}

func Test_JanusSubscriberCloseEmptyStreams(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)

	sess2 := hub.GetSessionByPublicId(hello2.Hello.SessionId)
	require.NotNil(sess2)
	session2 := sess2.(*ClientSession)

	sub := session2.GetSubscriber(hello1.Hello.SessionId, StreamTypeVideo)
	require.NotNil(sub)

	subscriber := sub.(*mcuJanusSubscriber)
	handle := subscriber.handle.Load()
	require.NotNil(handle)

	for ctx.Err() == nil {
		if handle = subscriber.handle.Load(); handle == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.Nil(handle, "subscriber should have been closed")
}

func Test_JanusSubscriberRoomDestroyed(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)

	sess2 := hub.GetSessionByPublicId(hello2.Hello.SessionId)
	require.NotNil(sess2)
	session2 := sess2.(*ClientSession)

	sub := session2.GetSubscriber(hello1.Hello.SessionId, StreamTypeVideo)
	require.NotNil(sub)

	subscriber := sub.(*mcuJanusSubscriber)
	handle := subscriber.handle.Load()
	require.NotNil(handle)

	for ctx.Err() == nil {
		if handle = subscriber.handle.Load(); handle == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.Nil(handle, "subscriber should have been closed")
}

func Test_JanusSubscriberUpdateOffer(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	stats := &mockJanusStats{}

	t.Cleanup(func() {
		if !t.Failed() {
			assert.True(stats.called.Load(), "stats were not called")
			assert.Equal(0, stats.Value("video"))
		}
	})

	mcu, gateway := newMcuJanusForTesting(t)
	mcu.stats = stats
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.EventMsg{
				Jsep: api.StringMap{
					"type": "answer",
					"sdp":  mock.MockSdpAnswerAudioAndVideo,
				},
			}, nil
		},
	})

	hub, _, _, server := CreateHubForTest(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")
	require.NotEqual(hello1.Hello.SessionId, hello2.Hello.SessionId)
	require.NotEqual(hello1.Hello.UserId, hello2.Hello.UserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// Give message processing some time.
	time.Sleep(10 * time.Millisecond)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	WaitForUsersJoined(ctx, t, client1, hello1, client2, hello2)

	// Simulate request from the backend that sessions joined the call.
	users1 := []api.StringMap{
		{
			"sessionId": hello1.Hello.SessionId,
			"inCall":    1,
		},
		{
			"sessionId": hello2.Hello.SessionId,
			"inCall":    1,
		},
	}
	room := hub.getRoom(roomId)
	require.NotNil(room, "Could not find room %s", roomId)
	room.PublishUsersInCallChanged(users1, users1)
	checkReceiveClientEvent(ctx, t, client1, "update", nil)
	checkReceiveClientEvent(ctx, t, client2, "update", nil)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioAndVideo)

	// Test MCU will trigger an updated offer.
	client2.RunUntilOffer(ctx, mock.MockSdpOfferAudioOnly)
}
