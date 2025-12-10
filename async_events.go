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
	"errors"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/nats"
)

var (
	ErrAlreadyRegistered = errors.New("already registered") // +checklocksignore: Global readonly variable.
)

const (
	DefaultAsyncChannelSize = 64
)

type AsyncChannel chan *nats.Msg

type AsyncEventListener interface {
	AsyncChannel() AsyncChannel
}

type AsyncEvents interface {
	Close(ctx context.Context) error

	RegisterBackendRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error
	UnregisterBackendRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error

	RegisterRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error
	UnregisterRoomListener(roomId string, backend *Backend, listener AsyncEventListener) error

	RegisterUserListener(userId string, backend *Backend, listener AsyncEventListener) error
	UnregisterUserListener(userId string, backend *Backend, listener AsyncEventListener) error

	RegisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncEventListener) error
	UnregisterSessionListener(sessionId PublicSessionId, backend *Backend, listener AsyncEventListener) error

	PublishBackendRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error
	PublishRoomMessage(roomId string, backend *Backend, message *AsyncMessage) error
	PublishUserMessage(userId string, backend *Backend, message *AsyncMessage) error
	PublishSessionMessage(sessionId PublicSessionId, backend *Backend, message *AsyncMessage) error
}

func NewAsyncEvents(ctx context.Context, url string) (AsyncEvents, error) {
	client, err := nats.NewClient(ctx, url)
	if err != nil {
		return nil, err
	}

	return NewAsyncEventsNats(log.LoggerFromContext(ctx), client)
}
