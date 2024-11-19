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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

func TestBandwidth_Client(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Client may not send an offer with audio and video.
	bitrate := 10000
	require.NoError(client.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Bitrate:  bitrate,
		Payload: api.StringMap{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.True(client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

	pub := mcu.GetPublisher(hello.Hello.SessionId)
	require.NotNil(pub)
	assert.Equal(bitrate, pub.settings.Bitrate)
}

func TestBandwidth_Backend(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	u, err := url.Parse(server.URL + "/one")
	require.NoError(t, err)
	backend := hub.backend.GetBackend(u)
	require.NotNil(t, backend, "Could not get backend")

	backend.maxScreenBitrate = 1000
	backend.maxStreamBitrate = 2000

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu, err := NewTestMCU()
	require.NoError(t, err)
	require.NoError(t, mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	streamTypes := []StreamType{
		StreamTypeVideo,
		StreamTypeScreen,
	}

	for _, streamType := range streamTypes {
		t.Run(string(streamType), func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)
			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			params := TestBackendClientAuthParams{
				UserId: testDefaultUserId,
			}
			require.NoError(client.SendHelloParams(server.URL+"/one", HelloVersionV1, "client", nil, params))

			hello := MustSucceed1(t, client.RunUntilHello, ctx)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			require.True(client.RunUntilJoined(ctx, hello.Hello))

			// Client may not send an offer with audio and video.
			bitrate := 10000
			require.NoError(client.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: string(streamType),
				Bitrate:  bitrate,
				Payload: api.StringMap{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}))

			require.True(client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

			pub := mcu.GetPublisher(hello.Hello.SessionId)
			require.NotNil(pub, "Could not find publisher")

			var expectBitrate int
			if streamType == StreamTypeVideo {
				expectBitrate = backend.maxStreamBitrate
			} else {
				expectBitrate = backend.maxScreenBitrate
			}
			assert.Equal(expectBitrate, pub.settings.Bitrate)
		})
	}
}

func TestFeatureChatRelay(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)

	testFunc := func(feature bool) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()
			var features []string
			if feature {
				features = append(features, ClientFeatureChatRelay)
			}
			require.NoError(client.SendHelloClientWithFeatures(testDefaultUserId, features))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello := MustSucceed1(t, client.RunUntilHello, ctx)

			roomId := "test-room"
			roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			client.RunUntilJoined(ctx, hello.Hello)

			room := hub.getRoom(roomId)
			require.NotNil(room)

			chatComment := api.StringMap{
				"foo": "bar",
				"baz": true,
				"lala": map[string]any{
					"one": "eins",
				},
			}
			message := api.StringMap{
				"type": "chat",
				"chat": api.StringMap{
					"refresh": true,
					"comment": chatComment,
				},
			}
			data, err := json.Marshal(message)
			require.NoError(err)

			// Simulate request from the backend.
			room.ProcessBackendRoomRequest(&AsyncMessage{
				Type: "room",
				Room: &BackendServerRoomRequest{
					Type: "message",
					Message: &BackendRoomMessageRequest{
						Data: data,
					},
				},
			})

			if msg, ok := client.RunUntilRoomMessage(ctx); ok {
				assert.Equal(roomId, msg.RoomId)
				var data api.StringMap
				if err := json.Unmarshal(msg.Data, &data); assert.NoError(err) {
					assert.Equal("chat", data["type"], "invalid type entry in %+v", data)
					if chat, found := api.GetStringMapEntry[map[string]any](data, "chat"); assert.True(found, "chat entry is missing in %+v", data) {
						if feature {
							assert.EqualValues(chatComment, chat["comment"])
							_, found := chat["refresh"]
							assert.False(found, "refresh should not be included")
						} else {
							assert.Equal(true, chat["refresh"])
							_, found := chat["comment"]
							assert.False(found, "the comment should not be included")
						}
					}
				}
			}
		}
	}

	t.Run("without-chat-relay", testFunc(false))
	t.Run("with-chat-relay", testFunc(true))
}

func TestPermissionHideDisplayNames(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)

	testFunc := func(permission bool) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)

			roomId := "test-room"
			roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			client.RunUntilJoined(ctx, hello.Hello)

			room := hub.getRoom(roomId)
			require.NotNil(room)

			if permission {
				session := hub.GetSessionByPublicId(hello.Hello.SessionId).(*ClientSession)
				require.NotNil(session, "Session %s does not exist", hello.Hello.SessionId)

				// Client may not receive display names.
				session.SetPermissions([]Permission{PERMISSION_HIDE_DISPLAYNAMES})
			}

			chatComment := api.StringMap{
				"actorDisplayName": "John Doe",
				"baz":              true,
				"lala": map[string]any{
					"one": "eins",
				},
			}
			message := api.StringMap{
				"type": "chat",
				"chat": api.StringMap{
					"comment": chatComment,
				},
			}
			data, err := json.Marshal(message)
			require.NoError(err)

			// Simulate request from the backend.
			room.ProcessBackendRoomRequest(&AsyncMessage{
				Type: "room",
				Room: &BackendServerRoomRequest{
					Type: "message",
					Message: &BackendRoomMessageRequest{
						Data: data,
					},
				},
			})

			if msg, ok := client.RunUntilRoomMessage(ctx); ok {
				assert.Equal(roomId, msg.RoomId)
				var data api.StringMap
				if err := json.Unmarshal(msg.Data, &data); assert.NoError(err) {
					assert.Equal("chat", data["type"], "invalid type entry in %+v", data)
					if chat, found := api.GetStringMapEntry[map[string]any](data, "chat"); assert.True(found, "chat entry is missing in %+v", data) {
						comment, found := chat["comment"]
						if assert.True(found, "comment is missing in %+v", chat) {
							if permission {
								displayName, found := comment.(map[string]any)["actorDisplayName"]
								assert.True(!found || displayName == "", "the display name should not be included in %+v", comment)
							} else {
								displayName, found := comment.(map[string]any)["actorDisplayName"]
								assert.True(found && displayName != "", "the display name should be included in %+v", comment)
							}
						}
					}
				}

				client2, hello2 := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId+"2")

				roomMsg2 := MustSucceed2(t, client2.JoinRoom, ctx, roomId)
				require.Equal(roomId, roomMsg2.Room.RoomId)

				client.RunUntilJoined(ctx, hello2.Hello)
				client2.RunUntilJoined(ctx, hello.Hello, hello2.Hello)

				recipient1 := MessageClientMessageRecipient{
					Type:      "session",
					SessionId: hello.Hello.SessionId,
				}
				data1 := api.StringMap{
					"type":    "nickChanged",
					"message": "from-1-to-2",
				}
				client2.SendMessage(recipient1, data1) // nolint
				if permission {
					ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
					defer cancel2()

					client.RunUntilErrorIs(ctx2, context.DeadlineExceeded)
				} else {
					var payload2 api.StringMap
					if ok := checkReceiveClientMessage(ctx, t, client, "session", hello2.Hello, &payload2); ok {
						assert.Equal(data1, payload2)
					}
				}
			}
		}
	}

	t.Run("without-hide-displaynames", testFunc(false))
	t.Run("with-hide-displaynames", testFunc(true))
}
