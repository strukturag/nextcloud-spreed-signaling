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
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	initialConnectInterval = time.Second
	maxConnectInterval     = 8 * time.Second
)

type NatsMessage struct {
	SendTime time.Time `json:"sendtime"`

	Type string `json:"type"`

	Message *ServerMessage `json:"message,omitempty"`

	Room *BackendServerRoomRequest `json:"room,omitempty"`

	Permissions []Permission `json:"permissions,omitempty"`

	Id string `json:"id"`
}

type NatsSubscription interface {
	Unsubscribe() error
}

type NatsClient interface {
	Close()

	Subscribe(subject string, ch chan *nats.Msg) (NatsSubscription, error)

	Publish(subject string, message interface{}) error
	PublishNats(subject string, message *NatsMessage) error
	PublishMessage(subject string, message *ServerMessage) error
	PublishBackendServerRoomRequest(subject string, message *BackendServerRoomRequest) error

	Decode(msg *nats.Msg, v interface{}) error
}

// The NATS client doesn't work if a subject contains spaces. As the room id
// can have an arbitrary format, we need to make sure the subject is valid.
// See "https://github.com/nats-io/nats.js/issues/158" for a similar report.
func GetEncodedSubject(prefix string, suffix string) string {
	return prefix + "." + base64.StdEncoding.EncodeToString([]byte(suffix))
}

type natsClient struct {
	nc   *nats.Conn
	conn *nats.EncodedConn
}

func NewNatsClient(url string) (NatsClient, error) {
	if url == ":loopback:" {
		log.Println("No NATS url configured, using internal loopback client")
		return NewLoopbackNatsClient()
	}

	client := &natsClient{}

	var err error
	client.nc, err = nats.Connect(url,
		nats.ClosedHandler(client.onClosed),
		nats.DisconnectHandler(client.onDisconnected),
		nats.ReconnectHandler(client.onReconnected))

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	delay := initialConnectInterval
	timer := time.NewTimer(delay)
	// The initial connect must succeed, so we retry in the case of an error.
	for err != nil {
		log.Printf("Could not create connection (%s), will retry in %s", err, delay)
		timer.Reset(delay)
		select {
		case <-interrupt:
			return nil, fmt.Errorf("interrupted")
		case <-timer.C:
			// Retry connection
			delay = delay * 2
			if delay > maxConnectInterval {
				delay = maxConnectInterval
			}
		}

		client.nc, err = nats.Connect(url)
	}
	log.Printf("Connection established to %s (%s)", client.nc.ConnectedUrl(), client.nc.ConnectedServerId())

	// All communication will be JSON based.
	client.conn, _ = nats.NewEncodedConn(client.nc, nats.JSON_ENCODER)
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
	return c.nc.ChanSubscribe(subject, ch)
}

func (c *natsClient) Publish(subject string, message interface{}) error {
	return c.conn.Publish(subject, message)
}

func (c *natsClient) PublishNats(subject string, message *NatsMessage) error {
	return c.Publish(subject, message)
}

func (c *natsClient) PublishMessage(subject string, message *ServerMessage) error {
	msg := &NatsMessage{
		SendTime: time.Now(),
		Type:     "message",
		Message:  message,
	}
	return c.PublishNats(subject, msg)
}

func (c *natsClient) PublishBackendServerRoomRequest(subject string, message *BackendServerRoomRequest) error {
	msg := &NatsMessage{
		SendTime: time.Now(),
		Type:     "room",
		Room:     message,
	}
	return c.PublishNats(subject, msg)
}

func (c *natsClient) Decode(msg *nats.Msg, v interface{}) error {
	return c.conn.Enc.Decode(msg.Subject, msg.Data, v)
}
