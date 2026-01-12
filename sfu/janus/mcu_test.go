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
package janus

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

var (
	TestMaxBitrateScreen = api.BandwidthFromBits(12345678)
	TestMaxBitrateVideo  = api.BandwidthFromBits(23456789)
)

type TestMCU struct {
	t  *testing.T
	mu sync.Mutex
	// +checklocks:mu
	publishers map[api.PublicSessionId]*TestMCUPublisher
	// +checklocks:mu
	subscribers map[string]*TestMCUSubscriber

	maxStreamBitrate api.AtomicBandwidth
	maxScreenBitrate api.AtomicBandwidth
}

func NewTestMCU(t *testing.T) *TestMCU {
	return &TestMCU{
		t: t,

		publishers:  make(map[api.PublicSessionId]*TestMCUPublisher),
		subscribers: make(map[string]*TestMCUSubscriber),
	}
}

func (m *TestMCU) GetBandwidthLimits() (api.Bandwidth, api.Bandwidth) {
	return m.maxStreamBitrate.Load(), m.maxScreenBitrate.Load()
}

func (m *TestMCU) SetBandwidthLimits(maxStreamBitrate api.Bandwidth, maxScreenBitrate api.Bandwidth) {
	m.maxStreamBitrate.Store(maxStreamBitrate)
	m.maxScreenBitrate.Store(maxScreenBitrate)
}

func (m *TestMCU) Start(ctx context.Context) error {
	return nil
}

func (m *TestMCU) Stop() {
}

func (m *TestMCU) Reload(config *goconf.ConfigFile) {
}

func (m *TestMCU) SetOnConnected(f func()) {
}

func (m *TestMCU) SetOnDisconnected(f func()) {
}

func (m *TestMCU) GetStats() any {
	return nil
}

func (m *TestMCU) GetServerInfoSfu() *talk.BackendServerInfoSfu {
	return nil
}

func (m *TestMCU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	var maxBitrate api.Bandwidth
	if streamType == sfu.StreamTypeScreen {
		maxBitrate = TestMaxBitrateScreen
	} else {
		maxBitrate = TestMaxBitrateVideo
	}
	publisherSettings := settings
	bitrate := publisherSettings.Bitrate
	if bitrate <= 0 || bitrate > maxBitrate {
		publisherSettings.Bitrate = maxBitrate
	}
	pub := &TestMCUPublisher{
		TestMCUClient: TestMCUClient{
			t:          m.t,
			id:         string(id),
			sid:        sid,
			streamType: streamType,
		},

		settings: publisherSettings,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.publishers[id] = pub
	return pub, nil
}

func (m *TestMCU) GetPublishers() map[api.PublicSessionId]*TestMCUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := maps.Clone(m.publishers)
	return result
}

func (m *TestMCU) GetPublisher(id api.PublicSessionId) *TestMCUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.publishers[id]
}

func (m *TestMCU) NewSubscriber(ctx context.Context, listener sfu.Listener, publisher api.PublicSessionId, streamType sfu.StreamType, initiator sfu.Initiator) (sfu.Subscriber, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pub := m.publishers[publisher]
	if pub == nil {
		return nil, errors.New("Waiting for publisher not implemented yet")
	}

	id := internal.RandomString(8)
	sub := &TestMCUSubscriber{
		TestMCUClient: TestMCUClient{
			t:          m.t,
			id:         id,
			streamType: streamType,
		},

		publisher: pub,
	}
	return sub, nil
}

type TestMCUClient struct {
	t      *testing.T
	closed atomic.Bool

	id         string
	sid        string
	streamType sfu.StreamType
}

func (c *TestMCUClient) Id() string {
	return c.id
}

func (c *TestMCUClient) Sid() string {
	return c.sid
}

func (c *TestMCUClient) StreamType() sfu.StreamType {
	return c.streamType
}

func (c *TestMCUClient) MaxBitrate() api.Bandwidth {
	return 0
}

func (c *TestMCUClient) Close(ctx context.Context) {
	if c.closed.CompareAndSwap(false, true) {
		logger := logtest.NewLoggerForTest(c.t)
		logger.Printf("Close MCU client %s", c.id)
	}
}

func (c *TestMCUClient) isClosed() bool {
	return c.closed.Load()
}

type TestMCUPublisher struct {
	TestMCUClient

	settings sfu.NewPublisherSettings

	sdp string
}

func (p *TestMCUPublisher) PublisherId() api.PublicSessionId {
	return api.PublicSessionId(p.id)
}

func (p *TestMCUPublisher) HasMedia(mt sfu.MediaType) bool {
	return (p.settings.MediaTypes & mt) == mt
}

func (p *TestMCUPublisher) SetMedia(mt sfu.MediaType) {
	p.settings.MediaTypes = mt
}

func (p *TestMCUPublisher) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	go func() {
		if p.isClosed() {
			callback(errors.New("Already closed"), nil)
			return
		}

		switch data.Type {
		case "offer":
			sdp := data.Payload["sdp"]
			if sdp, ok := sdp.(string); ok {
				p.sdp = sdp
				switch sdp {
				case mock.MockSdpOfferAudioOnly:
					callback(nil, api.StringMap{
						"type": "answer",
						"sdp":  mock.MockSdpAnswerAudioOnly,
					})
					return
				case mock.MockSdpOfferAudioAndVideo:
					callback(nil, api.StringMap{
						"type": "answer",
						"sdp":  mock.MockSdpAnswerAudioAndVideo,
					})
					return
				}
			}
			callback(fmt.Errorf("Offer payload %+v is not implemented", data.Payload), nil)
		default:
			callback(fmt.Errorf("Message type %s is not implemented", data.Type), nil)
		}
	}()
}

func (p *TestMCUPublisher) GetStreams(ctx context.Context) ([]sfu.PublisherStream, error) {
	return nil, errors.New("not implemented")
}

func (p *TestMCUPublisher) PublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("remote publishing not supported")
}

func (p *TestMCUPublisher) UnpublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("remote publishing not supported")
}

type TestMCUSubscriber struct {
	TestMCUClient

	publisher *TestMCUPublisher
}

func (s *TestMCUSubscriber) Publisher() api.PublicSessionId {
	return s.publisher.PublisherId()
}

func (s *TestMCUSubscriber) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	go func() {
		if s.isClosed() {
			callback(errors.New("Already closed"), nil)
			return
		}

		switch data.Type {
		case "requestoffer":
			fallthrough
		case "sendoffer":
			sdp := s.publisher.sdp
			if sdp == "" {
				callback(errors.New("Publisher not sending (no SDP)"), nil)
				return
			}

			callback(nil, api.StringMap{
				"type": "offer",
				"sdp":  sdp,
			})
		case "answer":
			callback(nil, nil)
		default:
			callback(fmt.Errorf("Message type %s is not implemented", data.Type), nil)
		}
	}()
}
