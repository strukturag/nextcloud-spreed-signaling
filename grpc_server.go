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
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	status "google.golang.org/grpc/status"
)

type GrpcServer struct {
	UnimplementedRpcMcuServer
	UnimplementedRpcSessionsServer

	conn     *grpc.Server
	listener net.Listener

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

	var opts []grpc.ServerOption
	certificateFile, _ := config.GetString("grpc", "certificate")
	keyFile, _ := config.GetString("grpc", "key")
	if certificateFile != "" && keyFile != "" {
		creds, err := credentials.NewServerTLSFromFile(certificateFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("invalid GRPC server certificate / key in %s / %s: %w", certificateFile, keyFile, err)
		}

		opts = append(opts, grpc.Creds(creds))
	} else {
		log.Printf("WARNING: No GRPC server certificate and/or key configured, running unencrypted")
	}

	conn := grpc.NewServer(opts...)
	result := &GrpcServer{
		conn:     conn,
		listener: listener,
	}
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
	// TODO: Remove debug logging
	log.Printf("Lookup session id for room session id %s", request.RoomSessionId)
	sid, err := s.hub.roomSessions.GetSessionId(request.RoomSessionId)
	if errors.Is(err, ErrNoSuchRoomSession) {
		return nil, status.Error(codes.NotFound, "no such room session id")
	} else if err != nil {
		return nil, err
	}

	return &LookupSessionIdReply{
		SessionId: sid,
	}, nil
}

func (s *GrpcServer) IsSessionInCall(ctx context.Context, request *IsSessionInCallRequest) (*IsSessionInCallReply, error) {
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

	publisher := clientSession.GetOrWaitForPublisher(ctx, request.StreamType)
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
