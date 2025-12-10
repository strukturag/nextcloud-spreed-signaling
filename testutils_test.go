/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func WaitForUsersJoined(ctx context.Context, t *testing.T, client1 *TestClient, hello1 *ServerMessage, client2 *TestClient, hello2 *ServerMessage) {
	t.Helper()
	// We will receive "joined" events for all clients. The ordering is not
	// defined as messages are processed and sent by asynchronous event handlers.
	client1.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
	client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello)
}

func MustSucceed1[T any, A1 any](t *testing.T, f func(a1 A1) (T, bool), a1 A1) T {
	t.Helper()
	result, ok := f(a1)
	if !ok {
		t.FailNow()
	}
	return result
}

func MustSucceed2[T any, A1 any, A2 any](t *testing.T, f func(a1 A1, a2 A2) (T, bool), a1 A1, a2 A2) T {
	t.Helper()
	result, ok := f(a1, a2)
	if !ok {
		t.FailNow()
	}
	return result
}

func MustSucceed3[T any, A1 any, A2 any, A3 any](t *testing.T, f func(a1 A1, a2 A2, a3 A3) (T, bool), a1 A1, a2 A2, a3 A3) T {
	t.Helper()
	result, ok := f(a1, a2, a3)
	if !ok {
		t.FailNow()
	}
	return result
}

func AssertEqualSerialized(t *testing.T, expected any, actual any, msgAndArgs ...any) bool {
	t.Helper()

	e, err := json.MarshalIndent(expected, "", "  ")
	if !assert.NoError(t, err) {
		return false
	}

	a, err := json.MarshalIndent(actual, "", "  ")
	if !assert.NoError(t, err) {
		return false
	}

	return assert.Equal(t, string(a), string(e), msgAndArgs...)
}
