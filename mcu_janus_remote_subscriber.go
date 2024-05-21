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
	"strconv"
	"sync/atomic"

	"github.com/notedit/janus-go"
)

type mcuJanusRemoteSubscriber struct {
	mcuJanusSubscriber

	remote atomic.Pointer[mcuJanusRemotePublisher]
}

func (p *mcuJanusRemoteSubscriber) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			log.Printf("Remote subscriber %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "event":
			// Handle renegotiations, but ignore other events like selected
			// substream / temporal layer.
			if getPluginStringValue(event.Plugindata, pluginVideoRoom, "configured") == "ok" &&
				event.Jsep != nil && event.Jsep["type"] == "offer" && event.Jsep["sdp"] != nil {
				p.listener.OnUpdateOffer(p, event.Jsep)
			}
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			log.Printf("Unsupported videoroom event %s for remote subscriber %d: %+v", videoroom, p.handleId, event)
		}
	} else {
		log.Printf("Unsupported event for remote subscriber %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusRemoteSubscriber) handleHangup(event *janus.HangupMsg) {
	log.Printf("Remote subscriber %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusRemoteSubscriber) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Remote subscriber %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusRemoteSubscriber) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Remote subscriber %d received connected", p.handleId)
	p.mcu.SubscriberConnected(p.Id(), p.publisher, p.streamType)
}

func (p *mcuJanusRemoteSubscriber) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Remote subscriber %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Remote subscriber %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusRemoteSubscriber) handleMedia(event *janus.MediaMsg) {
	// Only triggered for publishers
}

func (p *mcuJanusRemoteSubscriber) NotifyReconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
	defer cancel()
	handle, pub, err := p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
	if err != nil {
		// TODO(jojo): Retry?
		log.Printf("Could not reconnect remote subscriber for publisher %s: %s", p.publisher, err)
		p.Close(context.Background())
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.roomId = pub.roomId
	p.sid = strconv.FormatUint(handle.Id, 10)
	p.listener.SubscriberSidUpdated(p)
	log.Printf("Subscriber %d for publisher %s reconnected on handle %d", p.id, p.publisher, p.handleId)
}

func (p *mcuJanusRemoteSubscriber) Close(ctx context.Context) {
	p.mcuJanusSubscriber.Close(ctx)

	if remote := p.remote.Swap(nil); remote != nil {
		remote.Close(context.Background())
	}
}
