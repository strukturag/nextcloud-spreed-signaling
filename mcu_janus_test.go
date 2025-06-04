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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/notedit/janus-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestJanusHandle struct {
	id uint64
}

type TestJanusRoom struct {
	id uint64
}

type TestJanusHandler func(room *TestJanusRoom, body map[string]interface{}, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg)

type TestJanusGateway struct {
	t *testing.T

	sid atomic.Uint64
	tid atomic.Uint64
	hid atomic.Uint64
	rid atomic.Uint64
	mu  sync.Mutex

	sessions     map[uint64]*JanusSession
	transactions map[uint64]*transaction
	handles      map[uint64]*TestJanusHandle
	rooms        map[uint64]*TestJanusRoom
	handlers     map[string]TestJanusHandler

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
		assert.Len(gateway.sessions, 0)
		assert.Len(gateway.transactions, 0)
		assert.Len(gateway.handles, 0)
		assert.Len(gateway.rooms, 0)
		assert.Len(gateway.handleRooms, 0)
	})

	return gateway
}

func (g *TestJanusGateway) registerHandlers(handlers map[string]TestJanusHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, handler := range handlers {
		g.handlers[name] = handler
	}
}

func (g *TestJanusGateway) Info(ctx context.Context) (*InfoMsg, error) {
	return &InfoMsg{
		Name:          "TestJanus",
		Version:       1400,
		VersionString: "1.4.0",
		Author:        "struktur AG",
		DataChannels:  true,
		FullTrickle:   true,
		Plugins: map[string]janus.PluginInfo{
			pluginVideoRoom: {
				Name:          "Test VideoRoom plugin",
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

func (g *TestJanusGateway) processMessage(session *JanusSession, handle *TestJanusHandle, body map[string]interface{}, jsep map[string]interface{}) interface{} {
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
				Data: map[string]interface{}{
					"room": room.id,
				},
			},
		}
	case "join":
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

		assert.Equal(g.t, "publisher", body["ptype"])
		g.handleRooms[handle] = room
		return &janus.EventMsg{
			Session: session.Id,
			Handle:  handle.id,
			Plugindata: janus.PluginData{
				Plugin: pluginVideoRoom,
				Data: map[string]interface{}{
					"room": room.id,
				},
			},
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

		assert.EqualValues(g.t, room.id, uint64(rid))
		delete(g.rooms, uint64(rid))
		for h, r := range g.handleRooms {
			if r.id == room.id {
				delete(g.handleRooms, h)
			}
		}

		return &janus.SuccessMsg{
			PluginData: janus.PluginData{
				Plugin: pluginVideoRoom,
				Data:   map[string]interface{}{},
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
				if request == "start" {
					g.handleRooms[handle] = room
				}
			}
			return result
		}
	}

	return nil
}

func (g *TestJanusGateway) processRequest(msg map[string]interface{}) interface{} {
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

		var result interface{}
		switch method {
		case "message":
			body := msg["body"].(map[string]interface{})
			if jsep, found := msg["jsep"]; found {
				result = g.processMessage(session, handle, body, jsep.(map[string]interface{}))
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

func (g *TestJanusGateway) send(msg map[string]interface{}, t *transaction) (uint64, error) {
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
	delete(g.transactions, id)
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
	mcu, err := NewMcuJanus(context.Background(), "", config)
	require.NoError(t, err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	mcuJanus := mcu.(*mcuJanus)
	mcuJanus.createJanusGateway = func(ctx context.Context, wsURL string, listener GatewayListener) (JanusGatewayInterface, error) {
		return gateway, nil
	}
	require.NoError(t, mcu.Start(context.Background()))
	return mcuJanus, gateway
}

type TestMcuListener struct {
	id string
}

func (t *TestMcuListener) PublicId() string {
	return t.id
}

func (t *TestMcuListener) OnUpdateOffer(client McuClient, offer map[string]interface{}) {

}

func (t *TestMcuListener) OnIceCandidate(client McuClient, candidate interface{}) {

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
	id string
}

func (c *TestMcuController) PublisherId() string {
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
	CatchLogForTest(t)
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"configure": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				// The SDP received by Janus will be filtered from blocked candidates.
				if sdpValue, found := jsep["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(MockSdpOfferAudioOnlyNoFilter, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}

			return &janus.EventMsg{
				Jsep: map[string]interface{}{
					"sdp": MockSdpAnswerAudioOnly,
				},
			}, nil
		},
		"trickle": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "publisher-id"
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
	data := &MessageClientMessageData{
		Type: "offer",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioOnly,
		},
	}
	require.NoError(data.CheckValid())

	var wg sync.WaitGroup
	wg.Add(1)
	pub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		if assert.NoError(err) {
			if sdpValue, found := m["sdp"]; assert.True(found) {
				sdpText, ok := sdpValue.(string)
				if assert.True(ok) {
					assert.Equal(MockSdpAnswerAudioOnly, strings.ReplaceAll(sdpText, "\r\n", "\n"))
				}
			}
		}
	})
	wg.Wait()

	data = &MessageClientMessageData{
		Type: "candidate",
		Payload: map[string]interface{}{
			"candidate": map[string]interface{}{
				"candidate": "candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	pub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		assert.ErrorContains(err, "filtered")
		assert.Empty(m)
	})
	wg.Wait()

	data = &MessageClientMessageData{
		Type: "candidate",
		Payload: map[string]interface{}{
			"candidate": map[string]interface{}{
				"candidate": "candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	pub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		assert.NoError(err)
		assert.Empty(m)
	})
	wg.Wait()
}

func Test_JanusSubscriberFilterAnswer(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"start": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			if assert.NotNil(jsep) {
				// The SDP received by Janus will be filtered from blocked candidates.
				if sdpValue, found := jsep["sdp"]; assert.True(found) {
					sdpText, ok := sdpValue.(string)
					if assert.True(ok) {
						assert.Equal(MockSdpAnswerAudioOnlyNoFilter, strings.ReplaceAll(sdpText, "\r\n", "\n"))
					}
				}
			}

			return &janus.EventMsg{
				Plugindata: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: map[string]interface{}{
						"room":      room.id,
						"started":   true,
						"videoroom": "event",
					},
				},
			}, nil
		},
		"trickle": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "publisher-id"
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
	data := &MessageClientMessageData{
		Type: "answer",
		Payload: map[string]interface{}{
			"sdp": MockSdpAnswerAudioOnly,
		},
	}
	require.NoError(data.CheckValid())

	var wg sync.WaitGroup
	wg.Add(1)
	sub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		if assert.NoError(err) {
			assert.Empty(m)
		}
	})
	wg.Wait()

	data = &MessageClientMessageData{
		Type: "candidate",
		Payload: map[string]interface{}{
			"candidate": map[string]interface{}{
				"candidate": "candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	sub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		assert.ErrorContains(err, "filtered")
		assert.Empty(m)
	})
	wg.Wait()

	data = &MessageClientMessageData{
		Type: "candidate",
		Payload: map[string]interface{}{
			"candidate": map[string]interface{}{
				"candidate": "candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host",
			},
		},
	}
	require.NoError(data.CheckValid())
	wg.Add(1)
	sub.SendMessage(ctx, &MessageClientMessage{}, data, func(err error, m map[string]interface{}) {
		defer wg.Done()

		assert.NoError(err)
		assert.Empty(m)
	})
	wg.Wait()
}

func Test_JanusPublisherSubscriber(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	require := require.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "publisher-id"
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
}

func Test_JanusSubscriberPublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	require := require.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := "publisher-id"
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
		require.NoError(err)
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

func Test_JanusRemotePublisher(t *testing.T) {
	CatchLogForTest(t)
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)

	var added atomic.Int32
	var removed atomic.Int32

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"add_remote_publisher": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			assert.Nil(jsep)
			if streams := body["streams"].([]interface{}); assert.Len(streams, 1) {
				stream := streams[0].(map[string]interface{})
				assert.Equal("0", stream["mid"])
				assert.EqualValues(0, stream["mindex"])
				assert.Equal("audio", stream["type"])
				assert.Equal("opus", stream["codec"])
			}
			added.Add(1)
			return &janus.SuccessMsg{
				PluginData: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data: map[string]interface{}{
						"id":        12345,
						"port":      10000,
						"rtcp_port": 10001,
					},
				},
			}, nil
		},
		"remove_remote_publisher": func(room *TestJanusRoom, body, jsep map[string]interface{}) (interface{}, *janus.ErrorMsg) {
			assert.EqualValues(1, room.id)
			assert.Nil(jsep)
			removed.Add(1)
			return &janus.SuccessMsg{
				PluginData: janus.PluginData{
					Plugin: pluginVideoRoom,
					Data:   map[string]interface{}{},
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
