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
package nats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func CreateLoopbackClientForTest(t *testing.T) Client {
	logger := log.NewLoggerForTest(t)
	result, err := NewLoopbackClient(logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		assert.NoError(t, result.Close(ctx))
	})
	return result
}

func TestLoopbackClient_Subscribe(t *testing.T) {
	t.Parallel()

	client := CreateLoopbackClientForTest(t)
	testClient_Subscribe(t, client)
}

func TestLoopbackClient_PublishAfterClose(t *testing.T) {
	t.Parallel()

	client := CreateLoopbackClientForTest(t)
	test_PublishAfterClose(t, client)
}

func TestLoopbackClient_SubscribeAfterClose(t *testing.T) {
	t.Parallel()

	client := CreateLoopbackClientForTest(t)
	testClient_SubscribeAfterClose(t, client)
}

func TestLoopbackClient_BadSubjects(t *testing.T) {
	t.Parallel()

	client := CreateLoopbackClientForTest(t)
	testClient_BadSubjects(t, client)
}
