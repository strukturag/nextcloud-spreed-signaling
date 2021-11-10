package signaling

const (
	// See https://tools.ietf.org/id/draft-ietf-rtcweb-sdp-08.html#rfc.section.5.2.1
	MockSdpOfferAudioOnly = `v=0
o=- 20518 0 IN IP4 0.0.0.0
s=-
t=0 0
a=group:BUNDLE audio-D.ietf-mmusic-sdp-bundle-negotiation
a=ice-options:trickle-D.ietf-mmusic-trickle-ice
m=audio 54609 UDP/TLS/RTP/SAVPF 109 0 8
c=IN IP4 192.168.0.1
a=mid:audio
a=msid:ma ta
a=sendrecv
a=rtpmap:109 opus/48000/2
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=maxptime:120
a=ice-ufrag:074c6550
a=ice-pwd:a28a397a4c3f31747d1ee3474af08a068
a=fingerprint:sha-256 19:E2:1C:3B:4B:9F:81:E6:B8:5C:F4:A5:A8:D8:73:04:BB:05:2F:70:9F:04:A9:0E:05:E9:26:33:E8:70:88:A2
a=setup:actpass
a=tls-id:1
a=rtcp-mux
a=rtcp:60065 IN IP4 192.168.0.1
a=rtcp-rsize
a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
a=candidate:0 1 UDP 2122194687 192.0.2.4 61665 typ host
a=candidate:1 1 UDP 1685987071 192.168.0.1 54609 typ srflx raddr 192.0.2.4 rport 61665
a=candidate:0 2 UDP 2122194687 192.0.2.4 61667 typ host
a=candidate:1 2 UDP 1685987071 192.168.0.1 60065 typ srflx raddr 192.0.2.4 rport 61667
a=end-of-candidates
`
	MockSdpAnswerAudioOnly = `v=0
o=- 16833 0 IN IP4 0.0.0.0
s=-
t=0 0
a=group:BUNDLE audio
a=ice-options:trickle
m=audio 49203 UDP/TLS/RTP/SAVPF 109 0 8
c=IN IP4 192.168.0.1
a=mid:audio
a=msid:ma ta
a=sendrecv
a=rtpmap:109 opus/48000/2
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=maxptime:120
a=ice-ufrag:05067423
a=ice-pwd:1747d1ee3474a28a397a4c3f3af08a068
a=fingerprint:sha-256 6B:8B:F0:65:5F:78:E2:51:3B:AC:6F:F3:3F:46:1B:35:DC:B8:5F:64:1A:24:C2:43:F0:A1:58:D0:A1:2C:19:08
a=setup:active
a=tls-id:1
a=rtcp-mux
a=rtcp-rsize
a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
a=candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host
a=candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556
a=end-of-candidates
`

	// See https://tools.ietf.org/id/draft-ietf-rtcweb-sdp-08.html#rfc.section.5.2.2.1
	MockSdpOfferAudioAndVideo = `v=0
o=- 20518 0 IN IP4 0.0.0.0
s=-
t=0 0
a=group:BUNDLE audio-D.ietf-mmusic-sdp-bundle-negotiation
a=ice-options:trickle-D.ietf-mmusic-trickle-ice
m=audio 54609 UDP/TLS/RTP/SAVPF 109 0 8
c=IN IP4 192.168.0.1
a=mid:audio
a=msid:ma ta
a=sendrecv
a=rtpmap:109 opus/48000/2
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=maxptime:120
a=ice-ufrag:074c6550
a=ice-pwd:a28a397a4c3f31747d1ee3474af08a068
a=fingerprint:sha-256 19:E2:1C:3B:4B:9F:81:E6:B8:5C:F4:A5:A8:D8:73:04:BB:05:2F:70:9F:04:A9:0E:05:E9:26:33:E8:70:88:A2
a=setup:actpass
a=tls-id:1
a=rtcp-mux
a=rtcp:60065 IN IP4 192.168.0.1
a=rtcp-rsize
a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
a=candidate:0 1 UDP 2122194687 192.0.2.4 61665 typ host
a=candidate:1 1 UDP 1685987071 192.168.0.1 54609 typ srflx raddr 192.0.2.4 rport 61665
a=candidate:0 2 UDP 2122194687 192.0.2.4 61667 typ host
a=candidate:1 2 UDP 1685987071 192.168.0.1 60065 typ srflx raddr 192.0.2.4 rport 61667
a=end-of-candidates
m=video 54609 UDP/TLS/RTP/SAVPF 99 120
c=IN IP4 192.168.0.1
a=mid:video
a=msid:ma tb
a=sendrecv
a=rtpmap:99 H264/90000
a=fmtp:99 profile-level-id=4d0028;packetization-mode=1
a=rtpmap:120 VP8/90000
a=rtcp-fb:99 nack
a=rtcp-fb:99 nack pli
a=rtcp-fb:99 ccm fir
a=rtcp-fb:120 nack
a=rtcp-fb:120 nack pli
a=rtcp-fb:120 ccm fir
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
`
	MockSdpAnswerAudioAndVideo = `v=0
o=- 16833 0 IN IP4 0.0.0.0
s=-
t=0 0
a=group:BUNDLE audio
a=ice-options:trickle
m=audio 49203 UDP/TLS/RTP/SAVPF 109 0 8
c=IN IP4 192.168.0.1
a=mid:audio
a=msid:ma ta
a=sendrecv
a=rtpmap:109 opus/48000/2
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=maxptime:120
a=ice-ufrag:05067423
a=ice-pwd:1747d1ee3474a28a397a4c3f3af08a068
a=fingerprint:sha-256 6B:8B:F0:65:5F:78:E2:51:3B:AC:6F:F3:3F:46:1B:35:DC:B8:5F:64:1A:24:C2:43:F0:A1:58:D0:A1:2C:19:08
a=setup:active
a=tls-id:1
a=rtcp-mux
a=rtcp-rsize
a=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
a=candidate:0 1 UDP 2122194687 198.51.100.7 51556 typ host
a=candidate:1 1 UDP 1685987071 192.168.0.1 49203 typ srflx raddr 198.51.100.7 rport 51556
a=end-of-candidates
m=video 49203 UDP/TLS/RTP/SAVPF 99
c=IN IP4 192.168.0.1
a=mid:video
a=msid:ma tb
a=sendrecv
a=rtpmap:99 H264/90000
a=fmtp:99 profile-level-id=4d0028;packetization-mode=1
a=rtcp-fb:99 nack
a=rtcp-fb:99 nack pli
a=rtcp-fb:99 ccm fir
a=extmap:2 urn:ietf:params:rtp-hdrext:sdes:mid
`
)
