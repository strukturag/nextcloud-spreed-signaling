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
package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBandwidth(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var b Bandwidth
	assert.EqualValues(0, b.Bits())
	assert.EqualValues(0, b.Bytes())

	b = BandwidthFromBits(8000)
	assert.EqualValues(8000, b.Bits())
	assert.EqualValues(1000, b.Bytes())

	b = BandwidthFromBytes(1000)
	assert.EqualValues(8000, b.Bits())
	assert.EqualValues(1000, b.Bytes())

	b = BandwidthFromMegabits(2)
	assert.EqualValues(2*1024*1024, b.Bits())
	assert.EqualValues(2*1024*1024/8, b.Bytes())

	var a AtomicBandwidth
	assert.EqualValues(0, a.Load())
	a.Store(1000)
	assert.EqualValues(1000, a.Load())
	old := a.Swap(2000)
	assert.EqualValues(1000, old)
	assert.EqualValues(2000, a.Load())
}
