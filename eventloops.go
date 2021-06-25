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
	"container/heap"
	"fmt"
)

type EventLoop struct {
	loop  int
	count int
	index int
}

func (e *EventLoop) String() string {
	return fmt.Sprintf("Loop %d at %d (%d streams)", e.loop, e.index, e.count)
}

type EventLoops []*EventLoop

func NewEventLoops(count int) EventLoops {
	loops := make(EventLoops, count)
	for i := 0; i < count; i++ {
		loops[i] = &EventLoop{
			loop:  i,
			index: i,
		}
	}
	heap.Init(&loops)
	return loops
}

func (l EventLoops) Len() int {
	return len(l)
}

func (l EventLoops) Less(i, j int) bool {
	if l[i].count < l[j].count {
		return true
	} else if l[i].loop < l[j].loop {
		// Consistent ordering for tests, same number of streams are sorted by loop.
		return true
	}
	return false
}

func (l EventLoops) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
	l[i].index = i
	l[j].index = j
}

func (l *EventLoops) Push(x interface{}) {
	n := len(*l)
	item := x.(*EventLoop)
	item.index = n
	*l = append(*l, item)
}

func (l *EventLoops) Pop() interface{} {
	old := *l
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*l = old[0 : n-1]
	return item
}

func (l *EventLoops) Update(loop *EventLoop, change int) {
	loop.count += change
	heap.Fix(l, loop.index)
}

func (l *EventLoops) GetLowest() *EventLoop {
	loop := (*l)[0]
	l.Update(loop, 1)
	return loop
}
