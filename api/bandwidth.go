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
	"sync/atomic"

	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

// Bandwidth stores a bandwidth in bits per second.
type Bandwidth uint64

// Bits returns the bandwidth in bits per second.
func (b Bandwidth) Bits() uint64 {
	return uint64(b)
}

// Bytes returns the bandwidth in bytes per second.
func (b Bandwidth) Bytes() uint64 {
	return b.Bits() / 8
}

// BandwidthFromBits creates a bandwidth from bits per second.
func BandwidthFromBits(b uint64) Bandwidth {
	return Bandwidth(b)
}

// BandwithFromBits creates a bandwidth from megabits per second.
func BandwidthFromMegabits(b uint64) Bandwidth {
	return Bandwidth(b * 1024 * 1024)
}

// BandwidthFromBytes creates a bandwidth from bytes per second.
func BandwidthFromBytes(b uint64) Bandwidth {
	return Bandwidth(b * 8)
}

// AtomicBandwidth is an atomic Bandwidth. The zero value is zero.
// AtomicBandwidth must not be copied after first use.
type AtomicBandwidth struct {
	// 64-bit members that are accessed atomically must be 64-bit aligned.
	v uint64
	_ internal.NoCopy
}

// Load atomically loads and returns the value stored in b.
func (b *AtomicBandwidth) Load() Bandwidth {
	return Bandwidth(atomic.LoadUint64(&b.v)) // +checklocksignore
}

// Store atomically stores v into b.
func (b *AtomicBandwidth) Store(v Bandwidth) {
	atomic.StoreUint64(&b.v, uint64(v)) // +checklocksignore
}

// Swap atomically stores v into b and returns the previous value.
func (b *AtomicBandwidth) Swap(v Bandwidth) Bandwidth {
	return Bandwidth(atomic.SwapUint64(&b.v, uint64(v))) // +checklocksignore
}
