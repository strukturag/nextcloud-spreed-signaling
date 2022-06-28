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
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	status "google.golang.org/grpc/status"
)

const (
	GrpcTargetTypeStatic = "static"
	GrpcTargetTypeEtcd   = "etcd"

	DefaultGrpcTargetType = GrpcTargetTypeStatic
)

func init() {
	RegisterGrpcClientStats()
}

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
	statsGrpcClientCalls.WithLabelValues("LookupSessionId").Inc()
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
	statsGrpcClientCalls.WithLabelValues("IsSessionInCall").Inc()
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
	statsGrpcClientCalls.WithLabelValues("GetPublisherId").Inc()
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

	etcdClient        *EtcdClient
	targetPrefix      string
	targetSelf        string
	targetInformation map[string]*GrpcTargetInformationEtcd
	dialOptions       atomic.Value // []grpc.DialOption

	initializedCtx       context.Context
	initializedFunc      context.CancelFunc
	wakeupChanForTesting chan bool
}

func NewGrpcClients(config *goconf.ConfigFile, etcdClient *EtcdClient) (*GrpcClients, error) {
	initializedCtx, initializedFunc := context.WithCancel(context.Background())
	result := &GrpcClients{
		etcdClient:      etcdClient,
		initializedCtx:  initializedCtx,
		initializedFunc: initializedFunc,
	}
	if err := result.load(config); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *GrpcClients) load(config *goconf.ConfigFile) error {
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

	targetType, _ := config.GetString("grpc", "targettype")
	if targetType == "" {
		targetType = DefaultGrpcTargetType
	}

	switch targetType {
	case GrpcTargetTypeStatic:
		return c.loadTargetsStatic(config, opts...)
	case GrpcTargetTypeEtcd:
		return c.loadTargetsEtcd(config, opts...)
	default:
		return fmt.Errorf("unknown GRPC target type: %s", targetType)
	}
}

func (c *GrpcClients) loadTargetsStatic(config *goconf.ConfigFile, opts ...grpc.DialOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

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
	c.initializedFunc()
	statsGrpcClients.Set(float64(len(clients)))
	return nil
}

func (c *GrpcClients) loadTargetsEtcd(config *goconf.ConfigFile, opts ...grpc.DialOption) error {
	if !c.etcdClient.IsConfigured() {
		return fmt.Errorf("No etcd endpoints configured")
	}

	targetPrefix, _ := config.GetString("grpc", "targetprefix")
	if targetPrefix == "" {
		return fmt.Errorf("No GRPC target prefix configured")
	}
	c.targetPrefix = targetPrefix
	if c.targetInformation == nil {
		c.targetInformation = make(map[string]*GrpcTargetInformationEtcd)
	}

	targetSelf, _ := config.GetString("grpc", "targetself")
	c.targetSelf = targetSelf

	if opts == nil {
		opts = make([]grpc.DialOption, 0)
	}
	c.dialOptions.Store(opts)

	c.etcdClient.AddListener(c)
	return nil
}

func (c *GrpcClients) EtcdClientCreated(client *EtcdClient) {
	go func() {
		if err := client.Watch(context.Background(), c.targetPrefix, c, clientv3.WithPrefix()); err != nil {
			log.Printf("Error processing watch for %s: %s", c.targetPrefix, err)
		}
	}()

	go func() {
		client.WaitForConnection()

		waitDelay := initialWaitDelay
		for {
			response, err := c.getGrpcTargets(client, c.targetPrefix)
			if err != nil {
				if err == context.DeadlineExceeded {
					log.Printf("Timeout getting initial list of GRPC targets, retry in %s", waitDelay)
				} else {
					log.Printf("Could not get initial list of GRPC targets, retry in %s: %s", waitDelay, err)
				}

				time.Sleep(waitDelay)
				waitDelay = waitDelay * 2
				if waitDelay > maxWaitDelay {
					waitDelay = maxWaitDelay
				}
				continue
			}

			for _, ev := range response.Kvs {
				c.EtcdKeyUpdated(client, string(ev.Key), ev.Value)
			}
			c.initializedFunc()
			return
		}
	}()
}

func (c *GrpcClients) getGrpcTargets(client *EtcdClient, targetPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	return client.Get(ctx, targetPrefix, clientv3.WithPrefix())
}

func (c *GrpcClients) EtcdKeyUpdated(client *EtcdClient, key string, data []byte) {
	var info GrpcTargetInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		log.Printf("Could not decode GRPC target %s=%s: %s", key, string(data), err)
		return
	}
	if err := info.CheckValid(); err != nil {
		log.Printf("Received invalid GRPC target %s=%s: %s", key, string(data), err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	prev, found := c.targetInformation[key]
	if found && prev.Address != info.Address {
		// Address of endpoint has changed, remove old one.
		c.removeEtcdClientLocked(key)
	}

	if c.targetSelf != "" && info.Address == c.targetSelf {
		log.Printf("GRPC target %s is this server, ignoring %s", info.Address, key)
		c.wakeupForTesting()
		return
	}

	if _, found := c.clientsMap[info.Address]; found {
		log.Printf("GRPC target %s already exists, ignoring %s", info.Address, key)
		return
	}

	opts := c.dialOptions.Load().([]grpc.DialOption)
	cl, err := NewGrpcClient(info.Address, opts...)
	if err != nil {
		log.Printf("Could not create GRPC client for target %s: %s", info.Address, err)
		return
	}

	log.Printf("Adding %s as GRPC target", info.Address)

	if c.clientsMap == nil {
		c.clientsMap = make(map[string]*GrpcClient)
	}
	c.clientsMap[info.Address] = cl
	c.clients = append(c.clients, cl)
	c.targetInformation[key] = &info
	statsGrpcClients.Inc()
	c.wakeupForTesting()
}

func (c *GrpcClients) EtcdKeyDeleted(client *EtcdClient, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeEtcdClientLocked(key)
}

func (c *GrpcClients) removeEtcdClientLocked(key string) {
	info, found := c.targetInformation[key]
	if !found {
		log.Printf("No connection found for %s, ignoring", key)
		c.wakeupForTesting()
		return
	}

	delete(c.targetInformation, key)
	client, found := c.clientsMap[info.Address]
	if !found {
		return
	}

	log.Printf("Removing connection to %s (from %s)", info.Address, key)
	if err := client.Close(); err != nil {
		log.Printf("Error closing client to %s: %s", client.Target(), err)
	}
	delete(c.clientsMap, info.Address)
	c.clients = make([]*GrpcClient, 0, len(c.clientsMap))
	for _, client := range c.clientsMap {
		c.clients = append(c.clients, client)
	}
	statsGrpcClients.Dec()
	c.wakeupForTesting()
}

func (c *GrpcClients) WaitForInitialized(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.initializedCtx.Done():
		return nil
	}
}

func (c *GrpcClients) wakeupForTesting() {
	if c.wakeupChanForTesting == nil {
		return
	}

	select {
	case c.wakeupChanForTesting <- true:
	default:
	}
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

	if c.etcdClient != nil {
		c.etcdClient.RemoveListener(c)
	}
}

func (c *GrpcClients) GetClients() []*GrpcClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.clients
}
