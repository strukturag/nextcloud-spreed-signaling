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
package test

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

type SFU struct {
	t  *testing.T
	mu sync.Mutex
	// +checklocks:mu
	publishers map[api.PublicSessionId]*SFUPublisher
	// +checklocks:mu
	subscribers map[string]*SFUSubscriber

	maxStreamBitrate api.AtomicBandwidth
	maxScreenBitrate api.AtomicBandwidth
}

func NewSFU(t *testing.T) *SFU {
	return &SFU{
		t: t,

		publishers:  make(map[api.PublicSessionId]*SFUPublisher),
		subscribers: make(map[string]*SFUSubscriber),
	}
}

func (m *SFU) GetBandwidthLimits() (api.Bandwidth, api.Bandwidth) {
	return m.maxStreamBitrate.Load(), m.maxScreenBitrate.Load()
}

func (m *SFU) SetBandwidthLimits(maxStreamBitrate api.Bandwidth, maxScreenBitrate api.Bandwidth) {
	m.maxStreamBitrate.Store(maxStreamBitrate)
	m.maxScreenBitrate.Store(maxScreenBitrate)
}

func (m *SFU) Start(ctx context.Context) error {
	return nil
}

func (m *SFU) Stop() {
}

func (m *SFU) Reload(config *goconf.ConfigFile) {
}

func (m *SFU) SetOnConnected(f func()) {
}

func (m *SFU) SetOnDisconnected(f func()) {
}

func (m *SFU) GetStats() any {
	return nil
}

func (m *SFU) GetServerInfoSfu() *talk.BackendServerInfoSfu {
	return nil
}

func (m *SFU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
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
	pub := &SFUPublisher{
		SFUClient: SFUClient{
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

func (m *SFU) GetPublishers() map[api.PublicSessionId]*SFUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := maps.Clone(m.publishers)
	return result
}

func (m *SFU) GetPublisher(id api.PublicSessionId) *SFUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.publishers[id]
}

func (m *SFU) NewSubscriber(ctx context.Context, listener sfu.Listener, publisher api.PublicSessionId, streamType sfu.StreamType, initiator sfu.Initiator) (sfu.Subscriber, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pub := m.publishers[publisher]
	if pub == nil {
		return nil, errors.New("waiting for publisher not implemented yet")
	}

	id := internal.RandomString(8)
	sub := &SFUSubscriber{
		SFUClient: SFUClient{
			t:          m.t,
			id:         id,
			streamType: streamType,
		},

		publisher: pub,
	}
	return sub, nil
}

type SFUClient struct {
	t      *testing.T
	closed atomic.Bool

	id         string
	sid        string
	streamType sfu.StreamType
}

func (c *SFUClient) Id() string {
	return c.id
}

func (c *SFUClient) Sid() string {
	return c.sid
}

func (c *SFUClient) StreamType() sfu.StreamType {
	return c.streamType
}

func (c *SFUClient) MaxBitrate() api.Bandwidth {
	return 0
}

func (c *SFUClient) Close(ctx context.Context) {
	if c.closed.CompareAndSwap(false, true) {
		logger := logtest.NewLoggerForTest(c.t)
		logger.Printf("Close SFU client %s", c.id)
	}
}

func (c *SFUClient) IsClosed() bool {
	return c.closed.Load()
}

type SFUPublisher struct {
	SFUClient

	settings sfu.NewPublisherSettings

	sdp string

	bandwidth     atomic.Uint64
	bandwidthInfo atomic.Pointer[sfu.ClientBandwidthInfo]
}

func (p *SFUPublisher) Settings() sfu.NewPublisherSettings {
	return p.settings
}

func (p *SFUPublisher) PublisherId() api.PublicSessionId {
	return api.PublicSessionId(p.id)
}

func (p *SFUPublisher) HasMedia(mt sfu.MediaType) bool {
	return (p.settings.MediaTypes & mt) == mt
}

func (p *SFUPublisher) SetMedia(mt sfu.MediaType) {
	p.settings.MediaTypes = mt
}

func (p *SFUPublisher) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	go func() {
		if p.IsClosed() {
			callback(errors.New("already closed"), nil)
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
			callback(fmt.Errorf("offer payload %+v is not implemented", data.Payload), nil)
		default:
			callback(fmt.Errorf("message type %s is not implemented", data.Type), nil)
		}
	}()
}

func (p *SFUPublisher) GetStreams(ctx context.Context) ([]sfu.PublisherStream, error) {
	return nil, errors.New("not implemented")
}

func (p *SFUPublisher) PublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("remote publishing not supported")
}

func (p *SFUPublisher) UnpublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error {
	return errors.New("remote publishing not supported")
}

func (p *SFUPublisher) SetBandwidthInfo(bandwidth *sfu.ClientBandwidthInfo) {
	p.bandwidthInfo.Store(bandwidth)
}

func (p *SFUPublisher) Bandwidth() *sfu.ClientBandwidthInfo {
	return p.bandwidthInfo.Load()
}

func (p *SFUPublisher) SetBandwidth(ctx context.Context, bandwidth api.Bandwidth) error {
	p.bandwidth.Store(bandwidth.Bits())
	return nil
}

func (p *SFUPublisher) GetBandwidth() api.Bandwidth {
	return api.BandwidthFromBits(p.bandwidth.Load())
}

type SFUSubscriber struct {
	SFUClient

	publisher *SFUPublisher
}

func (s *SFUSubscriber) Publisher() api.PublicSessionId {
	return s.publisher.PublisherId()
}

func (s *SFUSubscriber) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
	go func() {
		if s.IsClosed() {
			callback(errors.New("already closed"), nil)
			return
		}

		switch data.Type {
		case "requestoffer":
			fallthrough
		case "sendoffer":
			sdp := s.publisher.sdp
			if sdp == "" {
				callback(errors.New("publisher not sending (no SDP)"), nil)
				return
			}

			callback(nil, api.StringMap{
				"type": "offer",
				"sdp":  sdp,
			})
		case "answer":
			callback(nil, nil)
		default:
			callback(fmt.Errorf("message type %s is not implemented", data.Type), nil)
		}
	}()
}
