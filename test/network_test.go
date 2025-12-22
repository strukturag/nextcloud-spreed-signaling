/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
package test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsErrorAddressAlreadyInUse(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	assert := assert.New(t)

	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(err)

	listener2, err := net.Listen("tcp", listener1.Addr().String())
	assert.Nil(listener2)
	assert.True(IsErrorAddressAlreadyInUse(err), "expected address already in use, got %+v", err)
}
