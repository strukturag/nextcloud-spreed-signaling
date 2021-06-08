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
	"strconv"
	"testing"
)

var (
	equalStrings = map[bool]string{
		true:  "equal",
		false: "not equal",
	}
)

type EqualTestData struct {
	a     map[Permission]bool
	b     map[Permission]bool
	equal bool
}

func Test_permissionsEqual(t *testing.T) {
	tests := []EqualTestData{
		{
			a:     nil,
			b:     nil,
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b:     nil,
			equal: false,
		},
		{
			a: nil,
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			equal: false,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			equal: true,
		},
		{
			a: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: true,
			},
			b: map[Permission]bool{
				PERMISSION_MAY_PUBLISH_MEDIA:  true,
				PERMISSION_MAY_PUBLISH_SCREEN: false,
			},
			equal: false,
		},
	}
	for idx, test := range tests {
		test := test
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			equal := permissionsEqual(test.a, test.b)
			if equal != test.equal {
				t.Errorf("Expected %+v to be %s to %+v but was %s", test.a, equalStrings[test.equal], test.b, equalStrings[equal])
			}
		})
	}
}
