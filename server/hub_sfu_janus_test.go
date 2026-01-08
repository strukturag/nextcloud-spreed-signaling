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
package server

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	sfujanus "github.com/strukturag/nextcloud-spreed-signaling/sfu/janus"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
	janustest "github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/test"
)

type JanusSFU interface {
	sfu.SFU

	SetStats(stats sfujanus.Stats)
	Settings() *sfujanus.Settings
}

func newMcuJanusForTesting(t *testing.T) (JanusSFU, *janustest.JanusGateway) {
	gateway := janustest.NewJanusGateway(t)

	config := goconf.NewConfigFile()
	if strings.Contains(t.Name(), "Filter") {
		config.AddOption("mcu", "blockedcandidates", "192.0.0.0/24, 192.168.0.0/16")
	}
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	mcu, err := sfujanus.NewJanusSFUWithGateway(ctx, gateway, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	require.NoError(t, mcu.Start(ctx))
	return mcu.(JanusSFU), gateway
}

type mockJanusStats struct {
	called atomic.Bool

	mu sync.Mutex
	// +checklocks:mu
	value map[sfu.StreamType]int
}

func (s *mockJanusStats) Value(streamType sfu.StreamType) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.value[streamType]
}

func (s *mockJanusStats) IncSubscriber(streamType sfu.StreamType) {
	s.called.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.value == nil {
		s.value = make(map[sfu.StreamType]int)
	}
	s.value[streamType]++
}

func (s *mockJanusStats) DecSubscriber(streamType sfu.StreamType) {
	s.called.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.value == nil {
		s.value = make(map[sfu.StreamType]int)
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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	oldTimeout := mcu.Settings().Timeout()
	mcu.Settings().SetTimeout(100 * time.Millisecond)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "requestoffer",
		RoomType: "video",
	}))

	MustSucceed2(t, client2.RunUntilError, ctx, "processing_failed") // nolint

	mcu.Settings().SetTimeout(oldTimeout)

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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	sub := session2.GetSubscriber(hello1.Hello.SessionId, sfu.StreamTypeVideo)
	require.NotNil(sub)

	subscriber := sub.(sfujanus.Subscriber)
	handle := subscriber.JanusHandle()
	require.NotNil(handle)

	for ctx.Err() == nil {
		if handle = subscriber.JanusHandle(); handle == nil {
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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	sub := session2.GetSubscriber(hello1.Hello.SessionId, sfu.StreamTypeVideo)
	require.NotNil(sub)

	subscriber := sub.(sfujanus.Subscriber)
	handle := subscriber.JanusHandle()
	require.NotNil(handle)

	for ctx.Err() == nil {
		if handle = subscriber.JanusHandle(); handle == nil {
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
	mcu.SetStats(stats)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
