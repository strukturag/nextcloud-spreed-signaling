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
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
	status "google.golang.org/grpc/status"
)

const (
	GrpcTargetTypeStatic = "static"
	GrpcTargetTypeEtcd   = "etcd"

	DefaultGrpcTargetType = GrpcTargetTypeStatic
)

var (
	ErrNoSuchResumeId = fmt.Errorf("unknown resume id")

	customResolverPrefix atomic.Uint64
)

func init() {
	RegisterGrpcClientStats()
}

type grpcClientImpl struct {
	RpcBackendClient
	RpcInternalClient
	RpcMcuClient
	RpcSessionsClient
}

func newGrpcClientImpl(conn grpc.ClientConnInterface) *grpcClientImpl {
	return &grpcClientImpl{
		RpcBackendClient:  NewRpcBackendClient(conn),
		RpcInternalClient: NewRpcInternalClient(conn),
		RpcMcuClient:      NewRpcMcuClient(conn),
		RpcSessionsClient: NewRpcSessionsClient(conn),
	}
}

type GrpcClient struct {
	ip     net.IP
	target string
	conn   *grpc.ClientConn
	impl   *grpcClientImpl

	isSelf atomic.Bool
}

type customIpResolver struct {
	resolver.Builder
	resolver.Resolver

	scheme   string
	addr     string
	hostname string
}

func (r *customIpResolver) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	state := resolver.State{
		Addresses: []resolver.Address{
			{
				Addr:       r.addr,
				ServerName: r.hostname,
			},
		},
	}

	if err := cc.UpdateState(state); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *customIpResolver) Scheme() string {
	return r.scheme
}

func (r *customIpResolver) ResolveNow(opts resolver.ResolveNowOptions) {
	// Noop, we use a static configuration.
}

func (r *customIpResolver) Close() {
	// Noop
}

func NewGrpcClient(target string, ip net.IP, opts ...grpc.DialOption) (*GrpcClient, error) {
	var conn *grpc.ClientConn
	var err error
	if ip != nil {
		prefix := customResolverPrefix.Add(1)
		addr := ip.String()
		hostname := target
		if host, port, err := net.SplitHostPort(target); err == nil {
			addr = net.JoinHostPort(addr, port)
			hostname = host
		}
		resolver := &customIpResolver{
			scheme:   fmt.Sprintf("custom%d", prefix),
			addr:     addr,
			hostname: hostname,
		}
		opts = append(opts, grpc.WithResolvers(resolver))
		conn, err = grpc.NewClient(fmt.Sprintf("%s://%s", resolver.Scheme(), target), opts...)
	} else {
		conn, err = grpc.NewClient(target, opts...)
	}
	if err != nil {
		return nil, err
	}

	result := &GrpcClient{
		ip:     ip,
		target: target,
		conn:   conn,
		impl:   newGrpcClientImpl(conn),
	}

	if ip != nil {
		result.target += " (" + ip.String() + ")"
	}
	return result, nil
}

func (c *GrpcClient) Target() string {
	return c.target
}

func (c *GrpcClient) Close() error {
	return c.conn.Close()
}

func (c *GrpcClient) IsSelf() bool {
	return c.isSelf.Load()
}

func (c *GrpcClient) SetSelf(self bool) {
	c.isSelf.Store(self)
}

func (c *GrpcClient) GetServerId(ctx context.Context) (string, error) {
	statsGrpcClientCalls.WithLabelValues("GetServerId").Inc()
	response, err := c.impl.GetServerId(ctx, &GetServerIdRequest{}, grpc.WaitForReady(true))
	if err != nil {
		return "", err
	}

	return response.GetServerId(), nil
}

func (c *GrpcClient) LookupResumeId(ctx context.Context, resumeId string) (*LookupResumeIdReply, error) {
	statsGrpcClientCalls.WithLabelValues("LookupResumeId").Inc()
	// TODO: Remove debug logging
	log.Printf("Lookup resume id %s on %s", resumeId, c.Target())
	response, err := c.impl.LookupResumeId(ctx, &LookupResumeIdRequest{
		ResumeId: resumeId,
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return nil, ErrNoSuchResumeId
	} else if err != nil {
		return nil, err
	}

	if sessionId := response.GetSessionId(); sessionId == "" {
		return nil, ErrNoSuchResumeId
	}

	return response, nil
}

func (c *GrpcClient) LookupSessionId(ctx context.Context, roomSessionId string, disconnectReason string) (string, error) {
	statsGrpcClientCalls.WithLabelValues("LookupSessionId").Inc()
	// TODO: Remove debug logging
	log.Printf("Lookup room session %s on %s", roomSessionId, c.Target())
	response, err := c.impl.LookupSessionId(ctx, &LookupSessionIdRequest{
		RoomSessionId:    roomSessionId,
		DisconnectReason: disconnectReason,
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

func (c *GrpcClient) GetPublisherId(ctx context.Context, sessionId string, streamType StreamType) (string, string, net.IP, error) {
	statsGrpcClientCalls.WithLabelValues("GetPublisherId").Inc()
	// TODO: Remove debug logging
	log.Printf("Get %s publisher id %s on %s", streamType, sessionId, c.Target())
	response, err := c.impl.GetPublisherId(ctx, &GetPublisherIdRequest{
		SessionId:  sessionId,
		StreamType: string(streamType),
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return "", "", nil, nil
	} else if err != nil {
		return "", "", nil, err
	}

	return response.GetPublisherId(), response.GetProxyUrl(), net.ParseIP(response.GetIp()), nil
}

func (c *GrpcClient) GetSessionCount(ctx context.Context, u *url.URL) (uint32, error) {
	statsGrpcClientCalls.WithLabelValues("GetSessionCount").Inc()
	// TODO: Remove debug logging
	log.Printf("Get session count for %s on %s", u, c.Target())
	response, err := c.impl.GetSessionCount(ctx, &GetSessionCountRequest{
		Url: u.String(),
	}, grpc.WaitForReady(true))
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	return response.GetCount(), nil
}

type ProxySessionReceiver interface {
	RemoteAddr() string
	Country() string
	UserAgent() string

	OnProxyMessage(message *ServerSessionMessage) error
	OnProxyClose(err error)
}

type SessionProxy struct {
	sessionId string
	receiver  ProxySessionReceiver

	sendMu sync.Mutex
	client RpcSessions_ProxySessionClient
}

func (p *SessionProxy) recvPump() {
	var closeError error
	defer func() {
		p.receiver.OnProxyClose(closeError)
		if err := p.Close(); err != nil {
			log.Printf("Error closing proxy for session %s: %s", p.sessionId, err)
		}
	}()

	for {
		msg, err := p.client.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			log.Printf("Error receiving message from proxy for session %s: %s", p.sessionId, err)
			closeError = err
			break
		}

		if err := p.receiver.OnProxyMessage(msg); err != nil {
			log.Printf("Error processing message %+v from proxy for session %s: %s", msg, p.sessionId, err)
		}
	}
}

func (p *SessionProxy) Send(message *ClientSessionMessage) error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	return p.client.Send(message)
}

func (p *SessionProxy) Close() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	return p.client.CloseSend()
}

func (c *GrpcClient) ProxySession(ctx context.Context, sessionId string, receiver ProxySessionReceiver) (*SessionProxy, error) {
	statsGrpcClientCalls.WithLabelValues("ProxySession").Inc()
	md := metadata.Pairs(
		"sessionId", sessionId,
		"remoteAddr", receiver.RemoteAddr(),
		"country", receiver.Country(),
		"userAgent", receiver.UserAgent(),
	)
	client, err := c.impl.ProxySession(metadata.NewOutgoingContext(ctx, md), grpc.WaitForReady(true))
	if err != nil {
		return nil, err
	}

	proxy := &SessionProxy{
		sessionId: sessionId,
		receiver:  receiver,

		client: client,
	}

	go proxy.recvPump()
	return proxy, nil
}

type grpcClientsList struct {
	clients []*GrpcClient
	entry   *DnsMonitorEntry
}

type GrpcClients struct {
	mu sync.RWMutex

	clientsMap map[string]*grpcClientsList
	clients    []*GrpcClient

	dnsMonitor   *DnsMonitor
	dnsDiscovery bool

	etcdClient        *EtcdClient
	targetPrefix      string
	targetInformation map[string]*GrpcTargetInformationEtcd
	dialOptions       atomic.Value // []grpc.DialOption
	creds             credentials.TransportCredentials

	initializedCtx       context.Context
	initializedFunc      context.CancelFunc
	wakeupChanForTesting chan struct{}
	selfCheckWaitGroup   sync.WaitGroup

	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewGrpcClients(config *goconf.ConfigFile, etcdClient *EtcdClient, dnsMonitor *DnsMonitor) (*GrpcClients, error) {
	initializedCtx, initializedFunc := context.WithCancel(context.Background())
	closeCtx, closeFunc := context.WithCancel(context.Background())
	result := &GrpcClients{
		dnsMonitor:      dnsMonitor,
		etcdClient:      etcdClient,
		initializedCtx:  initializedCtx,
		initializedFunc: initializedFunc,
		closeCtx:        closeCtx,
		closeFunc:       closeFunc,
	}
	if err := result.load(config, false); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *GrpcClients) load(config *goconf.ConfigFile, fromReload bool) error {
	creds, err := NewReloadableCredentials(config, false)
	if err != nil {
		return err
	}

	if c.creds != nil {
		if cr, ok := c.creds.(*reloadableCredentials); ok {
			cr.Close()
		}
	}
	c.creds = creds

	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	c.dialOptions.Store(opts)

	targetType, _ := config.GetString("grpc", "targettype")
	if targetType == "" {
		targetType = DefaultGrpcTargetType
	}

	switch targetType {
	case GrpcTargetTypeStatic:
		err = c.loadTargetsStatic(config, fromReload, opts...)
	case GrpcTargetTypeEtcd:
		err = c.loadTargetsEtcd(config, fromReload, opts...)
	default:
		err = fmt.Errorf("unknown GRPC target type: %s", targetType)
	}
	return err
}

func (c *GrpcClients) closeClient(client *GrpcClient) {
	if client.IsSelf() {
		// Already closed.
		return
	}

	if err := client.Close(); err != nil {
		log.Printf("Error closing client to %s: %s", client.Target(), err)
	}
}

func (c *GrpcClients) isClientAvailable(target string, client *GrpcClient) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, found := c.clientsMap[target]
	if !found {
		return false
	}

	for _, entry := range entries.clients {
		if entry == client {
			return true
		}
	}

	return false
}

func (c *GrpcClients) getServerIdWithTimeout(ctx context.Context, client *GrpcClient) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	id, err := client.GetServerId(ctx2)
	return id, err
}

func (c *GrpcClients) checkIsSelf(ctx context.Context, target string, client *GrpcClient) {
	backoff, _ := NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
	defer c.selfCheckWaitGroup.Done()

loop:
	for {
		select {
		case <-ctx.Done():
			// Cancelled
			return
		default:
			if !c.isClientAvailable(target, client) {
				return
			}

			id, err := c.getServerIdWithTimeout(ctx, client)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}

				if status.Code(err) != codes.Canceled {
					log.Printf("Error checking GRPC server id of %s, retrying in %s: %s", client.Target(), backoff.NextWait(), err)
				}
				backoff.Wait(ctx)
				continue
			}

			if id == GrpcServerId {
				log.Printf("GRPC target %s is this server, removing", client.Target())
				c.closeClient(client)
				client.SetSelf(true)
			} else {
				log.Printf("Checked GRPC server id of %s", client.Target())
			}
			break loop
		}
	}
}

func (c *GrpcClients) loadTargetsStatic(config *goconf.ConfigFile, fromReload bool, opts ...grpc.DialOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dnsDiscovery, _ := config.GetBool("grpc", "dnsdiscovery")
	if dnsDiscovery != c.dnsDiscovery {
		if !dnsDiscovery {
			for _, entry := range c.clientsMap {
				if entry.entry != nil {
					c.dnsMonitor.Remove(entry.entry)
					entry.entry = nil
				}
			}
		}
		c.dnsDiscovery = dnsDiscovery
	}

	clientsMap := make(map[string]*grpcClientsList)
	var clients []*GrpcClient
	removeTargets := make(map[string]bool, len(c.clientsMap))
	for target, entries := range c.clientsMap {
		removeTargets[target] = true
		clientsMap[target] = entries
	}

	targets, _ := config.GetString("grpc", "targets")
	for _, target := range strings.Split(targets, ",") {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		if entries, found := clientsMap[target]; found {
			clients = append(clients, entries.clients...)
			if dnsDiscovery && entries.entry == nil {
				entry, err := c.dnsMonitor.Add(target, c.onLookup)
				if err != nil {
					return err
				}

				entries.entry = entry
			}
			delete(removeTargets, target)
			continue
		}

		host := target
		if h, _, err := net.SplitHostPort(target); err == nil {
			host = h
		}

		if dnsDiscovery && net.ParseIP(host) == nil {
			// Use dedicated client for each IP address.
			entry, err := c.dnsMonitor.Add(target, c.onLookup)
			if err != nil {
				return err
			}

			clientsMap[target] = &grpcClientsList{
				entry: entry,
			}
			continue
		}

		client, err := NewGrpcClient(target, nil, opts...)
		if err != nil {
			for _, entry := range clientsMap {
				for _, client := range entry.clients {
					c.closeClient(client)
				}

				if entry.entry != nil {
					c.dnsMonitor.Remove(entry.entry)
					entry.entry = nil
				}
			}
			return err
		}

		c.selfCheckWaitGroup.Add(1)
		go c.checkIsSelf(c.closeCtx, target, client)

		log.Printf("Adding %s as GRPC target", client.Target())
		entry, found := clientsMap[target]
		if !found {
			entry = &grpcClientsList{}
			clientsMap[target] = entry
		}
		entry.clients = append(entry.clients, client)
		clients = append(clients, client)
	}

	for target := range removeTargets {
		if entry, found := clientsMap[target]; found {
			for _, client := range entry.clients {
				log.Printf("Deleting GRPC target %s", client.Target())
				c.closeClient(client)
			}

			if entry.entry != nil {
				c.dnsMonitor.Remove(entry.entry)
				entry.entry = nil
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

func (c *GrpcClients) onLookup(entry *DnsMonitorEntry, all []net.IP, added []net.IP, keep []net.IP, removed []net.IP) {
	c.mu.Lock()
	defer c.mu.Unlock()

	target := entry.URL()
	e, found := c.clientsMap[target]
	if !found {
		return
	}

	opts := c.dialOptions.Load().([]grpc.DialOption)

	mapModified := false
	var newClients []*GrpcClient
	for _, ip := range removed {
		for _, client := range e.clients {
			if ip.Equal(client.ip) {
				mapModified = true
				log.Printf("Removing connection to %s", client.Target())
				c.closeClient(client)
				c.wakeupForTesting()
			}
		}
	}

	for _, ip := range keep {
		for _, client := range e.clients {
			if ip.Equal(client.ip) {
				newClients = append(newClients, client)
			}
		}
	}

	for _, ip := range added {
		client, err := NewGrpcClient(target, ip, opts...)
		if err != nil {
			log.Printf("Error creating client to %s with IP %s: %s", target, ip.String(), err)
			continue
		}

		c.selfCheckWaitGroup.Add(1)
		go c.checkIsSelf(c.closeCtx, target, client)

		log.Printf("Adding %s as GRPC target", client.Target())
		newClients = append(newClients, client)
		mapModified = true
		c.wakeupForTesting()
	}

	if mapModified {
		c.clientsMap[target].clients = newClients

		c.clients = make([]*GrpcClient, 0, len(c.clientsMap))
		for _, entry := range c.clientsMap {
			c.clients = append(c.clients, entry.clients...)
		}
		statsGrpcClients.Set(float64(len(c.clients)))
	}
}

func (c *GrpcClients) loadTargetsEtcd(config *goconf.ConfigFile, fromReload bool, opts ...grpc.DialOption) error {
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

	c.etcdClient.AddListener(c)
	return nil
}

func (c *GrpcClients) EtcdClientCreated(client *EtcdClient) {
	go func() {
		if err := client.WaitForConnection(c.closeCtx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			panic(err)
		}

		backoff, _ := NewExponentialBackoff(initialWaitDelay, maxWaitDelay)
		var nextRevision int64
		for c.closeCtx.Err() == nil {
			response, err := c.getGrpcTargets(c.closeCtx, client, c.targetPrefix)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				} else if errors.Is(err, context.DeadlineExceeded) {
					log.Printf("Timeout getting initial list of GRPC targets, retry in %s", backoff.NextWait())
				} else {
					log.Printf("Could not get initial list of GRPC targets, retry in %s: %s", backoff.NextWait(), err)
				}

				backoff.Wait(c.closeCtx)
				continue
			}

			for _, ev := range response.Kvs {
				c.EtcdKeyUpdated(client, string(ev.Key), ev.Value, nil)
			}
			c.initializedFunc()
			nextRevision = response.Header.Revision + 1
			break
		}

		prevRevision := nextRevision
		backoff.Reset()
		for c.closeCtx.Err() == nil {
			var err error
			if nextRevision, err = client.Watch(c.closeCtx, c.targetPrefix, nextRevision, c, clientv3.WithPrefix()); err != nil {
				log.Printf("Error processing watch for %s (%s), retry in %s", c.targetPrefix, err, backoff.NextWait())
				backoff.Wait(c.closeCtx)
				continue
			}

			if nextRevision != prevRevision {
				backoff.Reset()
				prevRevision = nextRevision
			} else {
				log.Printf("Processing watch for %s interrupted, retry in %s", c.targetPrefix, backoff.NextWait())
				backoff.Wait(c.closeCtx)
			}
		}
	}()
}

func (c *GrpcClients) EtcdWatchCreated(client *EtcdClient, key string) {
}

func (c *GrpcClients) getGrpcTargets(ctx context.Context, client *EtcdClient, targetPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	return client.Get(ctx, targetPrefix, clientv3.WithPrefix())
}

func (c *GrpcClients) EtcdKeyUpdated(client *EtcdClient, key string, data []byte, prevValue []byte) {
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

	if _, found := c.clientsMap[info.Address]; found {
		log.Printf("GRPC target %s already exists, ignoring %s", info.Address, key)
		return
	}

	opts := c.dialOptions.Load().([]grpc.DialOption)
	cl, err := NewGrpcClient(info.Address, nil, opts...)
	if err != nil {
		log.Printf("Could not create GRPC client for target %s: %s", info.Address, err)
		return
	}

	c.selfCheckWaitGroup.Add(1)
	go c.checkIsSelf(c.closeCtx, info.Address, cl)

	log.Printf("Adding %s as GRPC target", cl.Target())

	if c.clientsMap == nil {
		c.clientsMap = make(map[string]*grpcClientsList)
	}
	c.clientsMap[info.Address] = &grpcClientsList{
		clients: []*GrpcClient{cl},
	}
	c.clients = append(c.clients, cl)
	c.targetInformation[key] = &info
	statsGrpcClients.Inc()
	c.wakeupForTesting()
}

func (c *GrpcClients) EtcdKeyDeleted(client *EtcdClient, key string, prevValue []byte) {
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
	entry, found := c.clientsMap[info.Address]
	if !found {
		return
	}

	for _, client := range entry.clients {
		log.Printf("Removing connection to %s (from %s)", client.Target(), key)
		c.closeClient(client)
	}
	delete(c.clientsMap, info.Address)
	c.clients = make([]*GrpcClient, 0, len(c.clientsMap))
	for _, entry := range c.clientsMap {
		c.clients = append(c.clients, entry.clients...)
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
	case c.wakeupChanForTesting <- struct{}{}:
	default:
	}
}

func (c *GrpcClients) Reload(config *goconf.ConfigFile) {
	if err := c.load(config, true); err != nil {
		log.Printf("Could not reload RPC clients: %s", err)
	}
}

func (c *GrpcClients) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, entry := range c.clientsMap {
		for _, client := range entry.clients {
			if err := client.Close(); err != nil {
				log.Printf("Error closing client to %s: %s", client.Target(), err)
			}
		}

		if entry.entry != nil {
			c.dnsMonitor.Remove(entry.entry)
			entry.entry = nil
		}
	}

	c.clients = nil
	c.clientsMap = nil
	c.dnsDiscovery = false

	if c.etcdClient != nil {
		c.etcdClient.RemoveListener(c)
	}
	if c.creds != nil {
		if cr, ok := c.creds.(*reloadableCredentials); ok {
			cr.Close()
		}
	}
	c.closeFunc()
}

func (c *GrpcClients) GetClients() []*GrpcClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.clients) == 0 {
		return c.clients
	}

	result := make([]*GrpcClient, 0, len(c.clients)-1)
	for _, client := range c.clients {
		if client.IsSelf() {
			continue
		}

		result = append(result, client)
	}
	return result
}
