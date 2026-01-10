/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
package nats

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/nats-io/nats.go"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

type NativeClient struct {
	logger log.Logger
	conn   *nats.Conn
	closed chan struct{}
}

func (c *NativeClient) URLs() []string {
	return c.conn.Servers()
}

func (c *NativeClient) IsConnected() bool {
	return c.conn.IsConnected()
}

func (c *NativeClient) ConnectedUrl() string {
	return c.conn.ConnectedUrl()
}

func (c *NativeClient) ConnectedServerId() string {
	return c.conn.ConnectedServerId()
}

func (c *NativeClient) ConnectedServerVersion() string {
	return c.conn.ConnectedServerVersion()
}

func (c *NativeClient) ConnectedClusterName() string {
	return c.conn.ConnectedClusterName()
}

func (c *NativeClient) Close(ctx context.Context) error {
	c.conn.Close()
	select {
	case <-c.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *NativeClient) FlushWithContext(ctx context.Context) error {
	return c.conn.FlushWithContext(ctx)
}

func (c *NativeClient) onClosed(conn *nats.Conn) {
	if err := conn.LastError(); err != nil {
		c.logger.Printf("NATS client closed, last error %s", conn.LastError())
	} else {
		c.logger.Println("NATS client closed")
	}
	close(c.closed)
}

func (c *NativeClient) onDisconnected(conn *nats.Conn) {
	c.logger.Println("NATS client disconnected")
}

func (c *NativeClient) onReconnected(conn *nats.Conn) {
	c.logger.Printf("NATS client reconnected to %s (%s)", conn.ConnectedUrl(), conn.ConnectedServerId())
}

func (c *NativeClient) Subscribe(subject string, ch chan *Msg) (Subscription, error) {
	return c.conn.ChanSubscribe(subject, ch)
}

func (c *NativeClient) Publish(subject string, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return c.conn.Publish(subject, data)
}

func removeURLCredentials(u string) string {
	if u, err := url.Parse(u); err == nil && u.User != nil {
		u.User = url.User("***")
		return u.String()
	}
	return u
}
