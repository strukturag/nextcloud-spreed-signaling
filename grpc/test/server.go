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
	"net"
	"strconv"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	logtest "github.com/strukturag/nextcloud-spreed-signaling/log/test"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

func NewServerForTestWithConfig(t *testing.T, config *goconf.ConfigFile) (server *grpc.Server, addr string) {
	logger := logtest.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	for port := 50000; port < 50100; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = grpc.NewServer(ctx, config, "0.0.0")
		if test.IsErrorAddressAlreadyInUse(err) {
			continue
		}

		require.NoError(t, err)
		break
	}

	require.NotNil(t, server, "could not find free port")

	// Don't match with own server id by default.
	server.SetServerId("dont-match")

	go func() {
		assert.NoError(t, server.Run(), "could not start GRPC server")
	}()

	t.Cleanup(func() {
		server.Close()
	})
	return server, addr
}

func NewServerForTest(t *testing.T) (server *grpc.Server, addr string) {
	config := goconf.NewConfigFile()
	return NewServerForTestWithConfig(t, config)
}
