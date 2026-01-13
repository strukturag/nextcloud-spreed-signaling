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
package internal

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

const (
	DefaultDeduplicateWatchEvents = 100 * time.Millisecond
)

type FileWatcherCallback func(filename string)

type FileWatcher struct {
	logger      log.Logger
	filename    string
	target      string
	callback    FileWatcherCallback
	deduplicate time.Duration

	watcher   *fsnotify.Watcher
	closeCtx  context.Context
	closeFunc context.CancelFunc
}

func NewFileWatcher(logger log.Logger, filename string, callback FileWatcherCallback, deduplicate time.Duration) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := watcher.Add(path.Dir(filename)); err != nil {
		watcher.Close() // nolint
		return nil, err
	}

	closeCtx, closeFunc := context.WithCancel(context.Background())

	w := &FileWatcher{
		logger:      logger,
		filename:    filename,
		callback:    callback,
		deduplicate: deduplicate,
		watcher:     watcher,

		closeCtx:  closeCtx,
		closeFunc: closeFunc,
	}
	if err := w.updateWatcher(); err != nil {
		watcher.Close() // nolint
		return nil, err
	}

	go w.run()
	return w, nil
}

func (f *FileWatcher) updateWatcher() error {
	realFilename, err := filepath.EvalSymlinks(f.filename)
	if err != nil {
		return err
	}

	if err := f.watcher.Add(realFilename); err != nil {
		return err
	}

	f.target = realFilename
	return nil
}

func (f *FileWatcher) Close() error {
	f.closeFunc()
	return f.watcher.Close()
}

func (f *FileWatcher) run() {
	var mu sync.Mutex
	timers := make(map[string]*time.Timer)

	triggerEvent := func(event fsnotify.Event) {
		if f.deduplicate <= 0 {
			f.callback(f.filename)
			return
		}

		filename := path.Clean(event.Name)

		// Use timer to deduplicate multiple events for the same file.
		mu.Lock()
		t, found := timers[filename]
		mu.Unlock()
		if !found {
			t = time.AfterFunc(f.deduplicate, func() {
				f.callback(f.filename)

				mu.Lock()
				delete(timers, filename)
				mu.Unlock()
			})
			mu.Lock()
			timers[filename] = t
			mu.Unlock()
		} else {
			t.Reset(f.deduplicate)
		}
	}

	for {
		select {
		case event := <-f.watcher.Events:
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
				continue
			}

			if event.Has(fsnotify.Remove) {
				// Watched target has been deleted, assume it was symlinked and try to watch new target.
				if event.Name != f.target {
					continue
				}

				triggerEvent(event)
				if err := f.updateWatcher(); err != nil {
					f.logger.Printf("Error updating watcher after %s is deleted: %s", event.Name, err)
				}
				continue
			}

			if stat, err := os.Lstat(event.Name); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					f.logger.Printf("Could not lstat %s: %s", event.Name, err)
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

			f.logger.Printf("Error watching %s: %s", f.filename, err)
		case <-f.closeCtx.Done():
			return
		}
	}
}
