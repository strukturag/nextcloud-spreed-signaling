/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/client"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/grpc"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	grpcRemoteClientMessageQueue = 16
)

func getMD(md metadata.MD, key string) string {
	if values := md.Get(key); len(values) > 0 {
		return values[0]
	}

	return ""
}

// remoteGrpcClient is a remote client connecting from a GRPC proxy to a Hub.
type remoteGrpcClient struct {
	logger log.Logger
	hub    *Hub
	client grpc.RpcSessions_ProxySessionServer

	sessionId  api.PublicSessionId
	remoteAddr string
	country    geoip.Country
	userAgent  string

	closeCtx  context.Context
	closeFunc context.CancelCauseFunc

	session  atomic.Pointer[Session]
	messages chan client.WritableClientMessage
}

func newRemoteGrpcClient(hub *Hub, request grpc.RpcSessions_ProxySessionServer) (*remoteGrpcClient, error) {
	md, found := metadata.FromIncomingContext(request.Context())
	if !found {
		return nil, errors.New("no metadata provided")
	}

	closeCtx, closeFunc := context.WithCancelCause(context.Background())

	result := &remoteGrpcClient{
		logger: hub.logger,
		hub:    hub,
		client: request,

		sessionId:  api.PublicSessionId(getMD(md, "sessionId")),
		remoteAddr: getMD(md, "remoteAddr"),
		country:    geoip.Country(getMD(md, "country")),
		userAgent:  getMD(md, "userAgent"),

		closeCtx:  closeCtx,
		closeFunc: closeFunc,

		messages: make(chan client.WritableClientMessage, grpcRemoteClientMessageQueue),
	}
	return result, nil
}

func (c *remoteGrpcClient) readPump() {
	var closeError error
	defer func() {
		c.closeFunc(closeError)
		c.hub.processUnregister(c)
	}()

	for {
		msg, err := c.client.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Connection was closed locally.
				break
			}

			if status.Code(err) != codes.Canceled {
				c.logger.Printf("Error reading from remote client for session %s: %s", c.sessionId, err)
				closeError = err
			}
			break
		}

		c.hub.processMessage(c, msg.Message)
	}
}

func (c *remoteGrpcClient) Context() context.Context {
	return c.client.Context()
}

func (c *remoteGrpcClient) RemoteAddr() string {
	return c.remoteAddr
}

func (c *remoteGrpcClient) UserAgent() string {
	return c.userAgent
}

func (c *remoteGrpcClient) Country() geoip.Country {
	return c.country
}

func (c *remoteGrpcClient) IsConnected() bool {
	return true
}

func (c *remoteGrpcClient) IsAuthenticated() bool {
	return c.GetSession() != nil
}

func (c *remoteGrpcClient) GetSessionId() api.PublicSessionId {
	return c.sessionId
}

func (c *remoteGrpcClient) GetSession() Session {
	session := c.session.Load()
	if session == nil {
		return nil
	}

	return *session
}

func (c *remoteGrpcClient) SetSession(session Session) {
	if session == nil {
		c.session.Store(nil)
	} else {
		c.session.Store(&session)
	}
}

func (c *remoteGrpcClient) SendError(e *api.Error) bool {
	message := &api.ServerMessage{
		Type:  "error",
		Error: e,
	}
	return c.SendMessage(message)
}

func (c *remoteGrpcClient) SendByeResponse(message *api.ClientMessage) bool {
	return c.SendByeResponseWithReason(message, "")
}

func (c *remoteGrpcClient) SendByeResponseWithReason(message *api.ClientMessage, reason string) bool {
	response := &api.ServerMessage{
		Type: "bye",
	}
	if message != nil {
		response.Id = message.Id
	}
	if reason != "" {
		if response.Bye == nil {
			response.Bye = &api.ByeServerMessage{}
		}
		response.Bye.Reason = reason
	}
	return c.SendMessage(response)
}

func (c *remoteGrpcClient) SendMessage(message client.WritableClientMessage) bool {
	if c.closeCtx.Err() != nil {
		return false
	}

	select {
	case c.messages <- message:
		return true
	default:
		c.logger.Printf("Message queue for remote client of session %s is full, not sending %+v", c.sessionId, message)
		return false
	}
}

func (c *remoteGrpcClient) Close() {
	c.closeFunc(nil)
}

func (c *remoteGrpcClient) run() error {
	go c.readPump()

	for {
		select {
		case <-c.closeCtx.Done():
			if err := context.Cause(c.closeCtx); err != context.Canceled {
				return err
			}
			return nil
		case msg := <-c.messages:
			data, err := json.Marshal(msg)
			if err != nil {
				c.logger.Printf("Error marshalling %+v for remote client for session %s: %s", msg, c.sessionId, err)
				continue
			}

			if err := c.client.Send(&grpc.ServerSessionMessage{
				Message: data,
			}); err != nil {
				return fmt.Errorf("error sending %+v to remote client for session %s: %w", msg, c.sessionId, err)
			}
		}
	}
}
