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
package internal

import (
	"sync/atomic"
)

type Flags struct {
	flags atomic.Uint32
}

func (f *Flags) Add(flags uint32) bool {
	old := f.flags.Or(flags)
	return old&flags != flags
}

func (f *Flags) Remove(flags uint32) bool {
	old := f.flags.And(^flags)
	return old&flags != 0
}

func (f *Flags) Set(flags uint32) bool {
	return f.flags.Swap(flags) != flags
}

func (f *Flags) Get() uint32 {
	return f.flags.Load()
}
