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
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testCheckValid interface {
	CheckValid() error
}

func wrapMessage(messageType string, msg testCheckValid) *ClientMessage {
	wrapped := &ClientMessage{
		Type: messageType,
	}
	switch messageType {
	case "hello":
		wrapped.Hello = msg.(*HelloClientMessage)
	case "message":
		wrapped.Message = msg.(*MessageClientMessage)
	case "bye":
		wrapped.Bye = msg.(*ByeClientMessage)
	case "room":
		wrapped.Room = msg.(*RoomClientMessage)
	default:
		return nil
	}
	return wrapped
}

func testMessages(t *testing.T, messageType string, valid_messages []testCheckValid, invalid_messages []testCheckValid) {
	t.Helper()
	assert := assert.New(t)
	for _, msg := range valid_messages {
		assert.NoError(msg.CheckValid(), "Message %+v should be valid", msg)

		// If the inner message is valid, it should also be valid in a wrapped
		// ClientMessage.
		if wrapped := wrapMessage(messageType, msg); assert.NotNil(wrapped, "Unknown message type: %s", messageType) {
			assert.NoError(wrapped.CheckValid(), "Message %+v should be valid", wrapped)
		}
	}
	for _, msg := range invalid_messages {
		assert.Error(msg.CheckValid(), "Message %+v should not be valid", msg)

		// If the inner message is invalid, it should also be invalid in a
		// wrapped ClientMessage.
		if wrapped := wrapMessage(messageType, msg); assert.NotNil(wrapped, "Unknown message type: %s", messageType) {
			assert.Error(wrapped.CheckValid(), "Message %+v should not be valid", wrapped)
		}
	}
}

func TestClientMessage(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	// The message needs a type.
	msg := ClientMessage{}
	assert.Error(msg.CheckValid())
}

func TestHelloClientMessage(t *testing.T) {
	t.Parallel()
	internalAuthParams := []byte("{\"backend\":\"https://domain.invalid\"}")
	tokenAuthParams := []byte("{\"token\":\"invalid-token\"}")
	valid_messages := []testCheckValid{
		// Hello version 1
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Params: json.RawMessage("{}"),
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "client",
				Params: json.RawMessage("{}"),
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: internalAuthParams,
			},
		},
		&HelloClientMessage{
			Version:  HelloVersionV1,
			ResumeId: "the-resume-id",
		},
		// Hello version 2
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: tokenAuthParams,
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Type:   "client",
				Params: tokenAuthParams,
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version:  HelloVersionV2,
			ResumeId: "the-resume-id",
		},
	}
	invalid_messages := []testCheckValid{
		// Hello version 1
		&HelloClientMessage{},
		&HelloClientMessage{Version: "0.0"},
		&HelloClientMessage{Version: HelloVersionV1},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Params: json.RawMessage("{}"),
				Type:   "invalid-type",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Url: "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Params: json.RawMessage("{}"),
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Params: json.RawMessage("{}"),
				Url:    "invalid-url",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: json.RawMessage("{}"),
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: json.RawMessage("xyz"), // Invalid JSON.
			},
		},
		// Hello version 2
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Url: "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: tokenAuthParams,
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: tokenAuthParams,
				Url:    "invalid-url",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: internalAuthParams,
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: json.RawMessage("xyz"), // Invalid JSON.
				Url:    "https://domain.invalid",
			},
		},
	}

	testMessages(t, "hello", valid_messages, invalid_messages)

	// A "hello" message must be present
	msg := ClientMessage{
		Type: "hello",
	}
	assert := assert.New(t)
	assert.Error(msg.CheckValid())
}

func TestMessageClientMessage(t *testing.T) {
	t.Parallel()
	valid_messages := []testCheckValid{
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:      "session",
				SessionId: "the-session-id",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:   "user",
				UserId: "the-user-id",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "room",
			},
			Data: json.RawMessage("{}"),
		},
	}
	invalid_messages := []testCheckValid{
		&MessageClientMessage{},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:      "session",
				SessionId: "the-session-id",
			},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "session",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:   "session",
				UserId: "the-user-id",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "user",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:   "user",
				UserId: "the-user-id",
			},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:      "user",
				SessionId: "the-user-id",
			},
			Data: json.RawMessage("{}"),
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "unknown-type",
			},
			Data: json.RawMessage("{}"),
		},
	}
	testMessages(t, "message", valid_messages, invalid_messages)

	// A "message" message must be present
	msg := ClientMessage{
		Type: "message",
	}
	assert := assert.New(t)
	assert.Error(msg.CheckValid())
}

func TestByeClientMessage(t *testing.T) {
	t.Parallel()
	// Any "bye" message is valid.
	valid_messages := []testCheckValid{
		&ByeClientMessage{},
	}
	invalid_messages := []testCheckValid{}

	testMessages(t, "bye", valid_messages, invalid_messages)

	// The "bye" message is optional.
	msg := ClientMessage{
		Type: "bye",
	}
	assert := assert.New(t)
	assert.NoError(msg.CheckValid())
}

func TestRoomClientMessage(t *testing.T) {
	t.Parallel()
	// Any "room" message is valid.
	valid_messages := []testCheckValid{
		&RoomClientMessage{},
	}
	invalid_messages := []testCheckValid{}

	testMessages(t, "room", valid_messages, invalid_messages)

	// But a "room" message must be present
	msg := ClientMessage{
		Type: "room",
	}
	assert := assert.New(t)
	assert.Error(msg.CheckValid())
}

func TestErrorMessages(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	id := "request-id"
	msg := ClientMessage{
		Id: id,
	}
	err1 := msg.NewErrorServerMessage(&Error{})
	assert.Equal(id, err1.Id, "%+v", err1)
	assert.Equal("error", err1.Type, "%+v", err1)
	assert.NotNil(err1.Error, "%+v", err1)

	err2 := msg.NewWrappedErrorServerMessage(fmt.Errorf("test-error"))
	assert.Equal(id, err2.Id, "%+v", err2)
	assert.Equal("error", err2.Type, "%+v", err2)
	if assert.NotNil(err2.Error, "%+v", err2) {
		assert.Equal("internal_error", err2.Error.Code, "%+v", err2)
		assert.Equal("test-error", err2.Error.Message, "%+v", err2)
	}
	// Test "error" interface
	assert.Equal("test-error", err2.Error.Error(), "%+v", err2)
}

func TestIsChatRefresh(t *testing.T) {
	t.Parallel()
	var msg ServerMessage
	data_true := []byte("{\"type\":\"chat\",\"chat\":{\"refresh\":true}}")
	msg = ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Data: data_true,
		},
	}
	assert.True(t, msg.IsChatRefresh())

	data_false := []byte("{\"type\":\"chat\",\"chat\":{\"refresh\":false}}")
	msg = ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Data: data_false,
		},
	}
	assert.False(t, msg.IsChatRefresh())
}

func assertEqualStrings(t *testing.T, expected, result []string) {
	t.Helper()

	if expected == nil {
		expected = make([]string, 0)
	} else {
		sort.Strings(expected)
	}
	if result == nil {
		result = make([]string, 0)
	} else {
		sort.Strings(result)
	}

	assert.Equal(t, expected, result)
}

func Test_Welcome_AddRemoveFeature(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	var msg WelcomeServerMessage
	assertEqualStrings(t, []string{}, msg.Features)

	msg.AddFeature("one", "two", "one")
	assertEqualStrings(t, []string{"one", "two"}, msg.Features)
	assert.True(sort.StringsAreSorted(msg.Features), "features should be sorted, got %+v", msg.Features)

	msg.AddFeature("three")
	assertEqualStrings(t, []string{"one", "two", "three"}, msg.Features)
	assert.True(sort.StringsAreSorted(msg.Features), "features should be sorted, got %+v", msg.Features)

	msg.RemoveFeature("three", "one")
	assertEqualStrings(t, []string{"two"}, msg.Features)
}
