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
	"reflect"
	"runtime"
	"sync"

	"go.uber.org/zap"
)

// DeferredExecutor will asynchronously execute functions while maintaining
// their order.
type DeferredExecutor struct {
	log       *zap.Logger
	queue     chan func()
	closed    chan struct{}
	closeOnce sync.Once
}

func NewDeferredExecutor(log *zap.Logger, queueSize int) *DeferredExecutor {
	if queueSize < 0 {
		queueSize = 0
	}
	result := &DeferredExecutor{
		log:    log,
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

func getFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

func (e *DeferredExecutor) Execute(f func()) {
	defer func() {
		if err := recover(); err != nil {
			e.log.Error("Could not defer",
				zap.String("function", getFunctionName(f)),
				zap.Any("error", err),
				zap.StackSkip("stack", 1),
			)
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
