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
package signaling

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCounterWriter(t *testing.T) {
	assert := assert.New(t)

	var b bytes.Buffer
	var written int
	w := &counterWriter{
		w:       &b,
		counter: &written,
	}
	if count, err := w.Write(nil); assert.NoError(err) && assert.EqualValues(0, count) {
		assert.EqualValues(0, written)
	}
	if count, err := w.Write([]byte("foo")); assert.NoError(err) && assert.EqualValues(3, count) {
		assert.EqualValues(3, written)
	}
}
