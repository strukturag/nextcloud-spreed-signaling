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
	"sync/atomic"
	"testing"

	"github.com/notedit/janus-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

func TestGetFmtpValueH264(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	testcases := []struct {
		fmtp    string
		profile string
	}{
		{
			"",
			"",
		},
		{
			"level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f",
			"42001f",
		},
		{
			"level-asymmetry-allowed=1;packetization-mode=0",
			"",
		},
		{
			"level-asymmetry-allowed=1; packetization-mode=0; profile-level-id = 42001f",
			"42001f",
		},
	}

	for _, tc := range testcases {
		value, found := getFmtpValue(tc.fmtp, "profile-level-id")
		if !found && tc.profile != "" {
			assert.Fail("did not find profile", "profile \"%s\" in \"%s\"", tc.profile, tc.fmtp)
		} else if found && tc.profile == "" {
			assert.Fail("did not expect profile", "in \"%s\" but got \"%s\"", tc.fmtp, value)
		} else if found && tc.profile != value {
			assert.Fail("expected profile", "profile \"%s\" in \"%s\" but got \"%s\"", tc.profile, tc.fmtp, value)
		}
	}
}

func TestGetFmtpValueVP9(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	testcases := []struct {
		fmtp    string
		profile string
	}{
		{
			"",
			"",
		},
		{
			"profile-id=0",
			"0",
		},
		{
			"profile-id = 0",
			"0",
		},
	}

	for _, tc := range testcases {
		value, found := getFmtpValue(tc.fmtp, "profile-id")
		if !found && tc.profile != "" {
			assert.Fail("did not find profile", "profile \"%s\" in \"%s\"", tc.profile, tc.fmtp)
		} else if found && tc.profile == "" {
			assert.Fail("did not expect profile", "in \"%s\" but got \"%s\"", tc.fmtp, value)
		} else if found && tc.profile != value {
			assert.Fail("expected profile", "profile \"%s\" in \"%s\" but got \"%s\"", tc.profile, tc.fmtp, value)
		}
	}
}

func TestJanusPublisherRemote(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	var remotePublishId atomic.Value

	remoteId := PublicSessionId("the-remote-id")
	hostname := "remote.server"
	port := 12345
	rtcpPort := 23456

	mcu, gateway := newMcuJanusForTesting(t)
	gateway.registerHandlers(map[string]TestJanusHandler{
		"publish_remotely": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			if value, found := api.GetStringMapString[string](body, "host"); assert.True(found) {
				assert.Equal(hostname, value)
			}
			if value, found := api.GetStringMapEntry[float64](body, "port"); assert.True(found) {
				assert.EqualValues(port, value)
			}
			if value, found := api.GetStringMapEntry[float64](body, "rtcp_port"); assert.True(found) {
				assert.EqualValues(rtcpPort, value)
			}
			if value, found := api.GetStringMapString[string](body, "remote_id"); assert.True(found) {
				prev := remotePublishId.Swap(value)
				assert.Nil(prev, "should not have previous value")
			}

			return &janus.SuccessMsg{
				Data: janus.SuccessData{
					ID: 1,
				},
			}, nil
		},
		"unpublish_remotely": func(room *TestJanusRoom, body, jsep api.StringMap) (any, *janus.ErrorMsg) {
			if value, found := api.GetStringMapString[string](body, "remote_id"); assert.True(found) {
				if prev := remotePublishId.Load(); assert.NotNil(prev, "should have previous value") {
					assert.Equal(prev, value)
				}
			}
			return &janus.SuccessMsg{
				Data: janus.SuccessData{
					ID: 1,
				},
			}, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pubId := PublicSessionId("publisher-id")
	listener1 := &TestMcuListener{
		id: pubId,
	}

	settings1 := NewPublisherSettings{}
	initiator1 := &TestMcuInitiator{
		country: "DE",
	}

	pub, err := mcu.NewPublisher(ctx, listener1, pubId, "sid", StreamTypeVideo, settings1, initiator1)
	require.NoError(err)
	defer pub.Close(context.Background())

	require.Implements((*McuRemoteAwarePublisher)(nil), pub)
	remotePub, _ := pub.(McuRemoteAwarePublisher)

	if assert.NoError(remotePub.PublishRemote(ctx, remoteId, hostname, port, rtcpPort)) {
		assert.NoError(remotePub.UnpublishRemote(ctx, remoteId, hostname, port, rtcpPort))
	}
}
