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
package nats

import (
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestGetEncodedSubject(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	encoded := GetEncodedSubject("foo", "this is the subject")
	assert.NotContains(encoded, " ")

	encoded = GetEncodedSubject("foo", "this-is-the-subject")
	assert.NotContains(encoded, "this-is")
}

func TestDecodeToString(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	testcases := []struct {
		data     []byte
		expected string
	}{
		{
			[]byte(`""`),
			"",
		},
		{
			[]byte(`"foo"`),
			"foo",
		},
		{
			[]byte(`{"type":"foo"}`),
			`{"type":"foo"}`,
		},
		{
			[]byte(`1234`),
			"1234",
		},
	}

	for idx, tc := range testcases {
		var dest string
		if assert.NoError(Decode(&nats.Msg{
			Data: tc.data,
		}, &dest), "decoding failed for test %d (%s)", idx, string(tc.data)) {
			assert.Equal(tc.expected, dest, "failed for test %s (%s)", idx, string(tc.data))
		}
	}
}

func TestDecodeToByteSlice(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	testcases := []struct {
		data     []byte
		expected []byte
	}{
		{
			[]byte(``),
			[]byte{},
		},
		{
			[]byte(`""`),
			[]byte(`""`),
		},
		{
			[]byte(`"foo"`),
			[]byte(`"foo"`),
		},
		{
			[]byte(`{"type":"foo"}`),
			[]byte(`{"type":"foo"}`),
		},
		{
			[]byte(`1234`),
			[]byte(`1234`),
		},
	}

	for idx, tc := range testcases {
		var dest []byte
		if assert.NoError(Decode(&nats.Msg{
			Data: tc.data,
		}, &dest), "decoding failed for test %d (%s)", idx, string(tc.data)) {
			assert.Equal(tc.expected, dest, "failed for test %s (%s)", idx, string(tc.data))
		}
	}
}

func TestDecodeRegular(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	type testdata struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	}

	testcases := []struct {
		data     []byte
		expected *testdata
	}{
		{
			[]byte(`null`),
			nil,
		},
		{
			[]byte(`{"value":"bar","type":"foo"}`),
			&testdata{
				Type:  "foo",
				Value: "bar",
			},
		},
		{
			[]byte(`{"value":123,"type":"foo"}`),
			&testdata{
				Type:  "foo",
				Value: float64(123),
			},
		},
	}

	for idx, tc := range testcases {
		var dest *testdata
		if assert.NoError(Decode(&nats.Msg{
			Data: tc.data,
		}, &dest), "decoding failed for test %d (%s)", idx, string(tc.data)) {
			assert.Equal(tc.expected, dest, "failed for test %s (%s)", idx, string(tc.data))
		}
	}
}
