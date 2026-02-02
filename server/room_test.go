/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	sfutest "github.com/strukturag/nextcloud-spreed-signaling/sfu/test"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

func TestRoom_InCallFlag(t *testing.T) {
	t.Parallel()
	type Testcase struct {
		Value  any
		InCall bool
		Valid  bool
	}
	tests := []Testcase{
		{nil, false, false},
		{"a", false, false},
		{true, true, true},
		{false, false, true},
		{0, false, true},
		{FlagDisconnected, false, true},
		{1, true, true},
		{FlagInCall, true, true},
		{2, false, true},
		{FlagWithAudio, false, true},
		{3, true, true},
		{FlagInCall | FlagWithAudio, true, true},
		{4, false, true},
		{FlagWithVideo, false, true},
		{5, true, true},
		{FlagInCall | FlagWithVideo, true, true},
		{1.1, true, true},
		{json.Number("1"), true, true},
		{json.Number("1.1"), false, false},
	}
	for _, test := range tests {
		inCall, ok := IsInCall(test.Value)
		if test.Valid {
			assert.True(t, ok, "%+v should be valid", test.Value)
		} else {
			assert.False(t, ok, "%+v should not be valid", test.Value)
		}
		assert.Equal(t, test.InCall, inCall, "conversion failed for %+v", test.Value)
	}
}

func TestRoom_Update(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.True(client.RunUntilJoined(ctx, hello.Hello))

	// Simulate backend request from Nextcloud to update the room.
	roomProperties := json.RawMessage("{\"foo\":\"bar\"}")
	msg := &talk.BackendServerRoomRequest{
		Type: "update",
		Update: &talk.BackendRoomUpdateRequest{
			UserIds: []string{
				testDefaultUserId,
			},
			Properties: roomProperties,
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	// The client receives a roomlist update and a changed room event. The
	// ordering is not defined because messages are sent by asynchronous event
	// handlers.
	message1, _ := client.RunUntilMessage(ctx)
	message2, _ := client.RunUntilMessage(ctx)

	if message1.Type != "event" {
		checkMessageRoomId(t, message1, roomId)
		if msg, ok := checkMessageRoomlistUpdate(t, message2); ok {
			assert.Equal(roomId, msg.RoomId)
			assert.Equal(string(roomProperties), string(msg.Properties))
		}
	} else if msg, ok := checkMessageRoomlistUpdate(t, message1); ok {
		assert.Equal(roomId, msg.RoomId)
		assert.Equal(string(roomProperties), string(msg.Properties))
		checkMessageRoomId(t, message2, roomId)
	}

	// Allow up to 100 milliseconds for asynchronous event processing.
	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

loop:
	for {
		select {
		case <-ctx2.Done():
			break loop
		default:
			// The internal room has been updated with the new properties.
			if room := hub.getRoom(roomId); room == nil {
				err = fmt.Errorf("Room %s not found in hub", roomId)
			} else if len(room.Properties()) == 0 || !bytes.Equal(room.Properties(), roomProperties) {
				err = fmt.Errorf("Expected room properties %s, got %+v", string(roomProperties), room.Properties())
			} else {
				err = nil
			}
		}
		if err == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.NoError(err)
}

func TestRoom_Delete(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.True(client.RunUntilJoined(ctx, hello.Hello))

	// Simulate backend request from Nextcloud to update the room.
	msg := &talk.BackendServerRoomRequest{
		Type: "delete",
		Delete: &talk.BackendRoomDeleteRequest{
			UserIds: []string{
				testDefaultUserId,
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(err)
	res, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data)
	require.NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res.StatusCode, "Expected successful request, got %s", string(body))

	// The client is no longer invited to the room and leaves it. The ordering
	// of messages is not defined as they get published through events and handled
	// by asynchronous channels.
	if message1, ok := client.RunUntilMessage(ctx); ok && message1.Type != "event" {
		// Ordering should be "leave room", "disinvited".
		checkMessageRoomId(t, message1, "")
		if message2, ok := client.RunUntilMessage(ctx); ok {
			checkMessageRoomlistDisinvite(t, message2)
		}
	} else {
		// Ordering should be "disinvited", "leave room".
		checkMessageRoomlistDisinvite(t, message1)
		// The connection should get closed after the "disinvited".
		// However due to the asynchronous processing, the "leave room" message might be received before.
		if message2, ok := client.RunUntilMessageOrClosed(ctx); ok && message2 != nil {
			checkMessageRoomId(t, message2, "")
			client.RunUntilClosed(ctx)
		}
	}

	// Allow up to 100 milliseconds for asynchronous event processing.
	ctx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

loop:
	for {
		select {
		case <-ctx2.Done():
			err = ctx2.Err()
			break loop
		default:
			// The internal room has been updated with the new properties.
			hub.ru.Lock()
			_, found := hub.rooms[roomId]
			hub.ru.Unlock()

			if found {
				err = fmt.Errorf("Room %s still found in hub", roomId)
			} else {
				err = nil
			}
		}
		if err == nil {
			break
		}

		time.Sleep(time.Millisecond)
	}

	assert.NoError(err)
}

func TestRoom_RoomJoinFeatures(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	features := []string{"one", "two", "three"}
	require.NoError(client.SendHelloClientWithFeatures(testDefaultUserId, features))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	hello := MustSucceed1(t, client.RunUntilHello, ctx)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	if message, ok := client.RunUntilMessage(ctx); ok {
		if client.checkMessageJoinedSession(message, hello.Hello.SessionId, testDefaultUserId) {
			assert.EqualValues(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId), message.Event.Join[0].RoomSessionId)
			assert.Equal(features, message.Event.Join[0].Features)
		}
	}
}

func TestRoom_RoomSessionData(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, authAnonymousUserId)

	// Join room by id.
	roomId := "test-room-with-sessiondata"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event with the userid from the room session data.
	expected := "userid-from-sessiondata"
	if message, ok := client.RunUntilMessage(ctx); ok {
		if client.checkMessageJoinedSession(message, hello.Hello.SessionId, expected) {
			assert.EqualValues(fmt.Sprintf("%s-%s", roomId, hello.Hello.SessionId), message.Event.Join[0].RoomSessionId)
		}
	}

	session := hub.GetSessionByPublicId(hello.Hello.SessionId)
	require.NotNil(session, "Could not find session %s", hello.Hello.SessionId)
	assert.Equal(expected, session.UserId())

	room := hub.getRoom(roomId)
	assert.NotNil(room, "Room not found")

	entries, wg := room.publishActiveSessions()
	assert.Equal(1, entries)
	wg.Wait()
}

func TestRoom_InCall(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	client1.RunUntilJoined(ctx, hello2.Hello)

	msg1 := &talk.BackendServerRoomRequest{
		Type: "incall",
		InCall: &talk.BackendRoomInCallRequest{
			InCall: json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
			Changed: []api.StringMap{
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
				},
			},
			Users: []api.StringMap{
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
				},
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello2.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(0, 10)),
				},
			},
		},
	}

	data1, err := json.Marshal(msg1)
	require.NoError(err)
	res1, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data1)
	require.NoError(err)
	defer res1.Body.Close()
	body1, err := io.ReadAll(res1.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res1.StatusCode, "Expected successful request, got %s", string(body1))

	if msg, ok := client1.RunUntilMessage(ctx); ok {
		if message, ok := checkMessageParticipantsInCall(t, msg); ok {
			assert.Equal(roomId, message.RoomId)
			if assert.Len(message.Users, 2) {
				assert.EqualValues(hello1.Hello.SessionId, message.Users[0]["sessionId"])
				assert.EqualValues(FlagInCall, message.Users[0]["inCall"])
				assert.EqualValues(hello2.Hello.SessionId, message.Users[1]["sessionId"])
				assert.EqualValues(0, message.Users[1]["inCall"])
			}
		}
	}

	if msg, ok := client2.RunUntilMessage(ctx); ok {
		if message, ok := checkMessageParticipantsInCall(t, msg); ok {
			assert.Equal(roomId, message.RoomId)
			if assert.Len(message.Users, 2) {
				assert.EqualValues(hello1.Hello.SessionId, message.Users[0]["sessionId"])
				assert.EqualValues(FlagInCall, message.Users[0]["inCall"])
				assert.EqualValues(hello2.Hello.SessionId, message.Users[1]["sessionId"])
				assert.EqualValues(0, message.Users[1]["inCall"])
			}
		}
	}

	msg2 := &talk.BackendServerRoomRequest{
		Type: "incall",
		InCall: &talk.BackendRoomInCallRequest{
			InCall: json.RawMessage(strconv.FormatInt(0, 10)),
			Changed: []api.StringMap{
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(0, 10)),
				},
			},
			Users: []api.StringMap{
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello1.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(0, 10)),
				},
				{
					"sessionId": fmt.Sprintf("%s-%s", roomId, hello2.Hello.SessionId),
					"inCall":    json.RawMessage(strconv.FormatInt(0, 10)),
				},
			},
		},
	}

	data2, err := json.Marshal(msg2)
	require.NoError(err)
	res2, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data2)
	require.NoError(err)
	defer res2.Body.Close()
	body2, err := io.ReadAll(res2.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res2.StatusCode, "Expected successful request, got %s", string(body2))

	if msg, ok := client1.RunUntilMessage(ctx); ok {
		if message, ok := checkMessageParticipantsInCall(t, msg); ok {
			assert.Equal(roomId, message.RoomId)
			if assert.Len(message.Users, 2) {
				assert.EqualValues(hello1.Hello.SessionId, message.Users[0]["sessionId"])
				assert.EqualValues(0, message.Users[0]["inCall"])
				assert.EqualValues(hello2.Hello.SessionId, message.Users[1]["sessionId"])
				assert.EqualValues(0, message.Users[1]["inCall"])
			}
		}
	}

	if msg, ok := client2.RunUntilMessage(ctx); ok {
		if message, ok := checkMessageParticipantsInCall(t, msg); ok {
			assert.Equal(roomId, message.RoomId)
			if assert.Len(message.Users, 2) {
				assert.EqualValues(hello1.Hello.SessionId, message.Users[0]["sessionId"])
				assert.EqualValues(0, message.Users[0]["inCall"])
				assert.EqualValues(hello2.Hello.SessionId, message.Users[1]["sessionId"])
				assert.EqualValues(0, message.Users[1]["inCall"])
			}
		}
	}
}

func TestRoom_InCallAll(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client1.RunUntilJoined(ctx, hello1.Hello)

	roomMsg = MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	client1.RunUntilJoined(ctx, hello2.Hello)

	// Simulate backend request from Nextcloud to update the "inCall" flag of all participants.
	msg1 := &talk.BackendServerRoomRequest{
		Type: "incall",
		InCall: &talk.BackendRoomInCallRequest{
			All:    true,
			InCall: json.RawMessage(strconv.FormatInt(FlagInCall, 10)),
		},
	}

	data1, err := json.Marshal(msg1)
	require.NoError(err)
	res1, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data1)
	require.NoError(err)
	defer res1.Body.Close()
	body1, err := io.ReadAll(res1.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res1.StatusCode, "Expected successful request, got %s", string(body1))

	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageInCallAll(t, msg, roomId, FlagInCall)
	}

	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageInCallAll(t, msg, roomId, FlagInCall)
	}

	// Simulate backend request from Nextcloud to update the "inCall" flag of all participants.
	msg2 := &talk.BackendServerRoomRequest{
		Type: "incall",
		InCall: &talk.BackendRoomInCallRequest{
			All:    true,
			InCall: json.RawMessage(strconv.FormatInt(0, 10)),
		},
	}

	data2, err := json.Marshal(msg2)
	require.NoError(err)
	res2, err := performBackendRequest(server.URL+"/api/v1/room/"+roomId, data2)
	require.NoError(err)
	defer res2.Body.Close()
	body2, err := io.ReadAll(res2.Body)
	assert.NoError(err)
	assert.Equal(http.StatusOK, res2.StatusCode, "Expected successful request, got %s", string(body2))

	if msg, ok := client1.RunUntilMessage(ctx); ok {
		checkMessageInCallAll(t, msg, roomId, 0)
	}

	if msg, ok := client2.RunUntilMessage(ctx); ok {
		checkMessageInCallAll(t, msg, roomId, 0)
	}
}

func TestRoom_BandwidthNoPublishers(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, router, server := CreateHubForTest(t)

	config, err := getTestConfig(server)
	require.NoError(err)
	b, err := NewBackendServer(ctx, config, hub, "no-version")
	require.NoError(err)
	require.NoError(b.Start(router))

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	client, hello1 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello1.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)

	room.updateBandwidth().Wait()
	pubs, subs, bw := room.Bandwidth()
	assert.EqualValues(0, pubs)
	assert.EqualValues(0, subs)
	assert.Nil(bw)
}

func TestRoom_LowBandwidthNoPublisher(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTestWithConfig(t, func(s *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(s)
		if err != nil {
			return nil, err
		}

		config.AddOption("backend", "maxstreambitrate", "700000")
		config.AddOption("backend", "maxscreenbitrate", "1234567")

		config.AddOption("backend", "bitrateperroom", "1000000")
		config.AddOption("backend", "minpublisherbitrate", "10000")
		config.AddOption("backend", "maxpublisherbitrate", "500000")
		return config, err
	})

	mcu := sfutest.NewSFU(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)

	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub := mcu.GetPublisher(hello.Hello.SessionId)
	require.NotNil(pub)

	// A publisher must send at least 64 Kbit/s to be counted as "active".
	pub.SetBandwidthInfo(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(60000),
	})

	room.updateBandwidth().Wait()
	pubs, subs, bw := room.Bandwidth()
	assert.EqualValues(0, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(60000),
	}, bw)

	assert.EqualValues(0, pub.GetBandwidth())
	assert.EqualValues(1234567, room.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(500000, room.GetNextPublisherBandwidth(sfu.StreamTypeVideo))
}

func TestRoom_Bandwidth(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTestWithConfig(t, func(s *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(s)
		if err != nil {
			return nil, err
		}

		config.AddOption("backend", "maxstreambitrate", "700000")
		config.AddOption("backend", "maxscreenbitrate", "1234567")

		config.AddOption("backend", "bitrateperroom", "1000000")
		config.AddOption("backend", "minpublisherbitrate", "10000")
		config.AddOption("backend", "maxpublisherbitrate", "500000")
		return config, err
	})

	mcu := sfutest.NewSFU(t)
	hub.SetMcu(mcu)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"1")

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	client.RunUntilJoined(ctx, hello.Hello)

	room := hub.getRoom(roomId)
	require.NotNil(room)

	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub := mcu.GetPublisher(hello.Hello.SessionId)
	require.NotNil(pub)

	pub.SetBandwidthInfo(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(2000),
		Received: api.BandwidthFromBits(100000),
	})

	room.updateBandwidth().Wait()
	pubs, subs, bw := room.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(2000),
		Received: api.BandwidthFromBits(100000),
	}, bw)

	assert.EqualValues(500000, pub.GetBandwidth())
	assert.EqualValues(1234567, room.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(500000, room.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	// Clearing the per-room limit will reset the bandwidth of publishers.
	room.Backend().SetBandwidthPerRoom(0)
	room.Backend().SetMinPublisherBandwidth(0)
	room.Backend().SetMaxPublisherBandwidth(0)
	room.updateBandwidth().Wait()

	pubs, subs, bw = room.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(2000),
		Received: api.BandwidthFromBits(100000),
	}, bw)

	assert.EqualValues(700000, pub.GetBandwidth())
	assert.EqualValues(1234567, room.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(700000, room.GetNextPublisherBandwidth(sfu.StreamTypeVideo))
}

func TestRoom_BandwidthClustered(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub1, hub2, _, _, server1, server2 := CreateClusteredHubsForTestWithConfig(t, func(s *httptest.Server) (*goconf.ConfigFile, error) {
		config, err := getTestConfig(s)
		if err != nil {
			return nil, err
		}

		config.AddOption("backend", "maxstreambitrate", "400000")
		config.AddOption("backend", "maxscreenbitrate", "600000")

		config.AddOption("backend", "bitrateperroom", "600000")
		config.AddOption("backend", "minpublisherbitrate", "100000")
		config.AddOption("backend", "maxpublisherbitrate", "300000")
		return config, err
	})

	mcu1 := sfutest.NewSFU(t)
	hub1.SetMcu(mcu1)

	mcu2 := sfutest.NewSFU(t)
	hub2.SetMcu(mcu2)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	client1, hello1 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"1")
	client2, hello2 := NewTestClientWithHello(ctx, t, server2, hub2, testDefaultUserId+"2")

	// Join room by id.
	roomId := "test-room"
	roomMsg1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg1.Room.RoomId)
	client1.RunUntilJoined(ctx, hello1.Hello)

	roomMsg2 := MustSucceed2(t, client2.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg2.Room.RoomId)
	client1.RunUntilJoined(ctx, hello2.Hello)
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

	room1 := hub1.getRoom(roomId)
	require.NotNil(room1)
	room2 := hub2.getRoom(roomId)
	require.NotNil(room2)

	require.NoError(client1.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	require.True(client1.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub1 := mcu1.GetPublisher(hello1.Hello.SessionId)
	require.NotNil(pub1)
	pub1.SetBandwidthInfo(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	})

	room1.updateBandwidth().Wait()
	pubs, subs, bw := room1.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	}, bw)

	assert.EqualValues(300000, pub1.GetBandwidth())
	assert.EqualValues(600000, room1.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(300000, room1.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	room2.updateBandwidth().Wait()
	pubs, subs, bw = room2.Bandwidth()
	assert.EqualValues(0, pubs)
	assert.EqualValues(0, subs)
	assert.Nil(bw)

	require.NoError(client2.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "65432",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	require.True(client2.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub2 := mcu2.GetPublisher(hello2.Hello.SessionId)
	require.NotNil(pub2)
	pub2.SetBandwidthInfo(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	})

	// Room1 will not have the latest properties (e.g. total number of publishers)
	// from room2 yet as that has not been updated yet.
	room1.updateBandwidth().Wait()
	pubs, subs, bw = room1.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	}, bw)

	assert.EqualValues(300000, pub1.GetBandwidth())
	assert.EqualValues(600000, room1.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(300000, room1.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	// After room2 has been updated, room1 will receive the latest properties when
	// updating again.
	room2.updateBandwidth().Wait()
	pubs, subs, bw = room2.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	}, bw)

	assert.EqualValues(300000, pub2.GetBandwidth())
	assert.EqualValues(600000, room2.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	// The next publisher (the third one) will get limited bandwidths.
	assert.EqualValues(200000, room2.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	room1.updateBandwidth().Wait()
	pubs, subs, bw = room1.Bandwidth()
	assert.EqualValues(1, pubs)
	assert.EqualValues(0, subs)
	assert.Equal(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(300000),
	}, bw)

	assert.EqualValues(300000, pub1.GetBandwidth())
	assert.EqualValues(600000, room1.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(200000, room1.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	// Joining a third publisher will decrease the bandwidth of all other publishers.
	client3, hello3 := NewTestClientWithHello(ctx, t, server1, hub1, testDefaultUserId+"3")
	roomMsg3 := MustSucceed2(t, client3.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg3.Room.RoomId)
	client1.RunUntilJoined(ctx, hello3.Hello)
	client2.RunUntilJoined(ctx, hello3.Hello)
	client3.RunUntilJoined(ctx, hello1.Hello, hello2.Hello, hello3.Hello)

	require.NoError(client3.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello3.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "65432",
		RoomType: "video",
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))
	require.True(client3.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub3 := mcu1.GetPublisher(hello3.Hello.SessionId)
	require.NotNil(pub3)
	pub3.SetBandwidthInfo(&sfu.ClientBandwidthInfo{
		Sent:     api.BandwidthFromBits(0),
		Received: api.BandwidthFromBits(200000),
	})

	room1.updateBandwidth().Wait()
	room2.updateBandwidth().Wait()

	assert.EqualValues(600000, room1.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(150000, room1.GetNextPublisherBandwidth(sfu.StreamTypeVideo))
	assert.EqualValues(600000, room2.GetNextPublisherBandwidth(sfu.StreamTypeScreen))
	assert.EqualValues(150000, room2.GetNextPublisherBandwidth(sfu.StreamTypeVideo))

	assert.EqualValues(200000, pub1.GetBandwidth())
	assert.EqualValues(200000, pub2.GetBandwidth())
	assert.EqualValues(200000, pub3.GetBandwidth())
}
