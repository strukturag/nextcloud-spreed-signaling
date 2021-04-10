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
	// The message needs a type.
	msg := ClientMessage{}
	if err := msg.CheckValid(); err == nil {
		t.Errorf("Message %+v should not be valid", msg)
	}
}

func TestHelloClientMessage(t *testing.T) {
	internalAuthParams := []byte("{\"backend\":\"https://domain.invalid\"}")
	valid_messages := []testCheckValid{
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Params: &json.RawMessage{'{', '}'},
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Type:   "client",
				Params: &json.RawMessage{'{', '}'},
				Url:    "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Type:   "internal",
				Params: (*json.RawMessage)(&internalAuthParams),
			},
		},
		&HelloClientMessage{
			Version:  HelloVersion,
			ResumeId: "the-resume-id",
		},
	}
	invalid_messages := []testCheckValid{
		&HelloClientMessage{},
		&HelloClientMessage{Version: "0.0"},
		&HelloClientMessage{Version: HelloVersion},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Params: &json.RawMessage{'{', '}'},
				Type:   "invalid-type",
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Url: "https://domain.invalid",
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Params: &json.RawMessage{'{', '}'},
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Params: &json.RawMessage{'{', '}'},
				Url:    "invalid-url",
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Type:   "internal",
				Params: &json.RawMessage{'{', '}'},
			},
		},
		&HelloClientMessage{
			Version: HelloVersion,
			Auth: HelloClientMessageAuth{
				Type:   "internal",
				Params: &json.RawMessage{'x', 'y', 'z'}, // Invalid JSON.
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
