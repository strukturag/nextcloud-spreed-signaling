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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	serverId := "the-test-server-id"
	server, addr := NewServerForTest(t)
	server.SetServerId(serverId)

	clients, _ := NewClientsForTest(t, addr, nil)

	require.NoError(clients.WaitForInitialized(t.Context()))

	for _, client := range clients.GetClients() {
		if id, version, err := client.GetServerId(t.Context()); assert.NoError(err) {
			assert.Equal(serverId, id)
			assert.NotEmpty(version)
		}
	}
}
