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
package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	log    *zap.Logger
	hub    *Hub
	client RpcSessions_ProxySessionServer

	sessionId  string
	remoteAddr string
	country    string
	userAgent  string

	closeCtx  context.Context
	closeFunc context.CancelCauseFunc

	session  atomic.Pointer[Session]
	messages chan WritableClientMessage
}

func newRemoteGrpcClient(log *zap.Logger, hub *Hub, request RpcSessions_ProxySessionServer) (*remoteGrpcClient, error) {
	md, found := metadata.FromIncomingContext(request.Context())
	if !found {
		return nil, errors.New("no metadata provided")
	}

	closeCtx, closeFunc := context.WithCancelCause(context.Background())

	result := &remoteGrpcClient{
		log:    log,
		hub:    hub,
		client: request,

		sessionId:  getMD(md, "sessionId"),
		remoteAddr: getMD(md, "remoteAddr"),
		country:    getMD(md, "country"),
		userAgent:  getMD(md, "userAgent"),

		closeCtx:  closeCtx,
		closeFunc: closeFunc,

		messages: make(chan WritableClientMessage, grpcRemoteClientMessageQueue),
	}
	return result, nil
}

func (c *remoteGrpcClient) readPump() {
	var closeError error
	defer func() {
		c.closeFunc(closeError)
		c.hub.OnClosed(c)
	}()

	for {
		msg, err := c.client.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Connection was closed locally.
				break
			}

			if status.Code(err) != codes.Canceled {
				c.log.Error("Error reading from remote client for session",
					zap.String("sessionid", c.sessionId),
					zap.Error(err),
				)
				closeError = err
			}
			break
		}

		c.hub.OnMessageReceived(c, msg.Message)
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

func (c *remoteGrpcClient) Country() string {
	return c.country
}

func (c *remoteGrpcClient) IsConnected() bool {
	return true
}

func (c *remoteGrpcClient) IsAuthenticated() bool {
	return c.GetSession() != nil
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

func (c *remoteGrpcClient) SendError(e *Error) bool {
	message := &ServerMessage{
		Type:  "error",
		Error: e,
	}
	return c.SendMessage(message)
}

func (c *remoteGrpcClient) SendByeResponse(message *ClientMessage) bool {
	return c.SendByeResponseWithReason(message, "")
}

func (c *remoteGrpcClient) SendByeResponseWithReason(message *ClientMessage, reason string) bool {
	response := &ServerMessage{
		Type: "bye",
	}
	if message != nil {
		response.Id = message.Id
	}
	if reason != "" {
		if response.Bye == nil {
			response.Bye = &ByeServerMessage{}
		}
		response.Bye.Reason = reason
	}
	return c.SendMessage(response)
}

func (c *remoteGrpcClient) SendMessage(message WritableClientMessage) bool {
	if c.closeCtx.Err() != nil {
		return false
	}

	select {
	case c.messages <- message:
		return true
	default:
		c.log.Warn("Message queue for remote client of session is full, not sending",
			zap.String("sessionid", c.sessionId),
			zap.Any("message", message),
		)
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
				c.log.Error("Error marshalling message for remote client for session",
					zap.Any("message", msg),
					zap.String("sessionid", c.sessionId),
					zap.Error(err),
				)
				continue
			}

			if err := c.client.Send(&ServerSessionMessage{
				Message: data,
			}); err != nil {
				return fmt.Errorf("error sending %+v to remote client for session %s: %w", msg, c.sessionId, err)
			}
		}
	}
}
