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
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
)

func TestBandwidth_Client(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)
	hub, _, _, server := CreateHubForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu := NewTestMCU(t)
	require.NoError(mcu.Start(ctx))
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
	defer client.CloseWithBye()

	// Join room by id.
	roomId := "test-room"
	roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	client.RunUntilJoined(ctx, hello.Hello)

	// Client may not send an offer with audio and video.
	bitrate := api.BandwidthFromBits(10000)
	require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, api.MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Bitrate:  bitrate,
		Payload: api.StringMap{
			"sdp": mock.MockSdpOfferAudioAndVideo,
		},
	}))

	require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

	pub := mcu.GetPublisher(hello.Hello.SessionId)
	require.NotNil(pub)
	assert.Equal(bitrate, pub.settings.Bitrate)
}

func TestBandwidth_Backend(t *testing.T) {
	t.Parallel()

	streamTypes := []StreamType{
		StreamTypeVideo,
		StreamTypeScreen,
	}

	for _, streamType := range streamTypes {
		t.Run(string(streamType), func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)

			hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

			u, err := url.Parse(server.URL + "/one")
			require.NoError(err)
			backend := hub.backend.GetBackend(u)
			require.NotNil(backend, "Could not get backend")

			backend.maxScreenBitrate = 1000
			backend.maxStreamBitrate = 2000

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			mcu := NewTestMCU(t)
			require.NoError(mcu.Start(ctx))
			defer mcu.Stop()

			hub.SetMcu(mcu)

			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			params := TestBackendClientAuthParams{
				UserId: testDefaultUserId,
			}
			require.NoError(client.SendHelloParams(server.URL+"/one", api.HelloVersionV1, "client", nil, params))

			hello := MustSucceed1(t, client.RunUntilHello, ctx)

			// Join room by id.
			roomId := "test-room"
			roomMsg := MustSucceed2(t, client.JoinRoom, ctx, roomId)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			require.True(client.RunUntilJoined(ctx, hello.Hello))

			// Client may not send an offer with audio and video.
			bitrate := api.BandwidthFromBits(10000)
			require.NoError(client.SendMessage(api.MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello.Hello.SessionId,
			}, api.MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: string(streamType),
				Bitrate:  bitrate,
				Payload: api.StringMap{
					"sdp": mock.MockSdpOfferAudioAndVideo,
				},
			}))

			require.True(client.RunUntilAnswer(ctx, mock.MockSdpAnswerAudioAndVideo))

			pub := mcu.GetPublisher(hello.Hello.SessionId)
			require.NotNil(pub, "Could not find publisher")

			var expectBitrate api.Bandwidth
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
				features = append(features, api.ClientFeatureChatRelay)
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
				"token": roomId,
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
			room.processAsyncMessage(&AsyncMessage{
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

	t.Run("without-chat-relay", testFunc(false)) // nolint:paralleltest
	t.Run("with-chat-relay", testFunc(true))     // nolint:paralleltest
}

func TestFeatureChatRelayFederation(t *testing.T) {
	t.Parallel()

	var testFunc = func(feature bool) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)

			hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

			localFeatures := []string{
				api.ClientFeatureChatRelay,
			}
			var federatedFeatures []string
			if feature {
				federatedFeatures = append(federatedFeatures, api.ClientFeatureChatRelay)
			}

			client1 := NewTestClient(t, server1, hub1)
			defer client1.CloseWithBye()
			require.NoError(client1.SendHelloClientWithFeatures(testDefaultUserId+"1", localFeatures))

			client2 := NewTestClient(t, server2, hub2)
			defer client2.CloseWithBye()
			require.NoError(client2.SendHelloClientWithFeatures(testDefaultUserId+"2", federatedFeatures))

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			hello1 := MustSucceed1(t, client1.RunUntilHello, ctx)
			hello2 := MustSucceed1(t, client2.RunUntilHello, ctx)

			roomId := "test-room"
			federatedRoomId := roomId + "@federated"
			room1 := MustSucceed2(t, client1.JoinRoom, ctx, roomId)
			require.Equal(roomId, room1.Room.RoomId)

			client1.RunUntilJoined(ctx, hello1.Hello)

			now := time.Now()
			userdata := api.StringMap{
				"displayname": "Federated user",
				"actorType":   "federated_users",
				"actorId":     "the-federated-user-id",
			}
			token, err := client1.CreateHelloV2TokenWithUserdata(testDefaultUserId+"2", now, now.Add(time.Minute), userdata)
			require.NoError(err)

			msg := &api.ClientMessage{
				Id:   "join-room-fed",
				Type: "room",
				Room: &api.RoomClientMessage{
					RoomId:    federatedRoomId,
					SessionId: api.RoomSessionId(fmt.Sprintf("%s-%s", federatedRoomId, hello2.Hello.SessionId)),
					Federation: &api.RoomFederationMessage{
						SignalingUrl: server1.URL,
						NextcloudUrl: server1.URL,
						RoomId:       roomId,
						Token:        token,
					},
				},
			}
			require.NoError(client2.WriteJSON(msg))

			if message, ok := client2.RunUntilMessage(ctx); ok {
				assert.Equal(msg.Id, message.Id)
				require.Equal("room", message.Type)
				require.Equal(federatedRoomId, message.Room.RoomId)
			}

			// The client1 will see the remote session id for client2.
			var remoteSessionId api.PublicSessionId
			if message, ok := client1.RunUntilMessage(ctx); ok {
				client1.checkSingleMessageJoined(message)
				evt := message.Event.Join[0]
				remoteSessionId = evt.SessionId
				assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
				assert.Equal(hello2.Hello.UserId, evt.UserId)
				assert.True(evt.Federated)
			}

			// The client2 will see its own session id, not the one from the remote server.
			client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)

			room := hub1.getRoom(roomId)
			require.NotNil(room)

			chatComment := map[string]any{
				"token":             roomId,
				"actorId":           hello1.Hello.UserId,
				"actorType":         "users",
				"lastEditActorId":   hello1.Hello.UserId,
				"lastEditActorType": "users",
				"parent": map[string]any{
					"actorId":           hello1.Hello.UserId,
					"actorType":         "users",
					"lastEditActorId":   hello1.Hello.UserId,
					"lastEditActorType": "users",
				},
				"messageParameters": map[string]map[string]any{
					"mention-local-user": {
						"type": "user",
						"id":   hello1.Hello.UserId,
						"name": "User 1",
					},
					"mention-remote-user": {
						"type":       "user",
						"id":         hello2.Hello.UserId,
						"name":       "User 2",
						"mention-id": "federated_user/" + hello2.Hello.UserId + "@" + getCloudUrl(server2.URL),
						"server":     server2.URL,
					},
					"mention-call": {
						"type": "call",
						"id":   roomId,
					},
				},
			}
			federatedChatComment := map[string]any{
				"token":             federatedRoomId,
				"actorId":           hello1.Hello.UserId + "@" + getCloudUrl(server1.URL),
				"actorType":         "federated_users",
				"lastEditActorId":   hello1.Hello.UserId + "@" + getCloudUrl(server1.URL),
				"lastEditActorType": "federated_users",
				"parent": map[string]any{
					"actorId":           hello1.Hello.UserId + "@" + getCloudUrl(server1.URL),
					"actorType":         "federated_users",
					"lastEditActorId":   hello1.Hello.UserId + "@" + getCloudUrl(server1.URL),
					"lastEditActorType": "federated_users",
				},
				"messageParameters": map[string]map[string]any{
					"mention-local-user": {
						"type":       "user",
						"id":         hello1.Hello.UserId,
						"mention-id": hello1.Hello.UserId,
						"name":       "User 1",
						"server":     server1.URL,
					},
					"mention-remote-user": {
						"type":       "user",
						"id":         hello2.Hello.UserId,
						"name":       "User 2",
						"mention-id": "federated_user/" + hello2.Hello.UserId + "@" + getCloudUrl(server2.URL),
					},
					"mention-call": {
						"type": "call",
						"id":   federatedRoomId,
					},
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
			room.processAsyncMessage(&AsyncMessage{
				Type: "room",
				Room: &BackendServerRoomRequest{
					Type: "message",
					Message: &BackendRoomMessageRequest{
						Data: data,
					},
				},
			})

			// The first client will receive the message for the local room (always including the actual message).
			if msg, ok := client1.RunUntilRoomMessage(ctx); ok {
				assert.Equal(roomId, msg.RoomId)
				var data api.StringMap
				if err := json.Unmarshal(msg.Data, &data); assert.NoError(err) {
					assert.Equal("chat", data["type"], "invalid type entry in %+v", data)
					if chat, found := api.GetStringMapEntry[map[string]any](data, "chat"); assert.True(found, "chat entry is missing in %+v", data) {
						AssertEqualSerialized(t, chatComment, chat["comment"])
						_, found := chat["refresh"]
						assert.False(found, "refresh should not be included")
					}
				}
			}

			// The second client will receive the message from the federated room (either as refresh or with the message).
			if msg, ok := client2.RunUntilRoomMessage(ctx); ok {
				assert.Equal(federatedRoomId, msg.RoomId)
				var data api.StringMap
				if err := json.Unmarshal(msg.Data, &data); assert.NoError(err) {
					assert.Equal("chat", data["type"], "invalid type entry in %+v", data)
					if chat, found := api.GetStringMapEntry[map[string]any](data, "chat"); assert.True(found, "chat entry is missing in %+v", data) {
						if feature {
							AssertEqualSerialized(t, federatedChatComment, chat["comment"])
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

	t.Run("without-chat-relay", testFunc(false)) // nolint:paralleltest
	t.Run("with-chat-relay", testFunc(true))     // nolint:paralleltest
}

func TestPermissionHideDisplayNames(t *testing.T) {
	t.Parallel()

	testFunc := func(permission bool) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			require := require.New(t)
			assert := assert.New(t)
			hub, _, _, server := CreateHubForTest(t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			client, hello := NewTestClientWithHello(ctx, t, server, hub, testDefaultUserId)
			defer client.CloseWithBye()

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
			room.processAsyncMessage(&AsyncMessage{
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
				defer client2.CloseWithBye()

				roomMsg2 := MustSucceed2(t, client2.JoinRoom, ctx, roomId)
				require.Equal(roomId, roomMsg2.Room.RoomId)

				client.RunUntilJoined(ctx, hello2.Hello)
				client2.RunUntilJoined(ctx, hello.Hello, hello2.Hello)

				recipient1 := api.MessageClientMessageRecipient{
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

	t.Run("without-hide-displaynames", testFunc(false)) // nolint:paralleltest
	t.Run("with-hide-displaynames", testFunc(true))     // nolint:paralleltest
}
