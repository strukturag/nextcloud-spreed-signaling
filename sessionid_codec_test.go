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
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReverseSessionId(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	a := base64.URLEncoding.EncodeToString([]byte("12345"))
	ar, err := reverseSessionId(a)
	require.NoError(err)
	require.NotEqual(a, ar)
	b := base64.URLEncoding.EncodeToString([]byte("54321"))
	br, err := reverseSessionId(b)
	require.NoError(err)
	require.NotEqual(b, br)
	assert.Equal(b, ar)
	assert.Equal(a, br)

	// Invalid base64.
	if s, err := reverseSessionId("hello world!"); !assert.Error(err) {
		assert.Fail("should have failed", "received %s", s)
	}
	// Invalid base64 length.
	if s, err := reverseSessionId("123"); !assert.Error(err) {
		assert.Fail("should have failed", "received %s", s)
	}
}

func TestPublicPrivate(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	sd := &SessionIdData{
		Sid:       1,
		Created:   timestamppb.Now(),
		BackendId: "foo",
	}

	codec := NewSessionIdCodec([]byte("0123456789012345"), []byte("0123456789012345"))
	private, err := codec.EncodePrivate(sd)
	require.NoError(err)
	public, err := codec.EncodePublic(sd)
	require.NoError(err)
	assert.NotEqual(private, public)

	if data, err := codec.DecodePublic(PublicSessionId(private)); !assert.Error(err) {
		assert.Fail("should have failed", "received %+v", data)
	}
	if data, err := codec.DecodePrivate(PrivateSessionId(public)); !assert.Error(err) {
		assert.Fail("should have failed", "received %+v", data)
	}
}
