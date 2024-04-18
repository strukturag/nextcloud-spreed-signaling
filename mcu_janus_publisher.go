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
	"fmt"
	"log"

	"github.com/notedit/janus-go"
)

type mcuJanusPublisher struct {
	mcuJanusClient

	id         string
	bitrate    int
	mediaTypes MediaType
	stats      publisherStatsCounter
}

func (p *mcuJanusPublisher) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			log.Printf("Publisher %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			log.Printf("Unsupported videoroom publisher event in %d: %+v", p.handleId, event)
		}
	} else {
		log.Printf("Unsupported publisher event in %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusPublisher) handleHangup(event *janus.HangupMsg) {
	log.Printf("Publisher %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusPublisher) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Publisher %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusPublisher) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Publisher %d received connected", p.handleId)
	p.mcu.publisherConnected.Notify(getStreamId(p.id, p.streamType))
}

func (p *mcuJanusPublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Publisher %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Publisher %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusPublisher) handleMedia(event *janus.MediaMsg) {
	mediaType := StreamType(event.Type)
	if mediaType == StreamTypeVideo && p.streamType == StreamTypeScreen {
		// We want to differentiate between audio, video and screensharing
		mediaType = p.streamType
	}

	p.stats.EnableStream(mediaType, event.Receiving)
}

func (p *mcuJanusPublisher) HasMedia(mt MediaType) bool {
	return (p.mediaTypes & mt) == mt
}

func (p *mcuJanusPublisher) SetMedia(mt MediaType) {
	p.mediaTypes = mt
}

func (p *mcuJanusPublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, _, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType, p.bitrate)
	if err != nil {
		log.Printf("Could not reconnect publisher %s: %s", p.id, err)
		// TODO(jojo): Retry
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.session = session
	p.roomId = roomId

	log.Printf("Publisher %s reconnected on handle %d", p.id, p.handleId)
}

func (p *mcuJanusPublisher) Close(ctx context.Context) {
	notify := false
	p.mu.Lock()
	if handle := p.handle; handle != nil && p.roomId != 0 {
		destroy_msg := map[string]interface{}{
			"request": "destroy",
			"room":    p.roomId,
		}
		if _, err := handle.Request(ctx, destroy_msg); err != nil {
			log.Printf("Error destroying room %d: %s", p.roomId, err)
		} else {
			log.Printf("Room %d destroyed", p.roomId)
		}
		p.mcu.mu.Lock()
		delete(p.mcu.publishers, getStreamId(p.id, p.streamType))
		p.mcu.mu.Unlock()
		p.roomId = 0
		notify = true
	}
	p.closeClient(ctx)
	p.mu.Unlock()

	p.stats.Reset()

	if notify {
		statsPublishersCurrent.WithLabelValues(string(p.streamType)).Dec()
		p.mcu.unregisterClient(p)
		p.listener.PublisherClosed(p)
	}
	p.mcuJanusClient.Close(ctx)
}

func (p *mcuJanusPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	statsMcuMessagesTotal.WithLabelValues(data.Type).Inc()
	jsep_msg := data.Payload
	switch data.Type {
	case "offer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			// TODO Tear down previous publisher and get a new one if sid does
			// not match?
			p.sendOffer(msgctx, jsep_msg, callback)
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
			} else {
				go callback(fmt.Errorf("Candidate message sid (%s) does not match publisher sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "endOfCandidates":
		// Ignore
	default:
		go callback(fmt.Errorf("Unsupported message type: %s", data.Type), nil)
	}
}

func (p *mcuJanusPublisher) PublishRemote(ctx context.Context, hostname string, port int, rtcpPort int) error {
	msg := map[string]interface{}{
		"request":      "publish_remotely",
		"room":         p.roomId,
		"publisher_id": streamTypeUserIds[p.streamType],
		"remote_id":    p.id,
		"host":         hostname,
		"port":         port,
		"rtcp_port":    rtcpPort,
	}
	response, err := p.handle.Request(ctx, msg)
	if err != nil {
		return err
	}

	errorMessage := getPluginStringValue(response.PluginData, pluginVideoRoom, "error")
	errorCode := getPluginIntValue(response.PluginData, pluginVideoRoom, "error_code")
	if errorMessage != "" || errorCode != 0 {
		if errorMessage == "" {
			errorMessage = "unknown error"
		}
		return fmt.Errorf("%s (%d)", errorMessage, errorCode)
	}

	log.Printf("Publishing %s to %s (port=%d, rtcpPort=%d)", p.id, hostname, port, rtcpPort)
	return nil
}
