/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringOptions(t *testing.T) {
	t.Setenv("FOO", "foo")
	expected := map[string]string{
		"one": "1",
		"two": "2",
		"foo": "http://foo/1",
	}
	config := goconf.NewConfigFile()
	for k, v := range expected {
		if k == "foo" {
			config.AddOption("foo", k, "http://$(FOO)/1")
		} else {
			config.AddOption("foo", k, v)
		}
	}
	config.AddOption("default", "three", "3")

	options, err := GetStringOptions(config, "foo", false)
	require.NoError(t, err)
	assert.Equal(t, expected, options)
}

func TestStringOptionWithEnv(t *testing.T) {
	t.Setenv("FOO", "foo")
	t.Setenv("BAR", "")
	t.Setenv("BA_R", "bar")

	config := goconf.NewConfigFile()
	config.AddOption("test", "foo", "http://$(FOO)/1")
	config.AddOption("test", "bar", "http://$(BAR)/2")
	config.AddOption("test", "bar2", "http://$(BA_R)/3")
	config.AddOption("test", "baz", "http://$(BAZ)/4")
	config.AddOption("test", "inv1", "http://$(FOO")
	config.AddOption("test", "inv2", "http://$FOO)")
	config.AddOption("test", "inv3", "http://$((FOO)")
	config.AddOption("test", "inv4", "http://$(F.OO)")

	expected := map[string]string{
		"foo":  "http://foo/1",
		"bar":  "http:///2",
		"bar2": "http://bar/3",
		"baz":  "http://BAZ/4",
		"inv1": "http://$(FOO",
		"inv2": "http://$FOO)",
		"inv3": "http://$((FOO)",
		"inv4": "http://$(F.OO)",
	}
	for k, v := range expected {
		value, err := GetStringOptionWithEnv(config, "test", k)
		if assert.NoError(t, err, "expected value for %s", k) {
			assert.Equal(t, v, value, "unexpected value for %s", k)
		}
	}

}
