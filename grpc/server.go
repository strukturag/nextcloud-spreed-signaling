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
package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	status "google.golang.org/grpc/status"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

func init() {
	RegisterServerStats()
}

type ServerHub interface {
	GetSessionIdByResumeId(resumeId api.PrivateSessionId) api.PublicSessionId
	GetSessionIdByRoomSessionId(roomSessionId api.RoomSessionId) (api.PublicSessionId, error)
	IsSessionIdInCall(sessionId api.PublicSessionId, roomId string, backendUrl string) (bool, bool)

	DisconnectSessionByRoomSessionId(sessionId api.PublicSessionId, roomSessionId api.RoomSessionId, reason string)

	GetBackend(u *url.URL) *talk.Backend
	GetInternalSessions(roomId string, backend *talk.Backend) ([]*InternalSessionData, []*VirtualSessionData, bool)
	GetTransientEntries(roomId string, backend *talk.Backend) (api.TransientDataEntries, bool)
	GetPublisherIdForSessionId(ctx context.Context, sessionId api.PublicSessionId, streamType sfu.StreamType) (*GetPublisherIdReply, error)

	ProxySession(request RpcSessions_ProxySessionServer) error
}

type Server struct {
	UnimplementedRpcBackendServer
	UnimplementedRpcInternalServer
	UnimplementedRpcMcuServer
	UnimplementedRpcSessionsServer

	logger   log.Logger
	version  string
	creds    credentials.TransportCredentials
	conn     *grpc.Server
	listener net.Listener
	serverId string // can be overwritten from tests

	hub ServerHub
}

func NewServer(ctx context.Context, cfg *goconf.ConfigFile, version string) (*Server, error) {
	var listener net.Listener
	if addr, _ := config.GetStringOptionWithEnv(cfg, "grpc", "listen"); addr != "" {
		var err error
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("could not create GRPC listener %s: %w", addr, err)
		}
	}

	logger := log.LoggerFromContext(ctx)
	creds, err := NewReloadableCredentials(logger, cfg, true)
	if err != nil {
		return nil, err
	}

	conn := grpc.NewServer(grpc.Creds(creds))
	result := &Server{
		logger:   logger,
		version:  version,
		creds:    creds,
		conn:     conn,
		listener: listener,
		serverId: ServerId,
	}
	RegisterRpcBackendServer(conn, result)
	RegisterRpcInternalServer(conn, result)
	RegisterRpcSessionsServer(conn, result)
	RegisterRpcMcuServer(conn, result)
	return result, nil
}

func (s *Server) SetHub(hub ServerHub) {
	s.hub = hub
}

func (s *Server) SetServerId(serverId string) {
	s.serverId = serverId
}

func (s *Server) Run() error {
	if s.listener == nil {
		return nil
	}

	return s.conn.Serve(s.listener)
}

type SimpleCloser interface {
	Close()
}

func (s *Server) Close() {
	s.conn.GracefulStop()
	if cr, ok := s.creds.(SimpleCloser); ok {
		cr.Close()
	}
}

func (s *Server) CloseUnclean() {
	s.conn.Stop()
}

func (s *Server) LookupResumeId(ctx context.Context, request *LookupResumeIdRequest) (*LookupResumeIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("LookupResumeId").Inc()
	// TODO: Remove debug logging
	s.logger.Printf("Lookup session for resume id %s", request.ResumeId)
	sessionId := s.hub.GetSessionIdByResumeId(api.PrivateSessionId(request.ResumeId))
	if sessionId == "" {
		return nil, status.Error(codes.NotFound, "no such room session id")
	}

	return &LookupResumeIdReply{
		SessionId: string(sessionId),
	}, nil
}

func (s *Server) LookupSessionId(ctx context.Context, request *LookupSessionIdRequest) (*LookupSessionIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("LookupSessionId").Inc()
	// TODO: Remove debug logging
	s.logger.Printf("Lookup session id for room session id %s", request.RoomSessionId)
	sid, err := s.hub.GetSessionIdByRoomSessionId(api.RoomSessionId(request.RoomSessionId))
	if errors.Is(err, ErrNoSuchRoomSession) {
		return nil, status.Error(codes.NotFound, "no such room session id")
	} else if err != nil {
		return nil, err
	}

	if sid != "" && request.DisconnectReason != "" {
		s.hub.DisconnectSessionByRoomSessionId(sid, api.RoomSessionId(request.RoomSessionId), request.DisconnectReason)
	}
	return &LookupSessionIdReply{
		SessionId: string(sid),
	}, nil
}

func (s *Server) IsSessionInCall(ctx context.Context, request *IsSessionInCallRequest) (*IsSessionInCallReply, error) {
	statsGrpcServerCalls.WithLabelValues("IsSessionInCall").Inc()
	// TODO: Remove debug logging
	s.logger.Printf("Check if session %s is in call %s on %s", request.SessionId, request.RoomId, request.BackendUrl)

	found, inCall := s.hub.IsSessionIdInCall(api.PublicSessionId(request.SessionId), request.GetRoomId(), request.GetBackendUrl())
	if !found {
		return nil, status.Error(codes.NotFound, "no such session id")
	}

	result := &IsSessionInCallReply{
		InCall: inCall,
	}
	return result, nil
}

func (s *Server) GetInternalSessions(ctx context.Context, request *GetInternalSessionsRequest) (*GetInternalSessionsReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetInternalSessions").Inc()
	// TODO: Remove debug logging
	s.logger.Printf("Get internal sessions from %s on %v (fallback %s)", request.RoomId, request.BackendUrls, request.BackendUrl) // nolint

	var backendUrls []string
	if len(request.BackendUrls) > 0 {
		backendUrls = request.BackendUrls
	} else if request.BackendUrl != "" { // nolint
		backendUrls = append(backendUrls, request.BackendUrl) // nolint
	} else {
		// Only compat backend.
		backendUrls = []string{""}
	}

	result := &GetInternalSessionsReply{}
	processed := make(map[string]bool)
	for _, bu := range backendUrls {
		var parsed *url.URL
		if bu != "" {
			var err error
			parsed, err = url.Parse(bu)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid url")
			}
		}

		backend := s.hub.GetBackend(parsed)
		if backend == nil {
			return nil, status.Error(codes.NotFound, "no such backend")
		}

		// Only process each backend once.
		if processed[backend.Id()] {
			continue
		}
		processed[backend.Id()] = true

		internalSessions, virtualSessions, found := s.hub.GetInternalSessions(request.RoomId, backend)
		if !found {
			return nil, status.Error(codes.NotFound, "no such room")
		}

		result.InternalSessions = append(result.InternalSessions, internalSessions...)
		result.VirtualSessions = append(result.VirtualSessions, virtualSessions...)
	}

	return result, nil
}

func (s *Server) GetPublisherId(ctx context.Context, request *GetPublisherIdRequest) (*GetPublisherIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetPublisherId").Inc()
	// TODO: Remove debug logging
	s.logger.Printf("Get %s publisher id for session %s", request.StreamType, request.SessionId)

	return s.hub.GetPublisherIdForSessionId(ctx, api.PublicSessionId(request.SessionId), sfu.StreamType(request.StreamType))
}

func (s *Server) GetServerId(ctx context.Context, request *GetServerIdRequest) (*GetServerIdReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetServerId").Inc()
	return &GetServerIdReply{
		ServerId: s.serverId,
		Version:  s.version,
	}, nil
}

func (s *Server) GetTransientData(ctx context.Context, request *GetTransientDataRequest) (*GetTransientDataReply, error) {
	statsGrpcServerCalls.WithLabelValues("GetTransientData").Inc()

	backendUrls := request.BackendUrls
	if len(backendUrls) == 0 {
		// Only compat backend.
		backendUrls = []string{""}
	}

	result := &GetTransientDataReply{}
	processed := make(map[string]bool)
	for _, bu := range backendUrls {
		var parsed *url.URL
		if bu != "" {
			var err error
			parsed, err = url.Parse(bu)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid url")
			}
		}

		backend := s.hub.GetBackend(parsed)
		if backend == nil {
			return nil, status.Error(codes.NotFound, "no such backend")
		}

		// Only process each backend once.
		if processed[backend.Id()] {
			continue
		}
		processed[backend.Id()] = true

		entries, found := s.hub.GetTransientEntries(request.RoomId, backend)
		if !found {
			return nil, status.Error(codes.NotFound, "no such room")
		} else if len(entries) == 0 {
			return nil, status.Error(codes.NotFound, "room has no transient data")
		}

		if result.Entries == nil {
			result.Entries = make(map[string]*GrpcTransientDataEntry)
		}
		for k, v := range entries {
			e := &GrpcTransientDataEntry{}
			var err error
			if e.Value, err = json.Marshal(v.Value); err != nil {
				return nil, status.Errorf(codes.Internal, "error marshalling data: %s", err)
			}
			if !v.Expires.IsZero() {
				e.Expires = v.Expires.UnixMicro()
			}
			result.Entries[k] = e
		}
	}

	return result, nil
}

func (s *Server) GetSessionCount(ctx context.Context, request *GetSessionCountRequest) (*GetSessionCountReply, error) {
	statsGrpcServerCalls.WithLabelValues("SessionCount").Inc()

	u, err := url.Parse(request.Url)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid url")
	}

	backend := s.hub.GetBackend(u)
	if backend == nil {
		return nil, status.Error(codes.NotFound, "no such backend")
	}

	return &GetSessionCountReply{
		Count: uint32(backend.Len()),
	}, nil
}

func (s *Server) ProxySession(request RpcSessions_ProxySessionServer) error {
	statsGrpcServerCalls.WithLabelValues("ProxySession").Inc()

	return s.hub.ProxySession(request)
}
