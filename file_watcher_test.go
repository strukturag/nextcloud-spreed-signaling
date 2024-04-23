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
	"os"
	"path"
	"testing"
)

var (
	testWatcherNoEventTimeout = 2 * defaultDeduplicateWatchEvents
)

func TestFileWatcher_NotExist(t *testing.T) {
	tmpdir := t.TempDir()
	w, err := NewFileWatcher(path.Join(tmpdir, "test.txt"), func(filename string) {})
	if err == nil {
		t.Error("should not be able to watch non-existing files")
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Error(err)
	}
}

func TestFileWatcher_File(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		tmpdir := t.TempDir()
		filename := path.Join(tmpdir, "test.txt")
		if err := os.WriteFile(filename, []byte("Hello world!"), 0644); err != nil {
			t.Fatal(err)
		}

		modified := make(chan struct{})
		w, err := NewFileWatcher(filename, func(filename string) {
			modified <- struct{}{}
		})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		if err := os.WriteFile(filename, []byte("Updated"), 0644); err != nil {
			t.Fatal(err)
		}
		<-modified

		ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
		defer cancel()

		select {
		case <-modified:
			t.Error("should not have received another event")
		case <-ctxTimeout.Done():
		}

		if err := os.WriteFile(filename, []byte("Updated"), 0644); err != nil {
			t.Fatal(err)
		}
		<-modified

		ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
		defer cancel()

		select {
		case <-modified:
			t.Error("should not have received another event")
		case <-ctxTimeout.Done():
		}
	})
}

func TestFileWatcher_Rename(t *testing.T) {
	tmpdir := t.TempDir()
	filename := path.Join(tmpdir, "test.txt")
	if err := os.WriteFile(filename, []byte("Hello world!"), 0644); err != nil {
		t.Fatal(err)
	}

	modified := make(chan struct{})
	w, err := NewFileWatcher(filename, func(filename string) {
		modified <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	filename2 := path.Join(tmpdir, "test.txt.tmp")
	if err := os.WriteFile(filename2, []byte("Updated"), 0644); err != nil {
		t.Fatal(err)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}

	if err := os.Rename(filename2, filename); err != nil {
		t.Fatal(err)
	}
	<-modified

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_Symlink(t *testing.T) {
	tmpdir := t.TempDir()
	sourceFilename := path.Join(tmpdir, "test1.txt")
	if err := os.WriteFile(sourceFilename, []byte("Hello world!"), 0644); err != nil {
		t.Fatal(err)
	}

	filename := path.Join(tmpdir, "symlink.txt")
	if err := os.Symlink(sourceFilename, filename); err != nil {
		t.Fatal(err)
	}

	modified := make(chan struct{})
	w, err := NewFileWatcher(filename, func(filename string) {
		modified <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(sourceFilename, []byte("Updated"), 0644); err != nil {
		t.Fatal(err)
	}
	<-modified

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_ChangeSymlinkTarget(t *testing.T) {
	tmpdir := t.TempDir()
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	if err := os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644); err != nil {
		t.Fatal(err)
	}

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	if err := os.WriteFile(sourceFilename2, []byte("Updated"), 0644); err != nil {
		t.Fatal(err)
	}

	filename := path.Join(tmpdir, "symlink.txt")
	if err := os.Symlink(sourceFilename1, filename); err != nil {
		t.Fatal(err)
	}

	modified := make(chan struct{})
	w, err := NewFileWatcher(filename, func(filename string) {
		modified <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Replace symlink by creating new one and rename it to the original target.
	if err := os.Symlink(sourceFilename2, filename+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filename+".tmp", filename); err != nil {
		t.Fatal(err)
	}
	<-modified

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_OtherSymlink(t *testing.T) {
	tmpdir := t.TempDir()
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	if err := os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644); err != nil {
		t.Fatal(err)
	}

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	if err := os.WriteFile(sourceFilename2, []byte("Updated"), 0644); err != nil {
		t.Fatal(err)
	}

	filename := path.Join(tmpdir, "symlink.txt")
	if err := os.Symlink(sourceFilename1, filename); err != nil {
		t.Fatal(err)
	}

	modified := make(chan struct{})
	w, err := NewFileWatcher(filename, func(filename string) {
		modified <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.Symlink(sourceFilename2, filename+".tmp"); err != nil {
		t.Fatal(err)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received event for other symlink")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_RenameSymlinkTarget(t *testing.T) {
	tmpdir := t.TempDir()
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	if err := os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644); err != nil {
		t.Fatal(err)
	}

	filename := path.Join(tmpdir, "test.txt")
	if err := os.Symlink(sourceFilename1, filename); err != nil {
		t.Fatal(err)
	}

	modified := make(chan struct{})
	w, err := NewFileWatcher(filename, func(filename string) {
		modified <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	sourceFilename2 := path.Join(tmpdir, "test1.txt.tmp")
	if err := os.WriteFile(sourceFilename2, []byte("Updated"), 0644); err != nil {
		t.Fatal(err)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}

	if err := os.Rename(sourceFilename2, sourceFilename1); err != nil {
		t.Fatal(err)
	}
	<-modified

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		t.Error("should not have received another event")
	case <-ctxTimeout.Done():
	}
}
