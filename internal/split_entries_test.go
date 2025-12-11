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
package internal

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitEntries(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	testcases := []struct {
		s        string
		sep      string
		expected []string
	}{
		{
			"a b",
			" ",
			[]string{"a", "b"},
		},
		{
			"a b",
			",",
			[]string{"a b"},
		},
		{
			"a   b",
			" ",
			[]string{"a", "b"},
		},
		{
			"a,b,",
			",",
			[]string{"a", "b"},
		},
		{
			"a,,b",
			",",
			[]string{"a", "b"},
		},
	}
	for idx, tc := range testcases {
		assert.Equal(tc.expected, slices.Collect(SplitEntries(tc.s, tc.sep)), "failed for testcase %d: %s", idx, tc.s)
	}
}
