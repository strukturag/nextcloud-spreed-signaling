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
	"reflect"
	"strconv"
	"sync"

	"github.com/notedit/janus-go"
	"go.uber.org/zap"
)

type mcuJanusClient struct {
	baseLog  *zap.Logger
	log      *zap.Logger
	mcu      *mcuJanus
	listener McuListener
	mu       sync.Mutex // nolint

	id         uint64
	session    uint64
	roomId     uint64
	sid        string
	streamType StreamType
	maxBitrate int

	handle    *JanusHandle
	handleId  uint64
	closeChan chan struct{}
	deferred  chan func()

	handleEvent     func(event *janus.EventMsg)
	handleHangup    func(event *janus.HangupMsg)
	handleDetached  func(event *janus.DetachedMsg)
	handleConnected func(event *janus.WebRTCUpMsg)
	handleSlowLink  func(event *janus.SlowLinkMsg)
	handleMedia     func(event *janus.MediaMsg)
}

func (c *mcuJanusClient) updateLogger() {
	c.log = c.baseLog.With(
		zap.Uint64("id", c.id),
		zap.Uint64("handle", c.handleId),
		zap.Uint64("roomid", c.roomId),
		zap.Any("streamtype", c.streamType),
	)
}

func (c *mcuJanusClient) Id() string {
	return strconv.FormatUint(c.id, 10)
}

func (c *mcuJanusClient) Sid() string {
	return c.sid
}

func (c *mcuJanusClient) StreamType() StreamType {
	return c.streamType
}

func (c *mcuJanusClient) MaxBitrate() int {
	return c.maxBitrate
}

func (c *mcuJanusClient) Close(ctx context.Context) {
}

func (c *mcuJanusClient) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
}

func (c *mcuJanusClient) closeClient(ctx context.Context) bool {
	if handle := c.handle; handle != nil {
		c.handle = nil
		close(c.closeChan)
		if _, err := handle.Detach(ctx); err != nil {
			if e, ok := err.(*janus.ErrorMsg); !ok || e.Err.Code != JANUS_ERROR_HANDLE_NOT_FOUND {
				c.log.Error("Could not detach client",
					zap.Uint64("handle", handle.Id),
					zap.Error(err),
				)
			}
		}
		return true
	}

	return false
}

func (c *mcuJanusClient) run(handle *JanusHandle, closeChan <-chan struct{}) {
loop:
	for {
		select {
		case msg := <-handle.Events:
			switch t := msg.(type) {
			case *janus.EventMsg:
				c.handleEvent(t)
			case *janus.HangupMsg:
				c.handleHangup(t)
			case *janus.DetachedMsg:
				c.handleDetached(t)
			case *janus.MediaMsg:
				c.handleMedia(t)
			case *janus.WebRTCUpMsg:
				c.handleConnected(t)
			case *janus.SlowLinkMsg:
				c.handleSlowLink(t)
			case *TrickleMsg:
				c.handleTrickle(t)
			default:
				c.log.Error("Received unsupported event type",
					zap.Any("message", msg),
					zap.Any("type", reflect.TypeOf(msg)),
				)
			}
		case f := <-c.deferred:
			f()
		case <-closeChan:
			break loop
		}
	}
}

func (c *mcuJanusClient) sendOffer(ctx context.Context, offer map[string]interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
		"audio":   true,
		"video":   true,
		"data":    true,
	}
	answer_msg, err := handle.Message(ctx, configure_msg, offer)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, answer_msg.Jsep)
}

func (c *mcuJanusClient) sendAnswer(ctx context.Context, answer map[string]interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	start_msg := map[string]interface{}{
		"request": "start",
		"room":    c.roomId,
	}
	start_response, err := handle.Message(ctx, start_msg, answer)
	if err != nil {
		callback(err, nil)
		return
	}
	c.log.Info("Started listener",
		zap.Any("response", start_response),
	)
	callback(nil, nil)
}

func (c *mcuJanusClient) sendCandidate(ctx context.Context, candidate interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	if _, err := handle.Trickle(ctx, candidate); err != nil {
		callback(err, nil)
		return
	}
	callback(nil, nil)
}

func (c *mcuJanusClient) handleTrickle(event *TrickleMsg) {
	if event.Candidate.Completed {
		c.listener.OnIceCompleted(c)
	} else {
		c.listener.OnIceCandidate(c, event.Candidate)
	}
}

func (c *mcuJanusClient) selectStream(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	if stream == nil || !stream.HasValues() {
		callback(nil, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
	}
	if stream != nil {
		stream.AddToMessage(configure_msg)
	}
	_, err := handle.Message(ctx, configure_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, nil)
}
