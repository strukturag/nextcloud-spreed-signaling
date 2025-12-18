/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
package talk

import (
	"testing"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/etcd"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/test"
)

func (s *backendStorageEtcd) getWakeupChannelForTesting() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.wakeupChanForTesting != nil {
		return s.wakeupChanForTesting
	}

	ch := make(chan struct{}, 1)
	s.wakeupChanForTesting = ch
	return ch
}

type testListener struct {
	etcd   *etcdtest.TestServer
	closed chan struct{}
}

func (tl *testListener) EtcdClientCreated(client etcd.Client) {
	close(tl.closed)
}

func Test_BackendStorageEtcdNoLeak(t *testing.T) { // nolint:paralleltest
	logger := log.NewLoggerForTest(t)
	test.EnsureNoGoroutinesLeak(t, func(t *testing.T) {
		embedEtcd, client := etcdtest.NewClientForTest(t)
		tl := &testListener{
			etcd:   embedEtcd,
			closed: make(chan struct{}),
		}
		client.AddListener(tl)
		defer client.RemoveListener(tl)

		config := goconf.NewConfigFile()
		config.AddOption("backend", "backendtype", "etcd")
		config.AddOption("backend", "backendprefix", "/backends")

		cfg, err := NewBackendConfiguration(logger, config, client)
		require.NoError(t, err)

		<-tl.closed
		cfg.Close()
	})
}
