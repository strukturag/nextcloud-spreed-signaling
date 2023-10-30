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
	"sync/atomic"
)

type Flags struct {
	flags atomic.Uint32
}

func (f *Flags) Add(flags uint32) bool {
	for {
		old := f.flags.Load()
		if old&flags == flags {
			// Flags already set.
			return false
		}
		newFlags := old | flags
		if f.flags.CompareAndSwap(old, newFlags) {
			return true
		}
		// Another thread updated the flags while we were checking, retry.
	}
}

func (f *Flags) Remove(flags uint32) bool {
	for {
		old := f.flags.Load()
		if old&flags == 0 {
			// Flags not set.
			return false
		}
		newFlags := old & ^flags
		if f.flags.CompareAndSwap(old, newFlags) {
			return true
		}
		// Another thread updated the flags while we were checking, retry.
	}
}

func (f *Flags) Set(flags uint32) bool {
	for {
		old := f.flags.Load()
		if old == flags {
			return false
		}

		if f.flags.CompareAndSwap(old, flags) {
			return true
		}
	}
}

func (f *Flags) Get() uint32 {
	return f.flags.Load()
}
