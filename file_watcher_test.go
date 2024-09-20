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
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testWatcherNoEventTimeout = 2 * defaultDeduplicateWatchEvents
)

func TestFileWatcher_NotExist(t *testing.T) {
	assert := assert.New(t)
	log := GetLoggerForTest(t)
	tmpdir := t.TempDir()
	if w, err := NewFileWatcher(log, path.Join(tmpdir, "test.txt"), func(filename string) {}); !assert.ErrorIs(err, os.ErrNotExist) {
		if w != nil {
			assert.NoError(w.Close())
		}
	}
}

func TestFileWatcher_File(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		log := GetLoggerForTest(t)
		tmpdir := t.TempDir()
		filename := path.Join(tmpdir, "test.txt")
		require.NoError(os.WriteFile(filename, []byte("Hello world!"), 0644))

		modified := make(chan struct{})
		w, err := NewFileWatcher(log, filename, func(filename string) {
			modified <- struct{}{}
		})
		require.NoError(err)
		defer w.Close()

		require.NoError(os.WriteFile(filename, []byte("Updated"), 0644))
		<-modified

		ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
		defer cancel()

		select {
		case <-modified:
			assert.Fail("should not have received another event")
		case <-ctxTimeout.Done():
		}

		require.NoError(os.WriteFile(filename, []byte("Updated"), 0644))
		<-modified

		ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
		defer cancel()

		select {
		case <-modified:
			assert.Fail("should not have received another event")
		case <-ctxTimeout.Done():
		}
	})
}

func TestFileWatcher_Rename(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()
	log := GetLoggerForTest(t)
	filename := path.Join(tmpdir, "test.txt")
	require.NoError(os.WriteFile(filename, []byte("Hello world!"), 0644))

	modified := make(chan struct{})
	w, err := NewFileWatcher(log, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	filename2 := path.Join(tmpdir, "test.txt.tmp")
	require.NoError(os.WriteFile(filename2, []byte("Updated"), 0644))

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}

	require.NoError(os.Rename(filename2, filename))
	<-modified

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_Symlink(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()
	log := GetLoggerForTest(t)
	sourceFilename := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename, []byte("Hello world!"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename, filename))

	modified := make(chan struct{})
	w, err := NewFileWatcher(log, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	require.NoError(os.WriteFile(sourceFilename, []byte("Updated"), 0644))
	<-modified

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_ChangeSymlinkTarget(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()
	log := GetLoggerForTest(t)
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	modified := make(chan struct{})
	w, err := NewFileWatcher(log, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	// Replace symlink by creating new one and rename it to the original target.
	require.NoError(os.Symlink(sourceFilename2, filename+".tmp"))
	require.NoError(os.Rename(filename+".tmp", filename))
	<-modified

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_OtherSymlink(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()
	log := GetLoggerForTest(t)
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	modified := make(chan struct{})
	w, err := NewFileWatcher(log, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	require.NoError(os.Symlink(sourceFilename2, filename+".tmp"))

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received event for other symlink")
	case <-ctxTimeout.Done():
	}
}

func TestFileWatcher_RenameSymlinkTarget(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()
	log := GetLoggerForTest(t)
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	filename := path.Join(tmpdir, "test.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	modified := make(chan struct{})
	w, err := NewFileWatcher(log, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	sourceFilename2 := path.Join(tmpdir, "test1.txt.tmp")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}

	require.NoError(os.Rename(sourceFilename2, sourceFilename1))
	<-modified

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}
}
