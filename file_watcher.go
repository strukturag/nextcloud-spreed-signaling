/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"context"
	"errors"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultDeduplicateWatchEvents = 100 * time.Millisecond
)

var (
	deduplicateWatchEvents atomic.Int64
)

func init() {
	deduplicateWatchEvents.Store(int64(defaultDeduplicateWatchEvents))
}

type FileWatcherCallback func(filename string)

type FileWatcher struct {
	filename string
	target   string
	callback FileWatcherCallback

	watcher   *fsnotify.Watcher
	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewFileWatcher(filename string, callback FileWatcherCallback) (*FileWatcher, error) {
	realFilename, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := watcher.Add(realFilename); err != nil {
		watcher.Close() // nolint
		return nil, err
	}

	if err := watcher.Add(path.Dir(filename)); err != nil {
		watcher.Close() // nolint
		return nil, err
	}

	closeCtx, closeFunc := context.WithCancel(context.Background())

	w := &FileWatcher{
		filename: filename,
		target:   realFilename,
		callback: callback,
		watcher:  watcher,

		closeCtx:  closeCtx,
		closeFunc: closeFunc,
	}
	go w.run()
	return w, nil
}

func (f *FileWatcher) Close() error {
	f.closeFunc()
	return f.watcher.Close()
}

func (f *FileWatcher) run() {
	var mu sync.Mutex
	timers := make(map[string]*time.Timer)

	triggerEvent := func(event fsnotify.Event) {
		deduplicate := time.Duration(deduplicateWatchEvents.Load())
		if deduplicate <= 0 {
			f.callback(f.filename)
			return
		}

		// Use timer to deduplicate multiple events for the same file.
		mu.Lock()
		t, found := timers[event.Name]
		mu.Unlock()
		if !found {
			t = time.AfterFunc(deduplicate, func() {
				f.callback(f.filename)

				mu.Lock()
				delete(timers, event.Name)
				mu.Unlock()
			})
			mu.Lock()
			timers[event.Name] = t
			mu.Unlock()
		} else {
			t.Reset(deduplicate)
		}
	}

	for {
		select {
		case event := <-f.watcher.Events:
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) {
				continue
			}

			if stat, err := os.Lstat(event.Name); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("Could not lstat %s: %s", event.Name, err)
				}
			} else if stat.Mode()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(event.Name)
				if err == nil && target != f.target && strings.HasSuffix(event.Name, f.filename) {
					f.target = target
					triggerEvent(event)
				}
				continue
			}

			if strings.HasSuffix(event.Name, f.filename) || strings.HasSuffix(event.Name, f.target) {
				triggerEvent(event)
			}
		case err := <-f.watcher.Errors:
			if err == nil {
				return
			}

			log.Printf("Error watching %s: %s", f.filename, err)
		case <-f.closeCtx.Done():
			return
		}
	}
}
