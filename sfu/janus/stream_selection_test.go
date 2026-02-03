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
package janus

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

func TestStreamSelection(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)
	testcases := []api.StringMap{
		{},
		{
			"substream": 1.0,
		},
		{
			"temporal": 1.0,
		},
		{
			"substream": float32(1.0),
		},
		{
			"temporal": float32(1.0),
		},
		{
			"substream": 1,
			"temporal":  3,
		},
		{
			"substream": 1,
			"audio":     true,
			"video":     false,
		},
	}

	for idx, tc := range testcases {
		parsed, err := parseStreamSelection(tc)
		if assert.NoError(err, "failed for testcase %d: %+v", idx, tc) {
			assert.Equal(len(tc) > 0, parsed.HasValues(), "failed for testcase %d: %+v", idx, tc)
			m := make(api.StringMap)
			parsed.AddToMessage(m)
			for k, v := range tc {
				assert.EqualValues(v, m[k], "failed for key %s in testcase %d", k, idx)
			}
		}
	}

	_, err := parseStreamSelection(api.StringMap{
		"substream": "foo",
	})
	assert.ErrorContains(err, "unsupported substream value")

	_, err = parseStreamSelection(api.StringMap{
		"temporal": "foo",
	})
	assert.ErrorContains(err, "unsupported temporal value")

	_, err = parseStreamSelection(api.StringMap{
		"audio": 1,
	})
	assert.ErrorContains(err, "unsupported audio value")

	_, err = parseStreamSelection(api.StringMap{
		"video": "true",
	})
	assert.ErrorContains(err, "unsupported video value")
}
