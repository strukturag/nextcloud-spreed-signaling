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
			equal := permissionsEqual(test.a, test.b)
			if equal != test.equal {
				t.Errorf("Expected %+v to be %s to %+v but was %s", test.a, equalStrings[test.equal], test.b, equalStrings[equal])
			}
		})
	}
}

func TestBandwidth_Client(t *testing.T) {
	hub, _, _, server := CreateHubForTest(t)

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	client := NewTestClient(t, server, hub)
	defer client.CloseWithBye()

	if err := client.SendHello(testDefaultUserId); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello, err := client.RunUntilHello(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Join room by id.
	roomId := "test-room"
	if room, err := client.JoinRoom(ctx, roomId); err != nil {
		t.Fatal(err)
	} else if room.Room.RoomId != roomId {
		t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
	}

	// We will receive a "joined" event.
	if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
		t.Error(err)
	}

	// Client may not send an offer with audio and video.
	bitrate := 10000
	if err := client.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "54321",
		RoomType: "video",
		Bitrate:  bitrate,
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
		t.Fatal(err)
	}

	pub := mcu.GetPublisher(hello.Hello.SessionId)
	if pub == nil {
		t.Fatal("Could not find publisher")
	}

	if pub.bitrate != bitrate {
		t.Errorf("Expected bitrate %d, got %d", bitrate, pub.bitrate)
	}
}

func TestBandwidth_Backend(t *testing.T) {
	hub, _, _, server := CreateHubWithMultipleBackendsForTest(t)

	u, err := url.Parse(server.URL + "/one")
	if err != nil {
		t.Fatal(err)
	}
	backend := hub.backend.GetBackend(u)
	if backend == nil {
		t.Fatal("Could not get backend")
	}

	backend.maxScreenBitrate = 1000
	backend.maxStreamBitrate = 2000

	mcu, err := NewTestMCU()
	if err != nil {
		t.Fatal(err)
	} else if err := mcu.Start(); err != nil {
		t.Fatal(err)
	}
	defer mcu.Stop()

	hub.SetMcu(mcu)

	streamTypes := []string{
		streamTypeVideo,
		streamTypeScreen,
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for _, streamType := range streamTypes {
		t.Run(streamType, func(t *testing.T) {
			client := NewTestClient(t, server, hub)
			defer client.CloseWithBye()

			params := TestBackendClientAuthParams{
				UserId: testDefaultUserId,
			}
			if err := client.SendHelloParams(server.URL+"/one", "client", params); err != nil {
				t.Fatal(err)
			}

			hello, err := client.RunUntilHello(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Join room by id.
			roomId := "test-room"
			if room, err := client.JoinRoom(ctx, roomId); err != nil {
				t.Fatal(err)
			} else if room.Room.RoomId != roomId {
				t.Fatalf("Expected room %s, got %s", roomId, room.Room.RoomId)
			}

			// We will receive a "joined" event.
			if err := client.RunUntilJoined(ctx, hello.Hello); err != nil {
				t.Error(err)
			}

			// Client may not send an offer with audio and video.
			bitrate := 10000
			if err := client.SendMessage(MessageClientMessageRecipient{
				Type:      "session",
				SessionId: hello.Hello.SessionId,
			}, MessageClientMessageData{
				Type:     "offer",
				Sid:      "54321",
				RoomType: streamType,
				Bitrate:  bitrate,
				Payload: map[string]interface{}{
					"sdp": MockSdpOfferAudioAndVideo,
				},
			}); err != nil {
				t.Fatal(err)
			}

			if err := client.RunUntilAnswer(ctx, MockSdpAnswerAudioAndVideo); err != nil {
				t.Fatal(err)
			}

			pub := mcu.GetPublisher(hello.Hello.SessionId)
			if pub == nil {
				t.Fatal("Could not find publisher")
			}

			var expectBitrate int
			if streamType == streamTypeVideo {
				expectBitrate = backend.maxStreamBitrate
			} else {
				expectBitrate = backend.maxScreenBitrate
			}
			if pub.bitrate != expectBitrate {
				t.Errorf("Expected bitrate %d, got %d", expectBitrate, pub.bitrate)
			}
		})
	}
}
