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
	"fmt"

	"golang.org/x/net/context"
)

const (
	McuTypeJanus = "janus"
	McuTypeProxy = "proxy"

	McuTypeDefault = McuTypeJanus
)

var (
	ErrNotConnected = fmt.Errorf("Not connected")
)

type McuListener interface {
	PublicId() string

	OnIceCandidate(client McuClient, candidate interface{})
	OnIceCompleted(client McuClient)

	PublisherClosed(publisher McuPublisher)
	SubscriberClosed(subscriber McuSubscriber)
}

type Mcu interface {
	Start() error
	Stop()

	SetOnConnected(func())
	SetOnDisconnected(func())

	GetStats() interface{}

	NewPublisher(ctx context.Context, listener McuListener, id string, streamType string) (McuPublisher, error)
	NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error)
}

type McuClient interface {
	Id() string
	StreamType() string

	Close(ctx context.Context)

	SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{}))
}

type McuPublisher interface {
	McuClient
}

type McuSubscriber interface {
	McuClient

	Publisher() string
}
