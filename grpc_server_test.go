/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"net"
	"strconv"
	"testing"

	"github.com/dlintw/goconf"
)

func NewGrpcServerForTest(t *testing.T) (server *GrpcServer, addr string) {
	config := goconf.NewConfigFile()
	for port := 50000; port < 50100; port++ {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		config.AddOption("grpc", "listen", addr)
		var err error
		server, err = NewGrpcServer(config)
		if isErrorAddressAlreadyInUse(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		break
	}

	if server == nil {
		t.Fatal("could not find free port")
	}

	t.Cleanup(func() {
		server.Close()
	})
	return server, addr
}
