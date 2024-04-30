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
	"reflect"
	"testing"

	"github.com/dlintw/goconf"
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
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expected, options) {
		t.Errorf("expected %+v, got %+v", expected, options)
	}
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
		if err != nil {
			t.Errorf("expected value for %s, got %s", k, err)
		} else if value != v {
			t.Errorf("expected value %s for %s, got %s", v, k, value)
		}
	}

}
