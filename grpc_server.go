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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	status "google.golang.org/grpc/status"
)

var (
	GrpcServerId string
)

func init() {
	RegisterGrpcServerStats()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = newRandomString(8)
	}
	md := sha256.New()
	md.Write([]byte(fmt.Sprintf("%s-%s-%d", newRandomString(32), hostname, os.Getpid())))
	GrpcServerId = hex.EncodeToString(md.Sum(nil))
}

type GrpcServer struct {
	UnimplementedRpcBackendServer
	UnimplementedRpcInternalServer
	UnimplementedRpcMcuServer
	UnimplementedRpcSessionsServer

	creds    credentials.TransportCredentials
	conn     *grpc.Server
	listener net.Listener
	serverId string // can be overwritten from tests

	hub *Hub
}

func NewGrpcServer(config *goconf.ConfigFile) (*GrpcServer, error) {
	var listener net.Listener
	if addr, _ := config.GetString("grpc", "listen"); addr != "" {
		var err error
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("could not create GRPC listener %s: %w", addr, err)
		}
	}

	creds, err := NewReloadableCredentials(config, true)
	if err != nil {
		return nil, err
	}

	conn := grpc.NewServer(grpc.Creds(creds))
	result := &GrpcServer{
		creds:    creds,
		conn:     conn,
		listener: listener,
		serverId: GrpcServerId,
	}
	RegisterRpcBackendServer(conn, result)
	RegisterRpcInternalServer(conn, result)
	RegisterRpcSessionsServer(conn, result)
	RegisterRpcMcuServer(conn, result)
	return result, nil
}

func (s *GrpcServer) Run() error {
	if s.listener == nil {
		return nil
	}

	return s.conn.Serve(s.listener)
}

func (s *GrpcServer) Close() {
	s.conn.GracefulStop()
}

func (s *GrpcServer) LookupSessionId(ctx context.Context, request *LookupSessionIdRequest) (*LookupSessionIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("LookupSessionId").Inc()
	// TODO: Remove debug logging
	log.Printf("Lookup session id for room session id %s", request.RoomSessionId)
	sid, err := s.hub.roomSessions.GetSessionId(request.RoomSessionId)
	if errors.Is(err, ErrNoSuchRoomSession) {
		return nil, status.Error(codes.NotFound, "no such room session id")
	} else if err != nil {
		return nil, err
	}

	if sid != "" && request.DisconnectReason != "" {
		if session := s.hub.GetSessionByPublicId(sid); session != nil {
			log.Printf("Closing session %s because same room session %s connected", session.PublicId(), request.RoomSessionId)
			session.LeaveRoom(false)
			switch sess := session.(type) {
			case *ClientSession:
				if client := sess.GetClient(); client != nil {
					client.SendByeResponseWithReason(nil, "room_session_reconnected")
				}
			}
			session.Close()
		}
	}
	return &LookupSessionIdReply{
		SessionId: sid,
	}, nil
}

func (s *GrpcServer) IsSessionInCall(ctx context.Context, request *IsSessionInCallRequest) (*IsSessionInCallReply, error) {
	statsGrpcServerCalls.WithLabelValues("IsSessionInCall").Inc()
	// TODO: Remove debug logging
	log.Printf("Check if session %s is in call %s on %s", request.SessionId, request.RoomId, request.BackendUrl)
	session := s.hub.GetSessionByPublicId(request.SessionId)
	if session == nil {
		return nil, status.Error(codes.NotFound, "no such session id")
	}

	result := &IsSessionInCallReply{}
	room := session.GetRoom()
	if room == nil || room.Id() != request.GetRoomId() || room.Backend().url != request.GetBackendUrl() ||
		(session.ClientType() != HelloClientTypeInternal && !room.IsSessionInCall(session)) {
		// Recipient is not in a room, a different room or not in the call.
		result.InCall = false
	} else {
		result.InCall = true
	}
	return result, nil
}

func (s *GrpcServer) GetPublisherId(ctx context.Context, request *GetPublisherIdRequest) (*GetPublisherIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetPublisherId").Inc()
	// TODO: Remove debug logging
	log.Printf("Get %s publisher id for session %s", request.StreamType, request.SessionId)
	session := s.hub.GetSessionByPublicId(request.SessionId)
	if session == nil {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	clientSession, ok := session.(*ClientSession)
	if !ok {
		return nil, status.Error(codes.NotFound, "no such session")
	}

	publisher := clientSession.GetOrWaitForPublisher(ctx, StreamType(request.StreamType))
	if publisher, ok := publisher.(*mcuProxyPublisher); ok {
		reply := &GetPublisherIdReply{
			PublisherId: publisher.Id(),
			ProxyUrl:    publisher.conn.rawUrl,
		}
		if ip := publisher.conn.ip; ip != nil {
			reply.Ip = ip.String()
		}
		return reply, nil
	}

	return nil, status.Error(codes.NotFound, "no such publisher")
}

func (s *GrpcServer) GetServerId(ctx context.Context, request *GetServerIdRequest) (*GetServerIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetServerId").Inc()
	return &GetServerIdReply{
		ServerId: s.serverId,
	}, nil
}

func (s *GrpcServer) GetSessionCount(ctx context.Context, request *GetSessionCountRequest) (*GetSessionCountReply, error) {
	statsGrpcServerCalls.WithLabelValues("SessionCount").Inc()

	u, err := url.Parse(request.Url)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid url")
	}

	backend := s.hub.backend.GetBackend(u)
	if backend == nil {
		return nil, status.Error(codes.NotFound, "no such backend")
	}

	return &GetSessionCountReply{
		Count: uint32(backend.Len()),
	}, nil
}
