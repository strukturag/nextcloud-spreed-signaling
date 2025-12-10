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
package signaling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	initialConnectInterval = time.Second
	maxConnectInterval     = 8 * time.Second

	NatsLoopbackUrl = "nats://loopback"
)

type NatsSubscription interface {
	Unsubscribe() error
}

type NatsClient interface {
	Close(ctx context.Context) error

	Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error)
	Publish(subject string, message any) error
}

// The NATS client doesn't work if a subject contains spaces. As the room id
// can have an arbitrary format, we need to make sure the subject is valid.
// See "https://github.com/nats-io/nats.js/issues/158" for a similar report.
func GetEncodedSubject(prefix string, suffix string) string {
	return prefix + "." + base64.StdEncoding.EncodeToString([]byte(suffix))
}

type natsClient struct {
	logger log.Logger
	conn   *nats.Conn
	closed chan struct{}
}

func NewNatsClient(ctx context.Context, url string, options ...nats.Option) (NatsClient, error) {
	logger := log.LoggerFromContext(ctx)
	if url == ":loopback:" {
		logger.Printf("WARNING: events url %s is deprecated, please use %s instead", url, NatsLoopbackUrl)
		url = NatsLoopbackUrl
	}
	if url == NatsLoopbackUrl {
		logger.Println("Using internal NATS loopback client")
		return NewLoopbackNatsClient(logger)
	}

	backoff, err := async.NewExponentialBackoff(initialConnectInterval, maxConnectInterval)
	if err != nil {
		return nil, err
	}

	client := &natsClient{
		logger: logger,
		closed: make(chan struct{}),
	}

	options = append([]nats.Option{
		nats.ClosedHandler(client.onClosed),
		nats.DisconnectHandler(client.onDisconnected),
		nats.ReconnectHandler(client.onReconnected),
		nats.MaxReconnects(-1),
	}, options...)
	client.conn, err = nats.Connect(url, options...)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// The initial connect must succeed, so we retry in the case of an error.
	for err != nil {
		logger.Printf("Could not create connection (%s), will retry in %s", err, backoff.NextWait())
		backoff.Wait(ctx)
		if ctx.Err() != nil {
			return nil, errors.New("interrupted")
		}

		client.conn, err = nats.Connect(url)
	}
	logger.Printf("Connection established to %s (%s)", removeURLCredentials(client.conn.ConnectedUrl()), client.conn.ConnectedServerId())
	return client, nil
}

func (c *natsClient) Close(ctx context.Context) error {
	c.conn.Close()
	select {
	case <-c.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *natsClient) onClosed(conn *nats.Conn) {
	if err := conn.LastError(); err != nil {
		c.logger.Printf("NATS client closed, last error %s", conn.LastError())
	} else {
		c.logger.Println("NATS client closed")
	}
	close(c.closed)
}

func (c *natsClient) onDisconnected(conn *nats.Conn) {
	c.logger.Println("NATS client disconnected")
}

func (c *natsClient) onReconnected(conn *nats.Conn) {
	c.logger.Printf("NATS client reconnected to %s (%s)", conn.ConnectedUrl(), conn.ConnectedServerId())
}

func (c *natsClient) Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error) {
	return c.conn.ChanSubscribe(subject, ch)
}

func (c *natsClient) Publish(subject string, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return c.conn.Publish(subject, data)
}

func NatsDecode(msg *nats.Msg, vPtr any) (err error) {
	switch arg := vPtr.(type) {
	case *string:
		// If they want a string and it is a JSON string, strip quotes
		// This allows someone to send a struct but receive as a plain string
		// This cast should be efficient for Go 1.3 and beyond.
		str := string(msg.Data)
		if strings.HasPrefix(str, `"`) && strings.HasSuffix(str, `"`) {
			*arg = str[1 : len(str)-1]
		} else {
			*arg = str
		}
	case *[]byte:
		*arg = msg.Data
	default:
		err = json.Unmarshal(msg.Data, arg)
	}
	return
}

func removeURLCredentials(u string) string {
	if u, err := url.Parse(u); err == nil && u.User != nil {
		u.User = url.User("***")
		return u.String()
	}
	return u
}
