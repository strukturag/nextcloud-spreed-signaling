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
package server

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/etcd/etcdtest"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	grpctest "github.com/strukturag/nextcloud-spreed-signaling/grpc/test"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/mock"
	proxytest "github.com/strukturag/nextcloud-spreed-signaling/sfu/proxy/test"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/proxy/testserver"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockGrpcServerHub struct {
	proxy        atomic.Pointer[sfu.WithToken]
	sessionsLock sync.Mutex
	// +checklocks:sessionsLock
	sessionByPublicId map[api.PublicSessionId]Session
}

func (h *mockGrpcServerHub) setProxy(t *testing.T, proxy sfu.SFU) {
	t.Helper()

	wt, ok := proxy.(sfu.WithToken)
	require.True(t, ok, "need a sfu with token support")
	h.proxy.Store(&wt)
}

func (h *mockGrpcServerHub) getSession(sessionId api.PublicSessionId) Session {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()

	return h.sessionByPublicId[sessionId]
}

func (h *mockGrpcServerHub) addSession(session *ClientSession) {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()
	if h.sessionByPublicId == nil {
		h.sessionByPublicId = make(map[api.PublicSessionId]Session)
	}
	h.sessionByPublicId[session.PublicId()] = session
}

func (h *mockGrpcServerHub) removeSession(session *ClientSession) {
	h.sessionsLock.Lock()
	defer h.sessionsLock.Unlock()
	delete(h.sessionByPublicId, session.PublicId())
}

func (h *mockGrpcServerHub) GetSessionIdByResumeId(resumeId api.PrivateSessionId) api.PublicSessionId {
	return ""
}

func (h *mockGrpcServerHub) GetSessionIdByRoomSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error) {
	return "", nil
}

func (h *mockGrpcServerHub) IsSessionIdInCall(sessionId api.PublicSessionId, roomId string, backendUrl string) (bool, bool) {
	return false, false
}

func (h *mockGrpcServerHub) DisconnectSessionByRoomSessionId(sessionId api.PublicSessionId, roomSessionId api.RoomSessionId, reason string) {
}

func (h *mockGrpcServerHub) GetBackend(u *url.URL) *talk.Backend {
	return nil
}

func (h *mockGrpcServerHub) GetInternalSessions(roomId string, backend *talk.Backend) ([]*grpc.InternalSessionData, []*grpc.VirtualSessionData, bool) {
	return nil, nil, false
}

func (h *mockGrpcServerHub) GetTransientEntries(roomId string, backend *talk.Backend) (api.TransientDataEntries, bool) {
	return nil, false
}

func (h *mockGrpcServerHub) GetPublisherIdForSessionId(ctx context.Context, sessionId api.PublicSessionId, streamType sfu.StreamType) (*grpc.GetPublisherIdReply, error) {
	session := h.getSession(sessionId)
	if session == nil {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	clientSession, ok := session.(*ClientSession)
	if !ok {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	publisher := clientSession.GetOrWaitForPublisher(ctx, streamType)
	if publisher, ok := publisher.(sfu.PublisherWithConnectionUrlAndIP); ok {
		connUrl, ip := publisher.GetConnectionURL()
		reply := &grpc.GetPublisherIdReply{
			PublisherId: publisher.Id(),
			ProxyUrl:    connUrl,
		}
		if len(ip) > 0 {
			reply.Ip = ip.String()
		}

		if proxy := h.proxy.Load(); proxy != nil {
			reply.ConnectToken, _ = (*proxy).CreateToken("")
			reply.PublisherToken, _ = (*proxy).CreateToken(publisher.Id())
		}
		return reply, nil
	}

	return nil, status.Error(codes.NotFound, "no such publisher")
}

func (h *mockGrpcServerHub) ProxySession(request grpc.RpcSessions_ProxySessionServer) error {
	return errors.New("not implemented")
}

func Test_ProxyRemotePublisher(t *testing.T) {
	t.Parallel()

	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")

	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyMultipleRemotePublisher(t *testing.T) {
	t.Parallel()

	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)
	grpcServer3, addr3 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	hub3 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)
	grpcServer3.SetHub(hub3)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	embedEtcd.SetValue("/grpctargets/three", []byte("{\"address\":\""+addr3+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "US")
	server3 := testserver.NewProxyServerForTest(t, "US")

	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
			server3,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
			server3,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)
	mcu3, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
			server3,
		},
	}, 3, nil)
	hub3.setProxy(t, mcu3)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	sub1Listener := mock.NewListener("subscriber-public-1")
	sub1Initiator := mock.NewInitiator("US")
	sub1, err := mcu2.NewSubscriber(ctx, sub1Listener, pubId, sfu.StreamTypeVideo, sub1Initiator)
	require.NoError(t, err)

	defer sub1.Close(context.Background())

	sub2Listener := mock.NewListener("subscriber-public-2")
	sub2Initiator := mock.NewInitiator("US")
	sub2, err := mcu3.NewSubscriber(ctx, sub2Listener, pubId, sfu.StreamTypeVideo, sub2Initiator)
	require.NoError(t, err)

	defer sub2.Close(context.Background())
}

func Test_ProxyRemotePublisherWait(t *testing.T) {
	t.Parallel()

	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")

	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
			server2,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")

	done := make(chan struct{})
	go func() {
		defer close(done)
		sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
		if !assert.NoError(t, err) {
			return
		}

		defer sub.Close(context.Background())
	}()

	// Give subscriber goroutine some time to start
	time.Sleep(100 * time.Millisecond)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		assert.NoError(t, ctx.Err())
	}
}

func Test_ProxyRemotePublisherTemporary(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")

	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server2,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	type connectionCounter interface {
		ConnectionsCount() int
	}

	if counter2, ok := mcu2.(connectionCounter); assert.True(ok) {
		assert.Equal(1, counter2.ConnectionsCount())
	}

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())

	if connSub, ok := sub.(sfu.SubscriberWithConnectionUrlAndIP); assert.True(ok) {
		url, ip := connSub.GetConnectionURL()
		assert.Equal(server1.URL(), url)
		assert.Empty(ip)
	}

	// The temporary connection has been added
	if counter2, ok := mcu2.(connectionCounter); assert.True(ok) {
		assert.Equal(2, counter2.ConnectionsCount())
	}

	sub.Close(context.Background())

	// Wait for temporary connection to be removed.
loop:
	for {
		select {
		case <-ctx.Done():
			assert.NoError(ctx.Err())
		default:
			if counter2, ok := mcu2.(connectionCounter); assert.True(ok) {
				if counter2.ConnectionsCount() == 1 {
					break loop
				}
			}
		}
	}
}

func Test_ProxyConnectToken(t *testing.T) {
	t.Parallel()

	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "DE")

	// Signaling server instances are in a cluster but don't share their proxies,
	// i.e. they are only known to their local proxy, not the one of the other
	// signaling server - so the connection token must be passed between them.
	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server2,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("DE")
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}

func Test_ProxyPublisherToken(t *testing.T) {
	t.Parallel()

	embedEtcd := etcdtest.NewServerForTest(t)

	grpcServer1, addr1 := grpctest.NewServerForTest(t)
	grpcServer2, addr2 := grpctest.NewServerForTest(t)

	hub1 := &mockGrpcServerHub{}
	hub2 := &mockGrpcServerHub{}
	grpcServer1.SetHub(hub1)
	grpcServer2.SetHub(hub2)

	embedEtcd.SetValue("/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	embedEtcd.SetValue("/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	server1 := testserver.NewProxyServerForTest(t, "DE")
	server2 := testserver.NewProxyServerForTest(t, "US")

	// Signaling server instances are in a cluster but don't share their proxies,
	// i.e. they are only known to their local proxy, not the one of the other
	// signaling server - so the connection token must be passed between them.
	// Also the subscriber is connecting from a different country, so a remote
	// stream will be created that needs a valid token from the remote proxy.
	mcu1, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server1,
		},
	}, 1, nil)
	hub1.setProxy(t, mcu1)
	mcu2, _ := proxytest.NewMcuProxyForTestWithOptions(t, testserver.ProxyTestOptions{
		Etcd: embedEtcd,
		Servers: []testserver.ProxyTestServer{
			server2,
		},
	}, 2, nil)
	hub2.setProxy(t, mcu2)
	// Support remote subscribers for the tests.
	server1.Servers = append(server1.Servers, server2)
	server2.Servers = append(server2.Servers, server1)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	pubId := api.PublicSessionId("the-publisher")
	pubSid := "1234567890"
	pubListener := mock.NewListener(pubId + "-public")
	pubInitiator := mock.NewInitiator("DE")

	session1 := &ClientSession{
		publicId:   pubId,
		publishers: make(map[sfu.StreamType]sfu.Publisher),
	}
	hub1.addSession(session1)
	defer hub1.removeSession(session1)

	pub, err := mcu1.NewPublisher(ctx, pubListener, pubId, pubSid, sfu.StreamTypeVideo, sfu.NewPublisherSettings{
		MediaTypes: sfu.MediaTypeVideo | sfu.MediaTypeAudio,
	}, pubInitiator)
	require.NoError(t, err)

	defer pub.Close(context.Background())

	session1.mu.Lock()
	session1.publishers[sfu.StreamTypeVideo] = pub
	session1.publisherWaiters.Wakeup()
	session1.mu.Unlock()

	subListener := mock.NewListener("subscriber-public")
	subInitiator := mock.NewInitiator("US")
	sub, err := mcu2.NewSubscriber(ctx, subListener, pubId, sfu.StreamTypeVideo, subInitiator)
	require.NoError(t, err)

	defer sub.Close(context.Background())
}
