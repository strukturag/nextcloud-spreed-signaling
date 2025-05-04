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
	"strconv"

	"github.com/notedit/janus-go"
)

type mcuJanusSubscriber struct {
	mcuJanusClient

	publisher string
}

func (p *mcuJanusSubscriber) Publisher() string {
	return p.publisher
}

func (p *mcuJanusSubscriber) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			log.Printf("Subscriber %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "updated":
			streams, ok := getPluginValue(event.Plugindata, pluginVideoRoom, "streams").([]interface{})
			if !ok || len(streams) == 0 {
				// The streams list will be empty if no stream was changed.
				return
			}

			for _, stream := range streams {
				if stream, ok := stream.(map[string]interface{}); ok {
					if (stream["type"] == "audio" || stream["type"] == "video") && stream["active"] != false {
						return
					}
				}
			}

			log.Printf("Subscriber %d: received updated event with no active media streams, closing", p.handleId)
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
			log.Printf("Unsupported videoroom event %s for subscriber %d: %+v", videoroom, p.handleId, event)
		}
	} else {
		log.Printf("Unsupported event for subscriber %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusSubscriber) handleHangup(event *janus.HangupMsg) {
	log.Printf("Subscriber %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Subscriber %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Subscriber %d received connected", p.handleId)
	p.mcu.SubscriberConnected(p.Id(), p.publisher, p.streamType)
}

func (p *mcuJanusSubscriber) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Subscriber %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Subscriber %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusSubscriber) handleMedia(event *janus.MediaMsg) {
	// Only triggered for publishers
}

func (p *mcuJanusSubscriber) NotifyReconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), p.mcu.settings.Timeout())
	defer cancel()
	handle, pub, err := p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
	if err != nil {
		// TODO(jojo): Retry?
		log.Printf("Could not reconnect subscriber for publisher %s: %s", p.publisher, err)
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

func (p *mcuJanusSubscriber) Close(ctx context.Context) {
	p.mu.Lock()
	closed := p.closeClient(ctx)
	p.mu.Unlock()

	if closed {
		p.mcu.SubscriberDisconnected(p.Id(), p.publisher, p.streamType)
		statsSubscribersCurrent.WithLabelValues(string(p.streamType)).Dec()
	}
	p.mcu.unregisterClient(p)
	p.listener.SubscriberClosed(p)
	p.mcuJanusClient.Close(ctx)
}

func (p *mcuJanusSubscriber) joinRoom(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := p.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	waiter := p.mcu.publisherConnected.NewWaiter(getStreamId(p.publisher, p.streamType))
	defer p.mcu.publisherConnected.Release(waiter)

	loggedNotPublishingYet := false
retry:
	join_msg := map[string]interface{}{
		"request": "join",
		"ptype":   "subscriber",
		"room":    p.roomId,
	}
	if p.mcu.isMultistream() {
		join_msg["streams"] = []map[string]interface{}{
			{
				"feed": streamTypeUserIds[p.streamType],
			},
		}
	} else {
		join_msg["feed"] = streamTypeUserIds[p.streamType]
	}
	if stream != nil {
		stream.AddToMessage(join_msg)
	}
	join_response, err := handle.Message(ctx, join_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	if error_code := getPluginIntValue(join_response.Plugindata, pluginVideoRoom, "error_code"); error_code > 0 {
		switch error_code {
		case JANUS_VIDEOROOM_ERROR_ALREADY_JOINED:
			// The subscriber is already connected to the room. This can happen
			// if a client leaves a call but keeps the subscriber objects active.
			// On joining the call again, the subscriber tries to join on the
			// MCU which will fail because he is still connected.
			// To get a new Offer SDP, we have to tear down the session on the
			// MCU and join again.
			p.mu.Lock()
			p.closeClient(ctx)
			p.mu.Unlock()

			var pub *mcuJanusPublisher
			handle, pub, err = p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
			if err != nil {
				// Reconnection didn't work, need to unregister/remove subscriber
				// so a new object will be created if the request is retried.
				p.mcu.unregisterClient(p)
				p.listener.SubscriberClosed(p)
				callback(fmt.Errorf("already connected as subscriber for %s, error during re-joining: %s", p.streamType, err), nil)
				return
			}

			p.handle = handle
			p.handleId = handle.Id
			p.roomId = pub.roomId
			p.sid = strconv.FormatUint(handle.Id, 10)
			p.listener.SubscriberSidUpdated(p)
			p.closeChan = make(chan struct{}, 1)
			go p.run(p.handle, p.closeChan)
			log.Printf("Already connected subscriber %d for %s, leaving and re-joining on handle %d", p.id, p.streamType, p.handleId)
			goto retry
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
			fallthrough
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
			switch error_code {
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
				log.Printf("Publisher %s not created yet for %s, not joining room %d as subscriber", p.publisher, p.streamType, p.roomId)
				p.listener.SubscriberClosed(p)
				callback(fmt.Errorf("Publisher %s not created yet for %s", p.publisher, p.streamType), nil)
				return
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
				log.Printf("Publisher %s not sending yet for %s, wait and retry to join room %d as subscriber", p.publisher, p.streamType, p.roomId)
			}

			if !loggedNotPublishingYet {
				loggedNotPublishingYet = true
				statsWaitingForPublisherTotal.WithLabelValues(string(p.streamType)).Inc()
			}

			if err := waiter.Wait(ctx); err != nil {
				p.listener.SubscriberClosed(p)
				callback(err, nil)
				return
			}
			log.Printf("Retry subscribing %s from %s", p.streamType, p.publisher)
			goto retry
		default:
			// TODO(jojo): Should we handle other errors, too?
			callback(fmt.Errorf("error joining room as subscriber: %+v", join_response), nil)
			return
		}
	}
	//log.Println("Joined as listener", join_response)

	p.session = join_response.Session
	callback(nil, join_response.Jsep)
}

func (p *mcuJanusSubscriber) update(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := p.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
		"update":  true,
	}
	if stream != nil {
		stream.AddToMessage(configure_msg)
	}
	configure_response, err := handle.Message(ctx, configure_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, configure_response.Jsep)
}

func (p *mcuJanusSubscriber) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	statsMcuMessagesTotal.WithLabelValues(data.Type).Inc()
	jsep_msg := data.Payload
	switch data.Type {
	case "requestoffer":
		fallthrough
	case "sendoffer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.settings.Timeout())
			defer cancel()

			stream, err := parseStreamSelection(jsep_msg)
			if err != nil {
				go callback(err, nil)
				return
			}

			if data.Sid == "" || data.Sid != p.Sid() {
				p.joinRoom(msgctx, stream, callback)
			} else {
				p.update(msgctx, stream, callback)
			}
		}
	case "answer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.settings.Timeout())
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendAnswer(msgctx, jsep_msg, callback)
			} else {
				go callback(fmt.Errorf("answer message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.settings.Timeout())
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
			} else {
				go callback(fmt.Errorf("candidate message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "endOfCandidates":
		// Ignore
	case "selectStream":
		stream, err := parseStreamSelection(jsep_msg)
		if err != nil {
			go callback(err, nil)
			return
		}

		if stream == nil || !stream.HasValues() {
			// Nothing to do
			go callback(nil, nil)
			return
		}

		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.settings.Timeout())
			defer cancel()

			p.selectStream(msgctx, stream, callback)
		}
	default:
		// Return error asynchronously
		go callback(fmt.Errorf("unsupported message type: %s", data.Type), nil)
	}
}
