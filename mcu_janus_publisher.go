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
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/notedit/janus-go"
	"github.com/pion/sdp/v3"
)

const (
	ExtensionUrlPlayoutDelay     = "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"
	ExtensionUrlVideoOrientation = "urn:3gpp:video-orientation"
)

const (
	sdpHasOffer  = 1
	sdpHasAnswer = 2
)

type mcuJanusPublisher struct {
	mcuJanusClient

	id         string
	bitrate    int
	mediaTypes MediaType
	stats      publisherStatsCounter
	sdpFlags   Flags
	sdpReady   *Closer
	offerSdp   atomic.Pointer[sdp.SessionDescription]
	answerSdp  atomic.Pointer[sdp.SessionDescription]
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
			if data.offerSdp == nil {
				// Should have been checked before.
				go callback(errors.New("No sdp found in offer"), nil)
				return
			}

			p.offerSdp.Store(data.offerSdp)
			p.sdpFlags.Add(sdpHasOffer)
			if p.sdpFlags.Get() == sdpHasAnswer|sdpHasOffer {
				p.sdpReady.Close()
			}

			// TODO Tear down previous publisher and get a new one if sid does
			// not match?
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.sendOffer(msgctx, jsep_msg, func(err error, jsep map[string]interface{}) {
				if err != nil {
					callback(err, jsep)
					return
				}

				sdpData, found := jsep["sdp"]
				if !found {
					log.Printf("No sdp found in answer %+v", jsep)
				} else {
					sdpString, ok := sdpData.(string)
					if !ok {
						log.Printf("Invalid sdp found in answer %+v", jsep)
					} else {
						var answerSdp sdp.SessionDescription
						if err := answerSdp.UnmarshalString(sdpString); err != nil {
							log.Printf("Error parsing answer sdp %+v: %s", sdpString, err)
							p.answerSdp.Store(nil)
							p.sdpFlags.Remove(sdpHasAnswer)
						} else {
							p.answerSdp.Store(&answerSdp)
							p.sdpFlags.Add(sdpHasAnswer)
							if p.sdpFlags.Get() == sdpHasAnswer|sdpHasOffer {
								p.sdpReady.Close()
							}
						}
					}
				}

				callback(nil, jsep)
			})
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

func getFmtpValue(fmtp string, key string) (string, bool) {
	parts := strings.Split(fmtp, ";")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		if strings.EqualFold(strings.TrimSpace(kv[0]), key) {
			return strings.TrimSpace(kv[1]), true
		}

	}
	return "", false
}

func (p *mcuJanusPublisher) GetStreams(ctx context.Context) ([]PublisherStream, error) {
	offerSdp := p.offerSdp.Load()
	answerSdp := p.answerSdp.Load()
	if offerSdp == nil || answerSdp == nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.sdpReady.C:
			offerSdp = p.offerSdp.Load()
			answerSdp = p.answerSdp.Load()
			if offerSdp == nil || answerSdp == nil {
				// Only can happen on invalid SDPs.
				return nil, errors.New("no offer and/or answer processed yet")
			}
		}
	}

	var streams []PublisherStream
	for idx, m := range answerSdp.MediaDescriptions {
		mid, found := m.Attribute(sdp.AttrKeyMID)
		if !found {
			continue
		}

		s := PublisherStream{
			Mid:    mid,
			Mindex: idx,
			Type:   m.MediaName.Media,
		}

		if len(m.MediaName.Formats) == 0 {
			continue
		}

		if strings.EqualFold(s.Type, "application") && strings.EqualFold(m.MediaName.Formats[0], "webrtc-datachannel") {
			s.Type = "data"
			streams = append(streams, s)
			continue
		}

		pt, err := strconv.ParseInt(m.MediaName.Formats[0], 10, 8)
		if err != nil {
			continue
		}

		answerCodec, err := answerSdp.GetCodecForPayloadType(uint8(pt))
		if err != nil {
			continue
		}

		if strings.EqualFold(s.Type, "audio") {
			s.Codec = answerCodec.Name
			if value, found := getFmtpValue(answerCodec.Fmtp, "useinbandfec"); found && value == "1" {
				s.Fec = true
			}
			if value, found := getFmtpValue(answerCodec.Fmtp, "usedtx"); found && value == "1" {
				s.Dtx = true
			}
			if value, found := getFmtpValue(answerCodec.Fmtp, "stereo"); found && value == "1" {
				s.Stereo = true
			}
		} else if strings.EqualFold(s.Type, "video") {
			s.Codec = answerCodec.Name
			// TODO: Determine if SVC is used.
			s.Svc = false

			if strings.EqualFold(answerCodec.Name, "vp9") {
				// Parse VP9 profile from "profile-id=XXX"
				// Exampe: "a=fmtp:98 profile-id=0"
				if profile, found := getFmtpValue(answerCodec.Fmtp, "profile-id"); found {
					s.ProfileVP9 = profile
				}
			} else if strings.EqualFold(answerCodec.Name, "h264") {
				// Parse H.264 profile from "profile-level-id=XXX"
				// Example: "a=fmtp:104 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f"
				if profile, found := getFmtpValue(answerCodec.Fmtp, "profile-level-id"); found {
					s.ProfileH264 = profile
				}
			}

			var extmap sdp.ExtMap
			for _, a := range m.Attributes {
				switch a.Key {
				case sdp.AttrKeyExtMap:
					if err := extmap.Unmarshal(extmap.Name() + ":" + a.Value); err != nil {
						log.Printf("Error parsing extmap %s: %s", a.Value, err)
						continue
					}

					switch extmap.URI.String() {
					case ExtensionUrlPlayoutDelay:
						s.ExtIdPlayoutDelay = extmap.Value
					case ExtensionUrlVideoOrientation:
						s.ExtIdVideoOrientation = extmap.Value
					}
				case "simulcast":
					s.Simulcast = true
				case sdp.AttrKeySSRCGroup:
					if strings.HasPrefix(a.Value, "SIM ") {
						s.Simulcast = true
					}
				}
			}

			for _, a := range offerSdp.MediaDescriptions[idx].Attributes {
				switch a.Key {
				case "simulcast":
					s.Simulcast = true
				case sdp.AttrKeySSRCGroup:
					if strings.HasPrefix(a.Value, "SIM ") {
						s.Simulcast = true
					}
				}
			}

		} else if strings.EqualFold(s.Type, "data") { // nolint
			// Already handled above.
		} else {
			log.Printf("Skip type %s", s.Type)
			continue
		}

		streams = append(streams, s)
	}

	return streams, nil
}

func getPublisherRemoteId(id string, remoteId string) string {
	return fmt.Sprintf("%s@%s", id, remoteId)
}

func (p *mcuJanusPublisher) PublishRemote(ctx context.Context, remoteId string, hostname string, port int, rtcpPort int) error {
	msg := map[string]interface{}{
		"request":      "publish_remotely",
		"room":         p.roomId,
		"publisher_id": streamTypeUserIds[p.streamType],
		"remote_id":    getPublisherRemoteId(p.id, remoteId),
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
		if errorCode == 0 {
			errorCode = 500
		}
		if errorMessage == "" {
			errorMessage = "unknown error"
		}

		return &janus.ErrorMsg{
			Err: janus.ErrorData{
				Code:   int(errorCode),
				Reason: errorMessage,
			},
		}
	}

	log.Printf("Publishing %s to %s (port=%d, rtcpPort=%d) for %s", p.id, hostname, port, rtcpPort, remoteId)
	return nil
}

func (p *mcuJanusPublisher) UnpublishRemote(ctx context.Context, remoteId string) error {
	msg := map[string]interface{}{
		"request":      "unpublish_remotely",
		"room":         p.roomId,
		"publisher_id": streamTypeUserIds[p.streamType],
		"remote_id":    getPublisherRemoteId(p.id, remoteId),
	}
	response, err := p.handle.Request(ctx, msg)
	if err != nil {
		return err
	}

	errorMessage := getPluginStringValue(response.PluginData, pluginVideoRoom, "error")
	errorCode := getPluginIntValue(response.PluginData, pluginVideoRoom, "error_code")
	if errorMessage != "" || errorCode != 0 {
		if errorCode == 0 {
			errorCode = 500
		}
		if errorMessage == "" {
			errorMessage = "unknown error"
		}

		return &janus.ErrorMsg{
			Err: janus.ErrorData{
				Code:   int(errorCode),
				Reason: errorMessage,
			},
		}
	}

	log.Printf("Unpublished remote %s for %s", p.id, remoteId)
	return nil
}
