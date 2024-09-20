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
package signaling

import (
	"context"
	"sync/atomic"

	"github.com/notedit/janus-go"
	"go.uber.org/zap"
)

type mcuJanusRemotePublisher struct {
	mcuJanusPublisher

	ref atomic.Int64

	port     int
	rtcpPort int
}

func (p *mcuJanusRemotePublisher) updateLogger() {
	p.mcuJanusPublisher.updateLogger()
}

func (p *mcuJanusRemotePublisher) addRef() int64 {
	return p.ref.Add(1)
}

func (p *mcuJanusRemotePublisher) release() bool {
	return p.ref.Add(-1) == 0
}

func (p *mcuJanusRemotePublisher) Port() int {
	return p.port
}

func (p *mcuJanusRemotePublisher) RtcpPort() int {
	return p.rtcpPort
}

func (p *mcuJanusRemotePublisher) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			p.log.Info("Remote publisher: associated room has been destroyed, closing")
			go p.Close(ctx)
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			p.log.Warn("Unsupported videoroom event for remote publisher",
				zap.String("videoroom", videoroom),
				zap.Any("event", event),
			)
		}
	} else {
		p.log.Warn("Unsupported remote publisher event",
			zap.Any("event", event),
		)
	}
}

func (p *mcuJanusRemotePublisher) handleHangup(event *janus.HangupMsg) {
	p.log.Info("Remote publisher received hangup, closing",
		zap.String("reason", event.Reason),
	)
	go p.Close(context.Background())
}

func (p *mcuJanusRemotePublisher) handleDetached(event *janus.DetachedMsg) {
	p.log.Info("Remote publisher received detached, closing")
	go p.Close(context.Background())
}

func (p *mcuJanusRemotePublisher) handleConnected(event *janus.WebRTCUpMsg) {
	p.log.Info("Remote publisher received connected")
	p.mcu.publisherConnected.Notify(getStreamId(p.id, p.streamType))
}

func (p *mcuJanusRemotePublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		p.log.Info("Remote publisher is reporting lost packets on the uplink (Janus -> client)",
			zap.String("sessionid", p.listener.PublicId()),
			zap.Int64("lost", event.Lost),
		)
	} else {
		p.log.Info("Remote publisher is reporting lost packets on the downlink (client -> Janus)",
			zap.String("sessionid", p.listener.PublicId()),
			zap.Int64("lost", event.Lost),
		)
	}
}

func (p *mcuJanusRemotePublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, _, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType, p.bitrate)
	if err != nil {
		p.log.Error("Could not reconnect remote publisher",
			zap.Error(err),
		)
		// TODO(jojo): Retry
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.session = session
	p.roomId = roomId
	p.updateLogger()

	p.log.Info("Remote publisher reconnected")
}

func (p *mcuJanusRemotePublisher) Close(ctx context.Context) {
	if !p.release() {
		return
	}

	p.mu.Lock()
	if handle := p.handle; handle != nil {
		response, err := p.handle.Request(ctx, map[string]interface{}{
			"request": "remove_remote_publisher",
			"room":    p.roomId,
			"id":      streamTypeUserIds[p.streamType],
		})
		if err != nil {
			p.log.Error("Error removing remote publisher",
				zap.Error(err),
			)
		} else {
			p.log.Info("Removed remote publisher",
				zap.Any("response", response),
			)
		}
		if p.roomId != 0 {
			destroy_msg := map[string]interface{}{
				"request": "destroy",
				"room":    p.roomId,
			}
			if _, err := handle.Request(ctx, destroy_msg); err != nil {
				p.log.Error("Error destroying room for remote publisher",
					zap.Error(err),
				)
			} else {
				p.log.Info("Room for remote publisher destroyed")
			}
			p.mcu.mu.Lock()
			delete(p.mcu.remotePublishers, getStreamId(p.id, p.streamType))
			p.mcu.mu.Unlock()
			p.roomId = 0
		}
	}

	p.closeClient(ctx)
	p.mu.Unlock()
}
