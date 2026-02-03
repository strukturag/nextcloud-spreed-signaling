/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package proxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/mock"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	testcases := []struct {
		message *ClientMessage
		reason  string
	}{
		{
			&ClientMessage{},
			"type missing",
		},
		{
			// Unknown types are ignored.
			&ClientMessage{
				Type: "invalid",
			},
			"",
		},
		{
			&ClientMessage{
				Type: "hello",
			},
			"hello missing",
		},
		{
			&ClientMessage{
				Type:  "hello",
				Hello: &HelloClientMessage{},
			},
			"unsupported hello version",
		},
		{
			&ClientMessage{
				Type: "hello",
				Hello: &HelloClientMessage{
					Version: "abc",
				},
			},
			"unsupported hello version",
		},
		{
			&ClientMessage{
				Type: "hello",
				Hello: &HelloClientMessage{
					Version: "1.0",
				},
			},
			"token missing",
		},
		{
			&ClientMessage{
				Type: "hello",
				Hello: &HelloClientMessage{
					Version: "1.0",
					Token:   "token",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "hello",
				Hello: &HelloClientMessage{
					Version:  "1.0",
					ResumeId: "resume-id",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "bye",
			},
			"",
		},
		{
			&ClientMessage{
				Type: "bye",
				Bye:  &ByeClientMessage{},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
			},
			"command missing",
		},
		{
			&ClientMessage{
				Type:    "command",
				Command: &CommandClientMessage{},
			},
			"type missing",
		},
		{
			// Unknown types are ignored.
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type: "invalid",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type: "create-publisher",
				},
			},
			"stream type missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:       "create-publisher",
					StreamType: sfu.StreamTypeVideo,
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type: "create-subscriber",
				},
			},
			"publisher id missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:        "create-subscriber",
					PublisherId: "foo",
				},
			},
			"stream type missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:        "create-subscriber",
					PublisherId: "foo",
					StreamType:  sfu.StreamTypeVideo,
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:        "create-subscriber",
					PublisherId: "foo",
					StreamType:  sfu.StreamTypeVideo,
					RemoteUrl:   "http://domain.invalid",
				},
			},
			"remote token missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:        "create-subscriber",
					PublisherId: "foo",
					StreamType:  sfu.StreamTypeVideo,
					RemoteUrl:   ":",
					RemoteToken: "remote-token",
				},
			},
			"invalid remote url",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:        "create-subscriber",
					PublisherId: "foo",
					StreamType:  sfu.StreamTypeVideo,
					RemoteUrl:   "http://domain.invalid",
					RemoteToken: "remote-token",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type: "delete-publisher",
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:     "delete-publisher",
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type: "delete-subscriber",
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "command",
				Command: &CommandClientMessage{
					Type:     "delete-subscriber",
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "payload",
			},
			"payload missing",
		},
		{
			&ClientMessage{
				Type:    "payload",
				Payload: &PayloadClientMessage{},
			},
			"type missing",
		},
		{
			// Unknown types are ignored.
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "invalid",
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "offer",
				},
			},
			"payload missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "offer",
					Payload: api.StringMap{
						"sdp": mock.MockSdpOfferAudioAndVideo,
					},
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "offer",
					Payload: api.StringMap{
						"sdp": mock.MockSdpOfferAudioAndVideo,
					},
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "answer",
				},
			},
			"payload missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "answer",
					Payload: api.StringMap{
						"sdp": mock.MockSdpAnswerAudioAndVideo,
					},
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "answer",
					Payload: api.StringMap{
						"sdp": mock.MockSdpAnswerAudioAndVideo,
					},
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "candidate",
				},
			},
			"payload missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "candidate",
					Payload: api.StringMap{
						"candidate": "invalid-candidate",
					},
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "candidate",
					Payload: api.StringMap{
						"candidate": "invalid-candidate",
					},
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "endOfCandidates",
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type:     "endOfCandidates",
					ClientId: "foo",
				},
			},
			"",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type: "requestoffer",
				},
			},
			"client id missing",
		},
		{
			&ClientMessage{
				Type: "payload",
				Payload: &PayloadClientMessage{
					Type:     "requestoffer",
					ClientId: "foo",
				},
			},
			"",
		},
	}

	for idx, tc := range testcases {
		err := tc.message.CheckValid()
		if tc.reason == "" {
			assert.NoError(err, "failed for testcase %d: %+v", idx, tc.message)
		} else {
			assert.ErrorContains(err, tc.reason, "failed for testcase %d: %+v", idx, tc.message)
		}
	}
}

func TestServerErrorMessage(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	message := &ClientMessage{
		Id: "12346",
	}
	err := message.NewErrorServerMessage(api.NewError("error_code", "Test error"))
	assert.Equal(message.Id, err.Id)
	if e := err.Error; assert.NotNil(e) {
		assert.Equal("error_code", e.Code)
		assert.Equal("Test error", e.Message)
		assert.Empty(e.Details)
	}
}

func TestWrapperServerErrorMessage(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	message := &ClientMessage{
		Id: "12346",
	}
	err := message.NewWrappedErrorServerMessage(errors.New("an internal server error"))
	assert.Equal(message.Id, err.Id)
	if e := err.Error; assert.NotNil(e) {
		assert.Equal("internal_error", e.Code)
		assert.Equal("an internal server error", e.Message)
		assert.Empty(e.Details)
	}
}

func TestCloseAfterSend(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	message := &ServerMessage{
		Type: "bye",
	}
	assert.True(message.CloseAfterSend(nil))

	for _, msgType := range []string{
		"error",
		"hello",
		"command",
		"payload",
		"event",
	} {
		message = &ServerMessage{
			Type: msgType,
		}
		assert.False(message.CloseAfterSend(nil), "failed for %s", msgType)
	}
}

func TestAllowIncoming(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	testcases := []struct {
		bw    float64
		allow bool
	}{
		{
			0, true,
		},
		{
			99, true,
		},
		{
			99.9, true,
		},
		{
			100, false,
		},
		{
			200, false,
		},
	}

	bw := EventServerBandwidth{
		Incoming: nil,
	}
	assert.True(bw.AllowIncoming())
	for idx, tc := range testcases {
		bw := EventServerBandwidth{
			Incoming: internal.MakePtr(tc.bw),
		}
		assert.Equal(tc.allow, bw.AllowIncoming(), "failed for testcase %d: %+v", idx, tc)
	}
}

func TestAllowOutgoing(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	testcases := []struct {
		bw    float64
		allow bool
	}{
		{
			0, true,
		},
		{
			99, true,
		},
		{
			99.9, true,
		},
		{
			100, false,
		},
		{
			200, false,
		},
	}

	bw := EventServerBandwidth{
		Outgoing: nil,
	}
	assert.True(bw.AllowOutgoing())
	for idx, tc := range testcases {
		bw := EventServerBandwidth{
			Outgoing: internal.MakePtr(tc.bw),
		}
		assert.Equal(tc.allow, bw.AllowOutgoing(), "failed for testcase %d: %+v", idx, tc)
	}
}

func TestInformationEtcd(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	info1 := &InformationEtcd{}
	assert.ErrorContains(info1.CheckValid(), "address missing")

	info2 := &InformationEtcd{
		Address: "http://domain.invalid",
	}
	if assert.NoError(info2.CheckValid()) {
		assert.Equal("http://domain.invalid/", info2.Address)
	}

	info3 := &InformationEtcd{
		Address: "http://domain.invalid/",
	}
	if assert.NoError(info3.CheckValid()) {
		assert.Equal("http://domain.invalid/", info3.Address)
	}
}
