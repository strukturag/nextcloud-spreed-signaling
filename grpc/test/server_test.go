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
package test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

type emptyReceiver struct {
}

func (r *emptyReceiver) RemoteAddr() string {
	return "127.0.0.1"
}

func (r *emptyReceiver) Country() geoip.Country {
	return "DE"
}

func (r *emptyReceiver) UserAgent() string {
	return "testing"
}

func (r *emptyReceiver) OnProxyMessage(message *grpc.ServerSessionMessage) error {
	return errors.New("not implemented")
}

func (r *emptyReceiver) OnProxyClose(err error) {
	// Ignore
}

func TestServer(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	serverId := "the-test-server-id"
	server, addr := NewServerForTest(t)
	server.SetServerId(serverId)
	hub := &MockHub{}
	server.SetHub(hub)

	clients, _ := NewClientsForTest(t, addr, nil)

	require.NoError(clients.WaitForInitialized(t.Context()))

	backend := talk.NewCompatBackend(nil)

	for _, client := range clients.GetClients() {
		if id, version, err := client.GetServerId(t.Context()); assert.NoError(err) {
			assert.Equal(serverId, id)
			assert.NotEmpty(version)
		}

		reply, err := client.LookupResumeId(t.Context(), "resume-id")
		assert.ErrorIs(err, grpc.ErrNoSuchResumeId)
		assert.Nil(reply)

		id, err := client.LookupSessionId(t.Context(), "session-id", "")
		if s, ok := status.FromError(err); assert.True(ok) {
			assert.Equal(codes.Unknown, s.Code())
			assert.Equal("not implemented", s.Message())
		}
		assert.Empty(id)

		if incall, err := client.IsSessionInCall(t.Context(), "session-id", "room-id", ""); assert.NoError(err) {
			assert.False(incall)
		}

		if internal, virtual, err := client.GetInternalSessions(t.Context(), "room-id", nil); assert.NoError(err) {
			assert.Empty(internal)
			assert.Empty(virtual)
		}

		publisherId, proxyUrl, ip, connToken, publisherToken, err := client.GetPublisherId(t.Context(), "session-id", sfu.StreamTypeVideo)
		if s, ok := status.FromError(err); assert.True(ok) {
			assert.Equal(codes.Unknown, s.Code())
			assert.Equal("not implemented", s.Message())
		}
		assert.Empty(publisherId)
		assert.Empty(proxyUrl)
		assert.Empty(ip)
		assert.Empty(connToken)
		assert.Empty(publisherToken)

		if count, err := client.GetSessionCount(t.Context(), ""); assert.NoError(err) {
			assert.EqualValues(0, count)
		}

		if data, err := client.GetTransientData(t.Context(), "room-id", backend); assert.NoError(err) {
			assert.Empty(data)
		}

		receiver := &emptyReceiver{}
		proxy, err := client.ProxySession(t.Context(), "session-id", receiver)
		assert.NoError(err)
		assert.NotNil(proxy)
	}
}
