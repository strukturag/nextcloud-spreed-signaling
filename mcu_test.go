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
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/dlintw/goconf"
)

const (
	TestMaxBitrateScreen = 12345678
	TestMaxBitrateVideo  = 23456789
)

type TestMCU struct {
	mu         sync.Mutex
	publishers map[string]*TestMCUPublisher
}

func NewTestMCU() (*TestMCU, error) {
	return &TestMCU{
		publishers: make(map[string]*TestMCUPublisher),
	}, nil
}

func (m *TestMCU) Start() error {
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

func (m *TestMCU) GetStats() interface{} {
	return nil
}

func (m *TestMCU) NewPublisher(ctx context.Context, listener McuListener, id string, sid string, streamType string, bitrate int, mediaTypes MediaType, initiator McuInitiator) (McuPublisher, error) {
	var maxBitrate int
	if streamType == streamTypeScreen {
		maxBitrate = TestMaxBitrateScreen
	} else {
		maxBitrate = TestMaxBitrateVideo
	}
	if bitrate <= 0 {
		bitrate = maxBitrate
	} else if bitrate > maxBitrate {
		bitrate = maxBitrate
	}
	pub := &TestMCUPublisher{
		TestMCUClient: TestMCUClient{
			id:         id,
			sid:        sid,
			streamType: streamType,
		},

		mediaTypes: mediaTypes,
		bitrate:    bitrate,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.publishers[id] = pub
	return pub, nil
}

func (m *TestMCU) GetPublishers() map[string]*TestMCUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*TestMCUPublisher, len(m.publishers))
	for id, pub := range m.publishers {
		result[id] = pub
	}
	return result
}

func (m *TestMCU) GetPublisher(id string) *TestMCUPublisher {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.publishers[id]
}

func (m *TestMCU) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	return nil, fmt.Errorf("Not implemented")
}

type TestMCUClient struct {
	closed int32

	id         string
	sid        string
	streamType string
}

func (c *TestMCUClient) Id() string {
	return c.id
}

func (c *TestMCUClient) Sid() string {
	return c.sid
}

func (c *TestMCUClient) StreamType() string {
	return c.streamType
}

func (c *TestMCUClient) Close(ctx context.Context) {
	log.Printf("Close MCU client %s", c.id)
	atomic.StoreInt32(&c.closed, 1)
}

func (c *TestMCUClient) isClosed() bool {
	return atomic.LoadInt32(&c.closed) != 0
}

type TestMCUPublisher struct {
	TestMCUClient

	mediaTypes MediaType
	bitrate    int
}

func (p *TestMCUPublisher) HasMedia(mt MediaType) bool {
	return (p.mediaTypes & mt) == mt
}

func (p *TestMCUPublisher) SetMedia(mt MediaType) {
	p.mediaTypes = mt
}

func (p *TestMCUPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	go func() {
		if p.isClosed() {
			callback(fmt.Errorf("Already closed"), nil)
			return
		}

		switch data.Type {
		case "offer":
			sdp := data.Payload["sdp"]
			if sdp, ok := sdp.(string); ok {
				if sdp == MockSdpOfferAudioOnly {
					callback(nil, map[string]interface{}{
						"type": "answer",
						"sdp":  MockSdpAnswerAudioOnly,
					})
					return
				} else if sdp == MockSdpOfferAudioAndVideo {
					callback(nil, map[string]interface{}{
						"type": "answer",
						"sdp":  MockSdpAnswerAudioAndVideo,
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
