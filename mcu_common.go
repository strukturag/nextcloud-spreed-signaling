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
package signaling

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	McuTypeJanus = "janus"
	McuTypeProxy = "proxy"

	McuTypeDefault = McuTypeJanus
)

var (
	defaultMaxStreamBitrate = api.BandwidthFromMegabits(1)
	defaultMaxScreenBitrate = api.BandwidthFromMegabits(2)

	ErrNotConnected = errors.New("not connected")
)

type MediaType int

const (
	MediaTypeAudio  MediaType = 1 << 0
	MediaTypeVideo  MediaType = 1 << 1
	MediaTypeScreen MediaType = 1 << 2
)

type McuListener interface {
	PublicId() PublicSessionId

	OnUpdateOffer(client McuClient, offer api.StringMap)

	OnIceCandidate(client McuClient, candidate any)
	OnIceCompleted(client McuClient)

	SubscriberSidUpdated(subscriber McuSubscriber)

	PublisherClosed(publisher McuPublisher)
	SubscriberClosed(subscriber McuSubscriber)
}

type McuInitiator interface {
	Country() string
}

type McuSettings interface {
	MaxStreamBitrate() api.Bandwidth
	MaxScreenBitrate() api.Bandwidth
	Timeout() time.Duration

	Reload(config *goconf.ConfigFile)
}

type mcuCommonSettings struct {
	logger Logger

	maxStreamBitrate api.AtomicBandwidth
	maxScreenBitrate api.AtomicBandwidth

	timeout atomic.Int64
}

func (s *mcuCommonSettings) MaxStreamBitrate() api.Bandwidth {
	return s.maxStreamBitrate.Load()
}

func (s *mcuCommonSettings) MaxScreenBitrate() api.Bandwidth {
	return s.maxScreenBitrate.Load()
}

func (s *mcuCommonSettings) Timeout() time.Duration {
	return time.Duration(s.timeout.Load())
}

func (s *mcuCommonSettings) setTimeout(timeout time.Duration) {
	s.timeout.Store(int64(timeout))
}

func (s *mcuCommonSettings) load(config *goconf.ConfigFile) error {
	maxStreamBitrateValue, _ := config.GetInt("mcu", "maxstreambitrate")
	if maxStreamBitrateValue <= 0 {
		maxStreamBitrateValue = int(defaultMaxStreamBitrate.Bits())
	}
	maxStreamBitrate := api.BandwidthFromBits(uint64(maxStreamBitrateValue))
	s.logger.Printf("Maximum bandwidth %s per publishing stream", maxStreamBitrate)
	s.maxStreamBitrate.Store(maxStreamBitrate)

	maxScreenBitrateValue, _ := config.GetInt("mcu", "maxscreenbitrate")
	if maxScreenBitrateValue <= 0 {
		maxScreenBitrateValue = int(defaultMaxScreenBitrate.Bits())
	}
	maxScreenBitrate := api.BandwidthFromBits(uint64(maxScreenBitrateValue))
	s.logger.Printf("Maximum bandwidth %s per screensharing stream", maxScreenBitrate)
	s.maxScreenBitrate.Store(maxScreenBitrate)
	return nil
}

type Mcu interface {
	Start(ctx context.Context) error
	Stop()
	Reload(config *goconf.ConfigFile)

	SetOnConnected(func())
	SetOnDisconnected(func())

	GetStats() any
	GetServerInfoSfu() *BackendServerInfoSfu
	GetBandwidthLimits() (api.Bandwidth, api.Bandwidth)

	NewPublisher(ctx context.Context, listener McuListener, id PublicSessionId, sid string, streamType StreamType, settings NewPublisherSettings, initiator McuInitiator) (McuPublisher, error)
	NewSubscriber(ctx context.Context, listener McuListener, publisher PublicSessionId, streamType StreamType, initiator McuInitiator) (McuSubscriber, error)
}

// PublisherStream contains the available properties when creating a
// remote publisher in Janus.
type PublisherStream struct {
	Mid    string `json:"mid"`
	Mindex int    `json:"mindex"`
	Type   string `json:"type"`

	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`

	// For types "audio" and "video"
	Codec string `json:"codec,omitempty"`

	// For type "audio"
	Stereo bool `json:"stereo,omitempty"`
	Fec    bool `json:"fec,omitempty"`
	Dtx    bool `json:"dtx,omitempty"`

	// For type "video"
	Simulcast bool `json:"simulcast,omitempty"`
	Svc       bool `json:"svc,omitempty"`

	ProfileH264 string `json:"h264_profile,omitempty"`
	ProfileVP9  string `json:"vp9_profile,omitempty"`

	ExtIdVideoOrientation int `json:"videoorient_ext_id,omitempty"`
	ExtIdPlayoutDelay     int `json:"playoutdelay_ext_id,omitempty"`
}

type RemotePublisherController interface {
	PublisherId() PublicSessionId

	StartPublishing(ctx context.Context, publisher McuRemotePublisherProperties) error
	StopPublishing(ctx context.Context, publisher McuRemotePublisherProperties) error
	GetStreams(ctx context.Context) ([]PublisherStream, error)
}

type RemoteMcu interface {
	NewRemotePublisher(ctx context.Context, listener McuListener, controller RemotePublisherController, streamType StreamType) (McuRemotePublisher, error)
	NewRemoteSubscriber(ctx context.Context, listener McuListener, publisher McuRemotePublisher) (McuRemoteSubscriber, error)
}

type StreamType string

const (
	StreamTypeAudio  StreamType = "audio"
	StreamTypeVideo  StreamType = "video"
	StreamTypeScreen StreamType = "screen"
)

func IsValidStreamType(s string) bool {
	switch s {
	case string(StreamTypeAudio):
		fallthrough
	case string(StreamTypeVideo):
		fallthrough
	case string(StreamTypeScreen):
		return true
	default:
		return false
	}
}

type McuClientBandwidthInfo struct {
	// Sent is the outgoing bandwidth.
	Sent api.Bandwidth
	// Received is the incoming bandwidth.
	Received api.Bandwidth
}

type McuClient interface {
	Id() string
	Sid() string
	StreamType() StreamType
	// MaxBitrate is the maximum allowed bitrate.
	MaxBitrate() api.Bandwidth

	Close(ctx context.Context)

	SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, api.StringMap))
}

type McuClientWithBandwidth interface {
	McuClient

	Bandwidth() *McuClientBandwidthInfo
}

type McuPublisher interface {
	McuClient

	PublisherId() PublicSessionId

	HasMedia(MediaType) bool
	SetMedia(MediaType)
}

type McuPublisherWithStreams interface {
	McuPublisher

	GetStreams(ctx context.Context) ([]PublisherStream, error)
}

type McuRemoteAwarePublisher interface {
	McuPublisher

	PublishRemote(ctx context.Context, remoteId PublicSessionId, hostname string, port int, rtcpPort int) error
	UnpublishRemote(ctx context.Context, remoteId PublicSessionId, hostname string, port int, rtcpPort int) error
}

type McuSubscriber interface {
	McuClient

	Publisher() PublicSessionId
}

type McuRemotePublisherProperties interface {
	Port() int
	RtcpPort() int
}

type McuRemotePublisher interface {
	McuClient

	McuRemotePublisherProperties
}

type McuRemoteSubscriber interface {
	McuSubscriber
}
