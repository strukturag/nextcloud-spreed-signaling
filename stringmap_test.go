/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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

	"github.com/stretchr/testify/assert"
)

func TestConvertStringMap(t *testing.T) {
	assert := assert.New(t)
	d := map[string]any{
		"foo": "bar",
		"bar": 2,
	}

	m, ok := ConvertStringMap(d)
	if assert.True(ok) {
		assert.EqualValues(d, m)
	}

	if m, ok := ConvertStringMap(nil); assert.True(ok) {
		assert.Nil(m)
	}

	_, ok = ConvertStringMap("foo")
	assert.False(ok)

	_, ok = ConvertStringMap(1)
	assert.False(ok)

	_, ok = ConvertStringMap(map[int]any{
		1: "foo",
	})
	assert.False(ok)
}

func TestGetStringMapString(t *testing.T) {
	assert := assert.New(t)

	type StringMapTestString string

	var ok bool
	m := StringMap{
		"foo": "bar",
		"bar": StringMapTestString("baz"),
		"baz": 1234,
	}
	if v, ok := GetStringMapString[string](m, "foo"); assert.True(ok) {
		assert.Equal("bar", v)
	}
	if v, ok := GetStringMapString[StringMapTestString](m, "foo"); assert.True(ok) {
		assert.Equal(StringMapTestString("bar"), v)
	}
	v, ok := GetStringMapString[string](m, "bar")
	assert.False(ok, "should not find object, got %+v", v)

	if v, ok := GetStringMapString[StringMapTestString](m, "bar"); assert.True(ok) {
		assert.Equal(StringMapTestString("baz"), v)
	}

	_, ok = GetStringMapString[string](m, "baz")
	assert.False(ok)
	_, ok = GetStringMapString[StringMapTestString](m, "baz")
	assert.False(ok)
	_, ok = GetStringMapString[string](m, "invalid")
	assert.False(ok)
	_, ok = GetStringMapString[StringMapTestString](m, "invalid")
	assert.False(ok)
}
