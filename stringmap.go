/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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

// StringMap maps string keys to arbitrary values.
type StringMap map[string]any

func ConvertStringMap(ob any) (StringMap, bool) {
	if ob == nil {
		return nil, true
	}

	if m, ok := ob.(map[string]any); ok {
		return StringMap(m), true
	}

	if m, ok := ob.(StringMap); ok {
		return m, true
	}

	return nil, false
}

// GetStringMapEntry returns an entry from a string map in a given type.
func GetStringMapEntry[T any](m StringMap, key string) (s T, ok bool) {
	var defaultValue T
	v, found := m[key]
	if !found {
		return defaultValue, false
	}

	s, ok = v.(T)
	return
}

func GetStringMapString[T ~string](m StringMap, key string) (T, bool) {
	var defaultValue T
	v, found := m[key]
	if !found {
		return defaultValue, false
	}

	switch v := v.(type) {
	case T:
		return v, true
	case string:
		return T(v), true
	default:
		return defaultValue, false
	}
}
