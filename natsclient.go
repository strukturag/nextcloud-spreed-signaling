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
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
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
	Close()

	Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error)
	Publish(subject string, message interface{}) error

	Decode(msg *nats.Msg, v interface{}) error
}

// The NATS client doesn't work if a subject contains spaces. As the room id
// can have an arbitrary format, we need to make sure the subject is valid.
// See "https://github.com/nats-io/nats.js/issues/158" for a similar report.
func GetEncodedSubject(prefix string, suffix string) string {
	return prefix + "." + base64.StdEncoding.EncodeToString([]byte(suffix))
}

type natsClient struct {
	conn *nats.Conn
}

func NewNatsClient(url string) (NatsClient, error) {
	if url == ":loopback:" {
		log.Printf("WARNING: events url %s is deprecated, please use %s instead", url, NatsLoopbackUrl)
		url = NatsLoopbackUrl
	}
	if url == NatsLoopbackUrl {
		log.Println("Using internal NATS loopback client")
		return NewLoopbackNatsClient()
	}

	backoff, err := NewExponentialBackoff(initialConnectInterval, maxConnectInterval)
	if err != nil {
		return nil, err
	}

	client := &natsClient{}

	client.conn, err = nats.Connect(url,
		nats.ClosedHandler(client.onClosed),
		nats.DisconnectHandler(client.onDisconnected),
		nats.ReconnectHandler(client.onReconnected))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The initial connect must succeed, so we retry in the case of an error.
	for err != nil {
		log.Printf("Could not create connection (%s), will retry in %s", err, backoff.NextWait())
		backoff.Wait(ctx)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("interrupted")
		}

		client.conn, err = nats.Connect(url)
	}
	log.Printf("Connection established to %s (%s)", client.conn.ConnectedUrl(), client.conn.ConnectedServerId())
	return client, nil
}

func (c *natsClient) Close() {
	c.conn.Close()
}

func (c *natsClient) onClosed(conn *nats.Conn) {
	log.Println("NATS client closed", conn.LastError())
}

func (c *natsClient) onDisconnected(conn *nats.Conn) {
	log.Println("NATS client disconnected")
}

func (c *natsClient) onReconnected(conn *nats.Conn) {
	log.Printf("NATS client reconnected to %s (%s)", conn.ConnectedUrl(), conn.ConnectedServerId())
}

func (c *natsClient) Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error) {
	return c.conn.ChanSubscribe(subject, ch)
}

func (c *natsClient) Publish(subject string, message interface{}) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return c.conn.Publish(subject, data)
}

func (c *natsClient) Decode(msg *nats.Msg, vPtr interface{}) (err error) {
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
