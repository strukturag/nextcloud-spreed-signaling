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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

func TestReverseSessionId(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	a := []byte("12345")
	codec.reverseSessionId(a)
	assert.Equal([]byte("54321"), a)
	b := []byte("4321")
	codec.reverseSessionId(b)
	assert.Equal([]byte("1234"), b)
}

func Benchmark_EncodePrivateSessionId(b *testing.B) {
	require := require.New(b)
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	for b.Loop() {
		if _, err := codec.EncodePrivate(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_DecodePrivateSessionId(b *testing.B) {
	require := require.New(b)
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	sid, err := codec.EncodePrivate(data)
	require.NoError(err)
	for b.Loop() {
		if decoded, err := codec.DecodePrivate(sid); err != nil {
			b.Fatal(err)
		} else {
			codec.Put(decoded)
		}
	}
}

func Benchmark_EncodePublicSessionId(b *testing.B) {
	require := require.New(b)
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	for b.Loop() {
		if _, err := codec.EncodePublic(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_DecodePublicSessionId(b *testing.B) {
	require := require.New(b)
	backend := talk.NewCompatBackend(nil)
	data := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: backend.Id(),
	}
	codec, err := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	require.NoError(err)
	sid, err := codec.EncodePublic(data)
	require.NoError(err)
	for b.Loop() {
		if decoded, err := codec.DecodePublic(sid); err != nil {
			b.Fatal(err)
		} else {
			codec.Put(decoded)
		}
	}
}

func TestPublicPrivate(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	require := require.New(t)
	sd := &SessionIdData{
		Sid:       1,
		Created:   time.Now().UnixMicro(),
		BackendId: "foo",
	}

	codec, err := NewSessionIdCodec([]byte("0123456789012345"), []byte("0123456789012345"))
	require.NoError(err)
	private, err := codec.EncodePrivate(sd)
	require.NoError(err)
	public, err := codec.EncodePublic(sd)
	require.NoError(err)
	assert.NotEqual(private, public)

	if data, err := codec.DecodePublic(public); assert.NoError(err) {
		assert.Equal(sd.Sid, data.Sid)
		assert.Equal(sd.Created, data.Created)
		assert.Equal(sd.BackendId, data.BackendId)
		codec.Put(data)
	}
	if data, err := codec.DecodePrivate(private); assert.NoError(err) {
		assert.Equal(sd.Sid, data.Sid)
		assert.Equal(sd.Created, data.Created)
		assert.Equal(sd.BackendId, data.BackendId)
		codec.Put(data)
	}

	if data, err := codec.DecodePublic(api.PublicSessionId(private)); !assert.Error(err) {
		assert.Fail("should have failed", "received %+v", data)
		codec.Put(data)
	}
	if data, err := codec.DecodePrivate(api.PrivateSessionId(public)); !assert.Error(err) {
		assert.Fail("should have failed", "received %+v", data)
		codec.Put(data)
	}
}
