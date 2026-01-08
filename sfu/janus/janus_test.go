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
package janus

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
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	metricstest "github.com/strukturag/nextcloud-spreed-signaling/metrics/test"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
	janustest "github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/test"
)

const (
	testTimeout = 10 * time.Second
)

func TestMcuJanusStats(t *testing.T) {
	t.Parallel()
	metricstest.CollectAndLint(t, janusMcuStats...)
}

func newMcuJanusForTesting(t *testing.T) (*janusSFU, *janustest.JanusGateway) {
	gateway := janustest.NewJanusGateway(t)

	config := goconf.NewConfigFile()
	if strings.Contains(t.Name(), "Filter") {
		config.AddOption("mcu", "blockedcandidates", "192.0.0.0/24, 192.168.0.0/16")
	}
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	mcu, err := NewJanusSFU(ctx, "", config)
	require.NoError(t, err)
	t.Cleanup(func() {
		mcu.Stop()
	})

	mcuJanus := mcu.(*janusSFU)
	mcuJanus.createJanusGateway = func(ctx context.Context, wsURL string, listener janus.GatewayListener) (janus.GatewayInterface, error) {
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

func (t *TestMcuListener) OnUpdateOffer(client sfu.Client, offer api.StringMap) {

}

func (t *TestMcuListener) OnIceCandidate(client sfu.Client, candidate any) {

}

func (t *TestMcuListener) OnIceCompleted(client sfu.Client) {

}

func (t *TestMcuListener) SubscriberSidUpdated(subscriber sfu.Subscriber) {

}

func (t *TestMcuListener) PublisherClosed(publisher sfu.Publisher) {

}

func (t *TestMcuListener) SubscriberClosed(subscriber sfu.Subscriber) {

}

type TestMcuController struct {
	id api.PublicSessionId
}

func (c *TestMcuController) PublisherId() api.PublicSessionId {
	return c.id
}

func (c *TestMcuController) StartPublishing(ctx context.Context, publisher sfu.RemotePublisherProperties) error {
	// TODO: Check parameters?
	return nil
}

func (c *TestMcuController) StopPublishing(ctx context.Context, publisher sfu.RemotePublisherProperties) error {
	// TODO: Check parameters?
	return nil
}

func (c *TestMcuController) GetStreams(ctx context.Context) ([]sfu.PublisherStream, error) {
	streams := []sfu.PublisherStream{
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
	country geoip.Country
}

func (i *TestMcuInitiator) Country() geoip.Country {
	return i.country
}

func Test_JanusPublisherFilterOffer(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
		"trickle": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"start": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
						"room":      room.Id(),
						"started":   true,
						"videoroom": "event",
					},
				},
			}, nil
		},
		"trickle": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
			return &janus.AckMsg{}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, sfu.StreamTypeVideo, initiator2)
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
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

	if sb, ok := pub.(*janusPublisher); assert.True(ok, "expected publisher with streams support, got %T", pub) {
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
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

	if sb, ok := pub.(*janusPublisher); assert.True(ok, "expected publisher with streams support, got %T", pub) {
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{})

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

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	janusPub, ok := pub.(*janusPublisher)
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
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, sfu.StreamTypeVideo, initiator2)
	require.NoError(err)
	defer sub.Close(context.Background())

	janusSub, ok := sub.(*janusSubscriber)
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	ready := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		time.Sleep(100 * time.Millisecond)
		pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
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
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, sfu.StreamTypeVideo, initiator2)
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"configure": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	settings1 := sfu.NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", sfu.StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	listener2 := &TestMcuListener{
		id: pubId,
	}

	initiator2 := &TestMcuInitiator{
		country: "DE",
	}
	sub, err := mcu.NewSubscriber(ctx, listener2, pubId, sfu.StreamTypeVideo, initiator2)
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
	gateway.RegisterHandlers(map[string]janustest.JanusHandler{
		"add_remote_publisher": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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
		"remove_remote_publisher": func(room *janustest.JanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			assert.EqualValues(1, room.Id())
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

	pub, err := mcu.NewRemotePublisher(ctx, listener1, controller, sfu.StreamTypeVideo)
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
