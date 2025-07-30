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
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	equalStrings = map[bool]string{
		true:  "equal",
		false: "not equal",
	}
)

type EqualTestData struct {
	a     map[Permission]bool
	b     map[Permission]bool
	equal bool
}

func Test_permissionsEqual(t *testing.T) {
	tests := []EqualTestData{
		{
			a:     nil,
			b:     nil,
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b:     nil,
			equal: false,
		},
		{
			a: nil,
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: false,
			},
			equal: false,
		},
	}
	for idx, test := range tests {
		test := test
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			t.Parallel()
			equal := permissionsEqual(test.a, test.b)
			assert.Equal(t, test.equal, equal, "Expected %+v to be %s to %+v but was %s", test.a, equalStrings[test.equal], test.b, equalStrings[equal])
		})
	}
}

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

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	require.NoError(client.SendHello(testDefaultUserId))

	hello, err := client.RunUntilHello(ctx)
	require.NoError(err)

	// Join room by id.
	roomId := "test-room"
	roomMsg, err := client.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, roomMsg.Room.RoomId)

	// We will receive a "joined" event.
	assert.NoError(client.RunUntilJoined(ctx, hello.Hello))

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
		Payload: StringMap{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.NoError(client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

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

			hello, err := client.RunUntilHello(ctx)
			require.NoError(err)

			// Join room by id.
			roomId := "test-room"
			roomMsg, err := client.JoinRoom(ctx, roomId)
			require.NoError(err)
			require.Equal(roomId, roomMsg.Room.RoomId)

			// We will receive a "joined" event.
			require.NoError(client.RunUntilJoined(ctx, hello.Hello))

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
				Payload: StringMap{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}))

			require.NoError(client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo))

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
