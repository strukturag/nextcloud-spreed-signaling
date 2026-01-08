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
package janus

import (
	"context"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
)

type janusClient struct {
	logger   log.Logger
	mcu      *janusSFU
	listener sfu.Listener
	mu       sync.Mutex

	id         uint64
	session    uint64
	roomId     uint64
	sid        string
	streamType sfu.StreamType
	maxBitrate api.Bandwidth

	// +checklocks:mu
	bandwidth map[string]*sfu.ClientBandwidthInfo

	handle    atomic.Pointer[janus.Handle]
	handleId  atomic.Uint64
	closeChan chan struct{}
	deferred  chan func()

	handleEvent     func(event *janus.EventMsg)
	handleHangup    func(event *janus.HangupMsg)
	handleDetached  func(event *janus.DetachedMsg)
	handleConnected func(event *janus.WebRTCUpMsg)
	handleSlowLink  func(event *janus.SlowLinkMsg)
	handleMedia     func(event *janus.MediaMsg)
}

func (c *janusClient) Id() string {
	return strconv.FormatUint(c.id, 10)
}

func (c *janusClient) Sid() string {
	return c.sid
}

func (c *janusClient) Handle() uint64 {
	return c.handleId.Load()
}

func (c *janusClient) StreamType() sfu.StreamType {
	return c.streamType
}

func (c *janusClient) MaxBitrate() api.Bandwidth {
	return c.maxBitrate
}

func (c *janusClient) Close(ctx context.Context) {
}

func (c *janusClient) SendMessage(ctx context.Context, message *api.MessageClientMessage, data *api.MessageClientMessageData, callback func(error, api.StringMap)) {
}

func (c *janusClient) UpdateBandwidth(media string, sent api.Bandwidth, received api.Bandwidth) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.bandwidth == nil {
		c.bandwidth = make(map[string]*sfu.ClientBandwidthInfo)
	}

	info, found := c.bandwidth[media]
	if !found {
		info = &sfu.ClientBandwidthInfo{}
		c.bandwidth[media] = info
	}

	info.Sent = sent
	info.Received = received
}

func (c *janusClient) Bandwidth() *sfu.ClientBandwidthInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.bandwidth == nil {
		return nil
	}

	result := &sfu.ClientBandwidthInfo{}
	for _, info := range c.bandwidth {
		result.Received += info.Received
		result.Sent += info.Sent
	}
	return result
}

func (c *janusClient) closeClient(ctx context.Context) bool {
	if handle := c.handle.Swap(nil); handle != nil {
		close(c.closeChan)
		if _, err := handle.Detach(ctx); err != nil {
			if e, ok := err.(*janus.ErrorMsg); !ok || e.Err.Code != janus.JANUS_ERROR_HANDLE_NOT_FOUND {
				c.logger.Println("Could not detach client", handle.Id, err)
			}
		}
		return true
	}

	return false
}

func (c *janusClient) run(handle *janus.Handle, closeChan <-chan struct{}) {
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
			case *janus.TrickleMsg:
				c.handleTrickle(t)
			default:
				c.logger.Println("Received unsupported event type", msg, reflect.TypeOf(msg))
			}
		case f := <-c.deferred:
			f()
		case <-closeChan:
			break loop
		}
	}
}

func (c *janusClient) sendOffer(ctx context.Context, offer api.StringMap, callback func(error, api.StringMap)) {
	handle := c.handle.Load()
	if handle == nil {
		callback(sfu.ErrNotConnected, nil)
		return
	}

	configure_msg := api.StringMap{
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

func (c *janusClient) sendAnswer(ctx context.Context, answer api.StringMap, callback func(error, api.StringMap)) {
	handle := c.handle.Load()
	if handle == nil {
		callback(sfu.ErrNotConnected, nil)
		return
	}

	start_msg := api.StringMap{
		"request": "start",
		"room":    c.roomId,
	}
	start_response, err := handle.Message(ctx, start_msg, answer)
	if err != nil {
		callback(err, nil)
		return
	}

	c.logger.Println("Started listener", start_response)
	callback(nil, nil)
}

func (c *janusClient) sendCandidate(ctx context.Context, candidate any, callback func(error, api.StringMap)) {
	handle := c.handle.Load()
	if handle == nil {
		callback(sfu.ErrNotConnected, nil)
		return
	}

	if _, err := handle.Trickle(ctx, candidate); err != nil {
		callback(err, nil)
		return
	}
	callback(nil, nil)
}

func (c *janusClient) handleTrickle(event *janus.TrickleMsg) {
	if event.Candidate.Completed {
		c.listener.OnIceCompleted(c)
	} else {
		c.listener.OnIceCandidate(c, event.Candidate)
	}
}

func (c *janusClient) selectStream(ctx context.Context, stream *streamSelection, callback func(error, api.StringMap)) {
	handle := c.handle.Load()
	if handle == nil {
		callback(sfu.ErrNotConnected, nil)
		return
	}

	if stream == nil || !stream.HasValues() {
		callback(nil, nil)
		return
	}

	configure_msg := api.StringMap{
		"request": "configure",
	}
	stream.AddToMessage(configure_msg)
	_, err := handle.Message(ctx, configure_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, nil)
}
