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
	"sync/atomic"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
)

type janusRemotePublisher struct {
	janusPublisher

	ref atomic.Int64

	controller sfu.RemotePublisherController

	port     int
	rtcpPort int
}

func (p *janusRemotePublisher) addRef() int64 {
	return p.ref.Add(1)
}

func (p *janusRemotePublisher) release() bool {
	return p.ref.Add(-1) == 0
}

func (p *janusRemotePublisher) Port() int {
	return p.port
}

func (p *janusRemotePublisher) RtcpPort() int {
	return p.rtcpPort
}

func (p *janusRemotePublisher) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			p.logger.Printf("Remote publisher %d: associated room has been destroyed, closing", p.handleId.Load())
			go p.Close(ctx)
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			p.logger.Printf("Unsupported videoroom remote publisher event in %d: %+v", p.handleId.Load(), event)
		}
	} else {
		p.logger.Printf("Unsupported remote publisher event in %d: %+v", p.handleId.Load(), event)
	}
}

func (p *janusRemotePublisher) handleHangup(event *janus.HangupMsg) {
	p.logger.Printf("Remote publisher %d received hangup (%s), closing", p.handleId.Load(), event.Reason)
	go p.Close(context.Background())
}

func (p *janusRemotePublisher) handleDetached(event *janus.DetachedMsg) {
	p.logger.Printf("Remote publisher %d received detached, closing", p.handleId.Load())
	go p.Close(context.Background())
}

func (p *janusRemotePublisher) handleConnected(event *janus.WebRTCUpMsg) {
	p.logger.Printf("Remote publisher %d received connected", p.handleId.Load())
	p.mcu.publisherConnected.Notify(string(sfu.GetStreamId(p.id, p.streamType)))
}

func (p *janusRemotePublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		p.logger.Printf("Remote publisher %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId.Load(), event.Lost)
	} else {
		p.logger.Printf("Remote publisher %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId.Load(), event.Lost)
	}
}

func (p *janusRemotePublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, _, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType, p.settings)
	if err != nil {
		p.logger.Printf("Could not reconnect remote publisher %s: %s", p.id, err)
		// TODO(jojo): Retry
		return
	}

	if prev := p.handle.Swap(handle); prev != nil {
		if _, err := prev.Detach(context.Background()); err != nil {
			p.logger.Printf("Error detaching old remote publisher handle %d: %s", prev.Id, err)
		}
	}
	p.handleId.Store(handle.Id)
	p.session = session
	p.roomId = roomId

	p.logger.Printf("Remote publisher %s reconnected on handle %d", p.id, p.handleId.Load())
}

func (p *janusRemotePublisher) Close(ctx context.Context) {
	if !p.release() {
		return
	}

	if err := p.controller.StopPublishing(ctx, p); err != nil {
		p.logger.Printf("Error stopping remote publisher %s in room %d: %s", p.id, p.roomId, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if handle := p.handle.Load(); handle != nil {
		response, err := handle.Request(ctx, api.StringMap{
			"request": "remove_remote_publisher",
			"room":    p.roomId,
			"id":      streamTypeUserIds[p.streamType],
		})
		if err != nil {
			p.logger.Printf("Error removing remote publisher %s in room %d: %s", p.id, p.roomId, err)
		} else {
			p.logger.Printf("Removed remote publisher: %+v", response)
		}
		if p.roomId != 0 {
			destroy_msg := api.StringMap{
				"request": "destroy",
				"room":    p.roomId,
			}
			if _, err := handle.Request(ctx, destroy_msg); err != nil {
				p.logger.Printf("Error destroying room %d: %s", p.roomId, err)
			} else {
				p.logger.Printf("Room %d destroyed", p.roomId)
			}
			p.mcu.mu.Lock()
			delete(p.mcu.remotePublishers, sfu.GetStreamId(p.id, p.streamType))
			p.mcu.mu.Unlock()
			p.roomId = 0
		}
	}

	p.closeClient(ctx)
}
