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
	"reflect"
	"sort"
	"testing"
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
	for _, msg := range valid_messages {
		if err := msg.CheckValid(); err != nil {
			t.Errorf("Message %+v should be valid, got %s", msg, err)
		}
		// If the inner message is valid, it should also be valid in a wrapped
		// ClientMessage.
		if wrapped := wrapMessage(messageType, msg); wrapped == nil {
			t.Errorf("Unknown message type: %s", messageType)
		} else if err := wrapped.CheckValid(); err != nil {
			t.Errorf("Message %+v should be valid, got %s", wrapped, err)
		}
	}
	for _, msg := range invalid_messages {
		if err := msg.CheckValid(); err == nil {
			t.Errorf("Message %+v should not be valid", msg)
		}

		// If the inner message is invalid, it should also be invalid in a
		// wrapped ClientMessage.
		if wrapped := wrapMessage(messageType, msg); wrapped == nil {
			t.Errorf("Unknown message type: %s", messageType)
		} else if err := wrapped.CheckValid(); err == nil {
			t.Errorf("Message %+v should not be valid", wrapped)
		}
	}
}

func TestClientMessage(t *testing.T) {
	t.Parallel()
	// The message needs a type.
	msg := ClientMessage{}
	if err := msg.CheckValid(); err == nil {
		t.Errorf("Message %+v should not be valid", msg)
	}
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
				Params: &json.RawMessage{'{', '}'},
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "client",
				Params: &json.RawMessage{'{', '}'},
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: (*json.RawMessage)(&internalAuthParams),
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
				Params: (*json.RawMessage)(&tokenAuthParams),
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Type:   "client",
				Params: (*json.RawMessage)(&tokenAuthParams),
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
				Params: &json.RawMessage{'{', '}'},
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
				Params: &json.RawMessage{'{', '}'},
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Params: &json.RawMessage{'{', '}'},
				Url:    "invalid-url",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: &json.RawMessage{'{', '}'},
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV1,
			Auth: &HelloClientMessageAuth{
				Type:   "internal",
				Params: &json.RawMessage{'x', 'y', 'z'}, // Invalid JSON.
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
				Params: (*json.RawMessage)(&tokenAuthParams),
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: (*json.RawMessage)(&tokenAuthParams),
				Url:    "invalid-url",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: (*json.RawMessage)(&internalAuthParams),
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersionV2,
			Auth: &HelloClientMessageAuth{
				Params: &json.RawMessage{'x', 'y', 'z'}, // Invalid JSON.
				Url:    "https://domain.invalid",
			},
		},
	}

	testMessages(t, "hello", valid_messages, invalid_messages)

	// A "hello" message must be present
	msg := ClientMessage{
		Type: "hello",
	}
	if err := msg.CheckValid(); err == nil {
		t.Errorf("Message %+v should not be valid", msg)
	}
}

func TestMessageClientMessage(t *testing.T) {
	t.Parallel()
	valid_messages := []testCheckValid{
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:      "session",
				SessionId: "the-session-id",
			},
			Data: &json.RawMessage{'{', '}'},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:   "user",
				UserId: "the-user-id",
			},
			Data: &json.RawMessage{'{', '}'},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "room",
			},
			Data: &json.RawMessage{'{', '}'},
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
			Data: &json.RawMessage{'{', '}'},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type:   "session",
				UserId: "the-user-id",
			},
			Data: &json.RawMessage{'{', '}'},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "user",
			},
			Data: &json.RawMessage{'{', '}'},
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
			Data: &json.RawMessage{'{', '}'},
		},
		&MessageClientMessage{
			Recipient: MessageClientMessageRecipient{
				Type: "unknown-type",
			},
			Data: &json.RawMessage{'{', '}'},
		},
	}
	testMessages(t, "message", valid_messages, invalid_messages)

	// A "message" message must be present
	msg := ClientMessage{
		Type: "message",
	}
	if err := msg.CheckValid(); err == nil {
		t.Errorf("Message %+v should not be valid", msg)
	}
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
	if err := msg.CheckValid(); err != nil {
		t.Errorf("Message %+v should be valid, got %s", msg, err)
	}
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
	if err := msg.CheckValid(); err == nil {
		t.Errorf("Message %+v should not be valid", msg)
	}
}

func TestErrorMessages(t *testing.T) {
	t.Parallel()
	id := "request-id"
	msg := ClientMessage{
		Id: id,
	}
	err1 := msg.NewErrorServerMessage(&Error{})
	if err1.Id != id {
		t.Errorf("Expected id %s, got %+v", id, err1)
	}
	if err1.Type != "error" || err1.Error == nil {
		t.Errorf("Expected type \"error\", got %+v", err1)
	}

	err2 := msg.NewWrappedErrorServerMessage(fmt.Errorf("test-error"))
	if err2.Id != id {
		t.Errorf("Expected id %s, got %+v", id, err2)
	}
	if err2.Type != "error" || err2.Error == nil {
		t.Errorf("Expected type \"error\", got %+v", err2)
	}
	if err2.Error.Code != "internal_error" {
		t.Errorf("Expected code \"internal_error\", got %+v", err2)
	}
	if err2.Error.Message != "test-error" {
		t.Errorf("Expected message \"test-error\", got %+v", err2)
	}
	// Test "error" interface
	if err2.Error.Error() != "test-error" {
		t.Errorf("Expected error string \"test-error\", got %+v", err2)
	}
}

func TestIsChatRefresh(t *testing.T) {
	t.Parallel()
	var msg ServerMessage
	data_true := []byte("{\"type\":\"chat\",\"chat\":{\"refresh\":true}}")
	msg = ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Data: (*json.RawMessage)(&data_true),
		},
	}
	if !msg.IsChatRefresh() {
		t.Error("message should be detected as chat refresh")
	}

	data_false := []byte("{\"type\":\"chat\",\"chat\":{\"refresh\":false}}")
	msg = ServerMessage{
		Type: "message",
		Message: &MessageServerMessage{
			Data: (*json.RawMessage)(&data_false),
		},
	}
	if msg.IsChatRefresh() {
		t.Error("message should not be detected as chat refresh")
	}
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

	if !reflect.DeepEqual(expected, result) {
		t.Errorf("Expected %+v, got %+v", expected, result)
	}
}

func Test_Welcome_AddRemoveFeature(t *testing.T) {
	t.Parallel()
	var msg WelcomeServerMessage
	assertEqualStrings(t, []string{}, msg.Features)

	msg.AddFeature("one", "two", "one")
	assertEqualStrings(t, []string{"one", "two"}, msg.Features)
	if !sort.StringsAreSorted(msg.Features) {
		t.Errorf("features should be sorted, got %+v", msg.Features)
	}

	msg.AddFeature("three")
	assertEqualStrings(t, []string{"one", "two", "three"}, msg.Features)
	if !sort.StringsAreSorted(msg.Features) {
		t.Errorf("features should be sorted, got %+v", msg.Features)
	}

	msg.RemoveFeature("three", "one")
	assertEqualStrings(t, []string{"two"}, msg.Features)
}
