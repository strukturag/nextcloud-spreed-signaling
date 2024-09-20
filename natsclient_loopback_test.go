/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2018 struktur AG
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (c *LoopbackNatsClient) waitForSubscriptionsEmpty(ctx context.Context, t *testing.T) {
	for {
		c.mu.Lock()
		count := len(c.subscriptions)
		c.mu.Unlock()
		if count == 0 {
			break
		}

		select {
		case <-ctx.Done():
			c.mu.Lock()
			assert.NoError(t, ctx.Err(), "Error waiting for subscriptions %+v to terminate", c.subscriptions)
			c.mu.Unlock()
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func CreateLoopbackNatsClientForTest(t *testing.T) NatsClient {
	log := GetLoggerForTest(t)
	result, err := NewLoopbackNatsClient(log)
	require.NoError(t, err)
	t.Cleanup(func() {
		result.Close()
	})
	return result
}

func TestLoopbackNatsClient_Subscribe(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLoopbackNatsClientForTest(t)

		testNatsClient_Subscribe(t, client)
	})
}

func TestLoopbackClient_PublishAfterClose(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLoopbackNatsClientForTest(t)

		testNatsClient_PublishAfterClose(t, client)
	})
}

func TestLoopbackClient_SubscribeAfterClose(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLoopbackNatsClientForTest(t)

		testNatsClient_SubscribeAfterClose(t, client)
	})
}

func TestLoopbackClient_BadSubjects(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		client := CreateLoopbackNatsClientForTest(t)

		testNatsClient_BadSubjects(t, client)
	})
}
