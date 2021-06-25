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

import (
	"testing"
)

func Test_EventLoops(t *testing.T) {
	loops := NewEventLoops(2)

	l0 := loops.GetLowest()
	if l0.loop != 0 {
		t.Errorf("Expected loop 0, got %+v", l0)
	}
	if l0.count != 1 {
		t.Errorf("Expected count 1, got %+v", l0)
	}

	l1 := loops.GetLowest()
	if l1.loop != 1 {
		t.Errorf("Expected loop 1, got %+v", l1)
	}
	if l1.count != 1 {
		t.Errorf("Expected count 1, got %+v", l1)
	}

	l2 := loops.GetLowest()
	if l2.loop != 0 {
		t.Errorf("Expected loop 0, got %+v", l2)
	}
	if l2.count != 2 {
		t.Errorf("Expected count 1, got %+v", l2)
	}

	loops.Update(l2, -1)
	if l0.count != 1 {
		t.Errorf("Expected count 1, got %+v", l0)
	}

	loops.Update(l1, -1)
	l3 := loops.GetLowest()
	if l3.loop != 1 {
		t.Errorf("Expected loop 1, got %+v", l3)
	}
	if l3.count != 1 {
		t.Errorf("Expected count 1, got %+v", l3)
	}
}
