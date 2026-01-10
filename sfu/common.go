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
package sfu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

const (
	TypeJanus = "janus"
	TypeProxy = "proxy"

	TypeDefault = TypeJanus
)

var (
	ErrNotConnected = errors.New("not connected")
)

type MediaType int

const (
	MediaTypeAudio  MediaType = 1 << 0
	MediaTypeVideo  MediaType = 1 << 1
	MediaTypeScreen MediaType = 1 << 2
)

type Listener interface {
	PublicId() api.PublicSessionId

	OnUpdateOffer(client Client, offer api.StringMap)

	OnIceCandidate(client Client, candidate any)
	OnIceCompleted(client Client)

	SubscriberSidUpdated(subscriber Subscriber)

	PublisherClosed(publisher Publisher)
	SubscriberClosed(subscriber Subscriber)
}

type Initiator interface {
	Country() geoip.Country
}

type Settings interface {
	MaxStreamBitrate() api.Bandwidth
	MaxScreenBitrate() api.Bandwidth
	Timeout() time.Duration

	Reload(config *goconf.ConfigFile)
}

type NewPublisherSettings struct {
	Bitrate    api.Bandwidth `json:"bitrate,omitempty"`
	MediaTypes MediaType     `json:"mediatypes,omitempty"`

	AudioCodec  string `json:"audiocodec,omitempty"`
	VideoCodec  string `json:"videocodec,omitempty"`
	VP9Profile  string `json:"vp9_profile,omitempty"`
	H264Profile string `json:"h264_profile,omitempty"`
}

type SFU interface {
	Start(ctx context.Context) error
	Stop()
	Reload(config *goconf.ConfigFile)

	SetOnConnected(func())
	SetOnDisconnected(func())

	GetStats() any
	GetServerInfoSfu() *talk.BackendServerInfoSfu
	GetBandwidthLimits() (api.Bandwidth, api.Bandwidth)

	NewPublisher(ctx context.Context, listener Listener, id api.PublicSessionId, sid string, streamType StreamType, settings NewPublisherSettings, initiator Initiator) (Publisher, error)
	NewSubscriber(ctx context.Context, listener Listener, publisher api.PublicSessionId, streamType StreamType, initiator Initiator) (Subscriber, error)
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
	PublisherId() api.PublicSessionId

	StartPublishing(ctx context.Context, publisher RemotePublisherProperties) error
	StopPublishing(ctx context.Context, publisher RemotePublisherProperties) error
	GetStreams(ctx context.Context) ([]PublisherStream, error)
}

type RemoteSfu interface {
	NewRemotePublisher(ctx context.Context, listener Listener, controller RemotePublisherController, streamType StreamType) (RemotePublisher, error)
	NewRemoteSubscriber(ctx context.Context, listener Listener, publisher RemotePublisher) (RemoteSubscriber, error)
}

type WithToken interface {
	CreateToken(subject string) (string, error)
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

type StreamId string

func GetStreamId(publisherId api.PublicSessionId, streamType StreamType) StreamId {
	return StreamId(fmt.Sprintf("%s|%s", publisherId, streamType))
}

type ClientBandwidthInfo struct {
	// Sent is the outgoing bandwidth.
	Sent api.Bandwidth
	// Received is the incoming bandwidth.
	Received api.Bandwidth
}

type Client interface {
	Id() string
	Sid() string
	StreamType() StreamType
	// MaxBitrate is the maximum allowed bitrate.
	MaxBitrate() api.Bandwidth

	Close(ctx context.Context)

	SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap))
}

type ClientWithBandwidth interface {
	Client

	Bandwidth() *ClientBandwidthInfo
}

type Publisher interface {
	Client

	PublisherId() api.PublicSessionId

	HasMedia(MediaType) bool
	SetMedia(MediaType)
}

type PublisherWithStreams interface {
	Publisher

	GetStreams(ctx context.Context) ([]PublisherStream, error)
}

type RemoteAwarePublisher interface {
	Publisher

	PublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error
	UnpublishRemote(ctx context.Context, remoteId api.PublicSessionId, hostname string, port int, rtcpPort int) error
}

type PublisherWithConnectionUrlAndIP interface {
	Publisher

	GetConnectionURL() (string, net.IP)
}

type Subscriber interface {
	Client

	Publisher() api.PublicSessionId
}

type SubscriberWithConnectionUrlAndIP interface {
	Subscriber

	GetConnectionURL() (string, net.IP)
}

type RemotePublisherProperties interface {
	Port() int
	RtcpPort() int
}

type RemotePublisher interface {
	Client

	RemotePublisherProperties
}

type RemoteSubscriber interface {
	Subscriber
}
