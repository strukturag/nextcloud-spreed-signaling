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
	"strconv"

	"github.com/notedit/janus-go"
	"go.uber.org/zap"
)

type mcuJanusSubscriber struct {
	mcuJanusClient

	publisher string
}

func (p *mcuJanusSubscriber) Publisher() string {
	return p.publisher
}

func (p *mcuJanusSubscriber) updateLogger() {
	p.mcuJanusClient.updateLogger()
}

func (p *mcuJanusSubscriber) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			p.log.Info("Subscriber: associated room has been destroyed, closing")
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
			p.log.Warn("Unsupported videoroom event for subscriber",
				zap.String("videoroom", videoroom),
				zap.Any("event", event),
			)
		}
	} else {
		p.log.Warn("Unsupported event for subscriber",
			zap.Any("event", event),
		)
	}
}

func (p *mcuJanusSubscriber) handleHangup(event *janus.HangupMsg) {
	p.log.Info("Subscriber received hangup, closing",
		zap.String("reason", event.Reason),
	)
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleDetached(event *janus.DetachedMsg) {
	p.log.Info("Subscriber received detached, closing")
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleConnected(event *janus.WebRTCUpMsg) {
	p.log.Info("Subscriber received connected")
	p.mcu.SubscriberConnected(p.Id(), p.publisher, p.streamType)
}

func (p *mcuJanusSubscriber) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		p.log.Info("Subscriber is reporting lost packets on the uplink (Janus -> client)",
			zap.String("sessionid", p.listener.PublicId()),
			zap.Int64("lost", event.Lost),
		)
	} else {
		p.log.Info("Subscriber is reporting lost packets on the downlink (client -> Janus)",
			zap.String("sessionid", p.listener.PublicId()),
			zap.Int64("lost", event.Lost),
		)
	}
}

func (p *mcuJanusSubscriber) handleMedia(event *janus.MediaMsg) {
	// Only triggered for publishers
}

func (p *mcuJanusSubscriber) NotifyReconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
	defer cancel()
	handle, pub, err := p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
	if err != nil {
		// TODO(jojo): Retry?
		p.log.Error("Could not reconnect subscriber",
			zap.String("publisher", p.publisher),
			zap.Error(err),
		)
		p.Close(context.Background())
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.roomId = pub.roomId
	p.sid = strconv.FormatUint(handle.Id, 10)
	p.updateLogger()
	p.listener.SubscriberSidUpdated(p)
	p.log.Info("Subscriber reconnected",
		zap.String("publisher", p.publisher),
	)
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
				callback(fmt.Errorf("Already connected as subscriber for %s, error during re-joining: %s", p.streamType, err), nil)
				return
			}

			p.handle = handle
			p.handleId = handle.Id
			p.roomId = pub.roomId
			p.sid = strconv.FormatUint(handle.Id, 10)
			p.updateLogger()
			p.listener.SubscriberSidUpdated(p)
			p.closeChan = make(chan struct{}, 1)
			go p.run(p.handle, p.closeChan)
			p.log.Warn("Already connected subscriber, leaving and re-joining",
				zap.String("publisher", p.publisher),
				zap.Any("streamtype", p.streamType),
			)
			goto retry
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
			fallthrough
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
			switch error_code {
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
				p.log.Info("Publisher not created yet, wait and retry to join room as subscriber",
					zap.String("publisher", p.publisher),
					zap.Any("streamtype", p.streamType),
				)
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
				p.log.Info("Publisher not sending yet, wait and retry to join room as subscriber",
					zap.String("publisher", p.publisher),
					zap.Any("streamtype", p.streamType),
				)
			}

			if !loggedNotPublishingYet {
				loggedNotPublishingYet = true
				statsWaitingForPublisherTotal.WithLabelValues(string(p.streamType)).Inc()
			}

			if err := waiter.Wait(ctx); err != nil {
				callback(err, nil)
				return
			}
			p.log.Info("Retry subscribing",
				zap.String("publisher", p.publisher),
				zap.Any("streamtype", p.streamType),
			)
			goto retry
		default:
			// TODO(jojo): Should we handle other errors, too?
			callback(fmt.Errorf("Error joining room as subscriber: %+v", join_response), nil)
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
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
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
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendAnswer(msgctx, jsep_msg, callback)
			} else {
				go callback(fmt.Errorf("Answer message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
			} else {
				go callback(fmt.Errorf("Candidate message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
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
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.selectStream(msgctx, stream, callback)
		}
	default:
		// Return error asynchronously
		go callback(fmt.Errorf("Unsupported message type: %s", data.Type), nil)
	}
}
