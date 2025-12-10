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
	"iter"
	"strings"
)

// SplitEntries returns an iterator over all non-empty substrings of s separated
// by sep.
func SplitEntries(s string, sep string) iter.Seq[string] {
	return func(yield func(entry string) bool) {
		for entry := range strings.SplitSeq(s, sep) {
			if entry = strings.TrimSpace(entry); entry != "" {
				if !yield(entry) {
					return
				}
			}
		}
	}
}
