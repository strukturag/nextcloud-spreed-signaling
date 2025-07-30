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
	"log"
	"sync/atomic"

	"github.com/notedit/janus-go"
)

type mcuJanusRemotePublisher struct {
	mcuJanusPublisher

	ref atomic.Int64

	controller RemotePublisherController

	port     int
	rtcpPort int
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
			log.Printf("Remote publisher %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			log.Printf("Unsupported videoroom remote publisher event in %d: %+v", p.handleId, event)
		}
	} else {
		log.Printf("Unsupported remote publisher event in %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusRemotePublisher) handleHangup(event *janus.HangupMsg) {
	log.Printf("Remote publisher %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusRemotePublisher) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Remote publisher %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusRemotePublisher) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Remote publisher %d received connected", p.handleId)
	p.mcu.publisherConnected.Notify(getStreamId(p.id, p.streamType))
}

func (p *mcuJanusRemotePublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Remote publisher %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Remote publisher %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusRemotePublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, _, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType, p.settings)
	if err != nil {
		log.Printf("Could not reconnect remote publisher %s: %s", p.id, err)
		// TODO(jojo): Retry
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.session = session
	p.roomId = roomId

	log.Printf("Remote publisher %s reconnected on handle %d", p.id, p.handleId)
}

func (p *mcuJanusRemotePublisher) Close(ctx context.Context) {
	if !p.release() {
		return
	}

	if err := p.controller.StopPublishing(ctx, p); err != nil {
		log.Printf("Error stopping remote publisher %s in room %d: %s", p.id, p.roomId, err)
	}

	p.mu.Lock()
	if handle := p.handle; handle != nil {
		response, err := p.handle.Request(ctx, StringMap{
			"request": "remove_remote_publisher",
			"room":    p.roomId,
			"id":      streamTypeUserIds[p.streamType],
		})
		if err != nil {
			log.Printf("Error removing remote publisher %s in room %d: %s", p.id, p.roomId, err)
		} else {
			log.Printf("Removed remote publisher: %+v", response)
		}
		if p.roomId != 0 {
			destroy_msg := StringMap{
				"request": "destroy",
				"room":    p.roomId,
			}
			if _, err := handle.Request(ctx, destroy_msg); err != nil {
				log.Printf("Error destroying room %d: %s", p.roomId, err)
			} else {
				log.Printf("Room %d destroyed", p.roomId)
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
