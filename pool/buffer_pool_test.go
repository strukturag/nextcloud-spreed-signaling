/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package pool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPool(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var pool BufferPool

	buf1 := pool.Get()
	assert.NotNil(buf1)
	buf2 := pool.Get()
	assert.NotSame(buf1, buf2)

	buf1.WriteString("empty string")
	pool.Put(buf1)

	buf3 := pool.Get()
	assert.Equal(0, buf3.Len())

	pool.Put(nil)
}

func TestBufferPoolReadAll(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	s := "Hello world!"
	data := bytes.NewBufferString(s)

	var pool BufferPool

	buf1 := pool.Get()
	assert.NotNil(buf1)
	pool.Put(buf1)

	buf2, err := pool.ReadAll(data)
	require.NoError(err)
	assert.Equal(s, buf2.String())
}

var errTest = errors.New("test error")

type errorReader struct{}

func (e errorReader) Read(b []byte) (int, error) {
	return 0, errTest
}

func TestBufferPoolReadAllError(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var pool BufferPool

	buf1 := pool.Get()
	assert.NotNil(buf1)
	pool.Put(buf1)

	r := &errorReader{}
	buf2, err := pool.ReadAll(r)
	assert.ErrorIs(err, errTest)
	assert.Nil(buf2)
}

func TestBufferPoolMarshalAsJSON(t *testing.T) {
	t.Parallel()

	require := require.New(t)
	assert := assert.New(t)

	var pool BufferPool
	buf1 := pool.Get()
	assert.NotNil(buf1)
	pool.Put(buf1)

	s := "Hello world!"
	buf2, err := pool.MarshalAsJSON(s)
	require.NoError(err)

	assert.Equal(fmt.Sprintf("\"%s\"\n", s), buf2.String())
}

type errorMarshaler struct {
	json.Marshaler
}

func (e errorMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errTest
}

func TestBufferPoolMarshalAsJSONError(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var pool BufferPool
	buf1 := pool.Get()
	assert.NotNil(buf1)
	pool.Put(buf1)

	var ob errorMarshaler
	buf2, err := pool.MarshalAsJSON(ob)
	assert.ErrorIs(err, errTest)
	assert.Nil(buf2)
}
