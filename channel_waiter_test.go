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
	"testing"
)

func TestChannelWaiters(t *testing.T) {
	var waiters ChannelWaiters

	ch1 := make(chan struct{}, 1)
	id1 := waiters.Add(ch1)
	defer waiters.Remove(id1)

	ch2 := make(chan struct{}, 1)
	id2 := waiters.Add(ch2)
	defer waiters.Remove(id2)

	waiters.Wakeup()
	<-ch1
	<-ch2

	select {
	case <-ch1:
		t.Error("should have not received another event")
	case <-ch2:
		t.Error("should have not received another event")
	default:
	}

	ch3 := make(chan struct{}, 1)
	id3 := waiters.Add(ch3)
	waiters.Remove(id3)

	// Multiple wakeups work even without processing.
	waiters.Wakeup()
	waiters.Wakeup()
	waiters.Wakeup()
	<-ch1
	<-ch2
	select {
	case <-ch3:
		t.Error("should have not received another event")
	default:
	}
}
