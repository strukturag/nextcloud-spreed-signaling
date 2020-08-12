/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2019 struktur AG
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

type TestMCU struct {
}

func NewTestMCU() (Mcu, error) {
	return &TestMCU{}, nil
}

func (m *TestMCU) Start() error {
	return nil
}

func (m *TestMCU) Stop() {
}

func (m *TestMCU) SetOnConnected(f func()) {
}

func (m *TestMCU) SetOnDisconnected(f func()) {
}

func (m *TestMCU) GetStats() interface{} {
	return nil
}

func (m *TestMCU) NewPublisher(ctx context.Context, listener McuListener, id string, streamType string, initiator McuInitiator) (McuPublisher, error) {
	return nil, fmt.Errorf("Not implemented")
}

func (m *TestMCU) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	return nil, fmt.Errorf("Not implemented")
}
