/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2020 struktur AG
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
	"log"
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
)

// DeferredExecutor will asynchronously execute functions while maintaining
// their order.
type DeferredExecutor struct {
	queue     chan func()
	closed    chan struct{}
	closeOnce sync.Once
}

func NewDeferredExecutor(queueSize int) *DeferredExecutor {
	if queueSize < 0 {
		queueSize = 0
	}
	result := &DeferredExecutor{
		queue:  make(chan func(), queueSize),
		closed: make(chan struct{}),
	}
	go result.run()
	return result
}

func (e *DeferredExecutor) run() {
	defer close(e.closed)

	for {
		f := <-e.queue
		if f == nil {
			break
		}

		f()
	}
}

func getFunctionName(i any) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

func (e *DeferredExecutor) Execute(f func()) {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Could not defer function %v: %+v", getFunctionName(f), e)
			log.Printf("Called from %s", string(debug.Stack()))
		}
	}()

	e.queue <- f
}

func (e *DeferredExecutor) Close() {
	e.closeOnce.Do(func() {
		close(e.queue)
	})
}

func (e *DeferredExecutor) waitForStop() {
	<-e.closed
}
