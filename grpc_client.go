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
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/dlintw/goconf"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	status "google.golang.org/grpc/status"
)

type grpcClientImpl struct {
	RpcMcuClient
	RpcSessionsClient
}

func newGrpcClientImpl(conn grpc.ClientConnInterface) *grpcClientImpl {
	return &grpcClientImpl{
		RpcMcuClient:      NewRpcMcuClient(conn),
		RpcSessionsClient: NewRpcSessionsClient(conn),
	}
}

type GrpcClient struct {
	conn *grpc.ClientConn
	impl *grpcClientImpl
}

func NewGrpcClient(target string, opts ...grpc.DialOption) (*GrpcClient, error) {
	conn, err := grpc.Dial(target, opts...)
	if err != nil {
		return nil, err
	}

	result := &GrpcClient{
		conn: conn,
		impl: newGrpcClientImpl(conn),
	}
	return result, nil
}

func (c *GrpcClient) Target() string {
	return c.conn.Target()
}

func (c *GrpcClient) Close() error {
	return c.conn.Close()
}

func (c *GrpcClient) LookupSessionId(ctx context.Context, roomSessionId string) (string, error) {
	// TODO: Remove debug logging
	log.Printf("Lookup room session %s on %s", roomSessionId, c.Target())
	response, err := c.impl.LookupSessionId(ctx, &LookupSessionIdRequest{
		RoomSessionId: roomSessionId,
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return "", ErrNoSuchRoomSession
	} else if err != nil {
		return "", err
	}

	sessionId := response.GetSessionId()
	if sessionId == "" {
		return "", ErrNoSuchRoomSession
	}

	return sessionId, nil
}

func (c *GrpcClient) IsSessionInCall(ctx context.Context, sessionId string, room *Room) (bool, error) {
	// TODO: Remove debug logging
	log.Printf("Check if session %s is in call %s on %s", sessionId, room.Id(), c.Target())
	response, err := c.impl.IsSessionInCall(ctx, &IsSessionInCallRequest{
		SessionId:  sessionId,
		RoomId:     room.Id(),
		BackendUrl: room.Backend().url,
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return false, nil
	} else if err != nil {
		return false, err
	}

	return response.GetInCall(), nil
}

func (c *GrpcClient) GetPublisherId(ctx context.Context, sessionId string, streamType string) (string, string, net.IP, error) {
	// TODO: Remove debug logging
	log.Printf("Get %s publisher id %s on %s", streamType, sessionId, c.Target())
	response, err := c.impl.GetPublisherId(ctx, &GetPublisherIdRequest{
		SessionId:  sessionId,
		StreamType: streamType,
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return "", "", nil, nil
	} else if err != nil {
		return "", "", nil, err
	}

	return response.GetPublisherId(), response.GetProxyUrl(), net.ParseIP(response.GetIp()), nil
}

type GrpcClients struct {
	mu sync.RWMutex

	clientsMap map[string]*GrpcClient
	clients    []*GrpcClient
}

func NewGrpcClients(config *goconf.ConfigFile) (*GrpcClients, error) {
	result := &GrpcClients{}
	if err := result.load(config); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *GrpcClients) load(config *goconf.ConfigFile) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var opts []grpc.DialOption
	caFile, _ := config.GetString("grpc", "ca")
	if caFile != "" {
		creds, err := credentials.NewClientTLSFromFile(caFile, "")
		if err != nil {
			return fmt.Errorf("invalid GRPC CA in %s: %w", caFile, err)
		}

		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		log.Printf("WARNING: No GRPC CA configured, expecting unencrypted connections")
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	clientsMap := make(map[string]*GrpcClient)
	var clients []*GrpcClient
	removeTargets := make(map[string]bool, len(c.clientsMap))
	for target, client := range c.clientsMap {
		removeTargets[target] = true
		clientsMap[target] = client
	}

	targets, _ := config.GetString("grpc", "targets")
	for _, target := range strings.Split(targets, ",") {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		if client, found := clientsMap[target]; found {
			clients = append(clients, client)
			delete(removeTargets, target)
			continue
		}

		client, err := NewGrpcClient(target, opts...)
		if err != nil {
			for target, client := range clientsMap {
				if closeerr := client.Close(); closeerr != nil {
					log.Printf("Error closing client to %s: %s", target, closeerr)
				}
			}
			return err
		}

		log.Printf("Adding %s as GRPC target", target)
		clientsMap[target] = client
		clients = append(clients, client)
	}

	for target := range removeTargets {
		if client, found := clientsMap[target]; found {
			log.Printf("Deleting GRPC target %s", target)
			if err := client.Close(); err != nil {
				log.Printf("Error closing client to %s: %s", target, err)
			}
			delete(clientsMap, target)
		}
	}

	c.clients = clients
	c.clientsMap = clientsMap
	return nil
}

func (c *GrpcClients) Reload(config *goconf.ConfigFile) {
	if err := c.load(config); err != nil {
		log.Printf("Could not reload RPC clients: %s", err)
	}
}

func (c *GrpcClients) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for target, client := range c.clientsMap {
		if err := client.Close(); err != nil {
			log.Printf("Error closing client to %s: %s", target, err)
		}
	}

	c.clients = nil
	c.clientsMap = nil
}

func (c *GrpcClients) GetClients() []*GrpcClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.clients
}
