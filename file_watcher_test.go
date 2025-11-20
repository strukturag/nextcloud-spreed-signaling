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
	tmpdir := t.TempDir()
	logger := NewLoggerForTest(t)
	if w, err := NewFileWatcher(logger, path.Join(tmpdir, "test.txt"), func(filename string) {}); !assert.ErrorIs(err, os.ErrNotExist) {
		if w != nil {
			assert.NoError(w.Close())
		}
	}
}

func TestFileWatcher_File(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		tmpdir := t.TempDir()
		filename := path.Join(tmpdir, "test.txt")
		require.NoError(os.WriteFile(filename, []byte("Hello world!"), 0644))

		logger := NewLoggerForTest(t)
		modified := make(chan struct{})
		w, err := NewFileWatcher(logger, filename, func(filename string) {
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

func TestFileWatcher_CurrentDir(t *testing.T) {
	ensureNoGoroutinesLeak(t, func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)
		tmpdir := t.TempDir()
		require.NoError(os.Chdir(tmpdir))
		filename := path.Join(tmpdir, "test.txt")
		require.NoError(os.WriteFile(filename, []byte("Hello world!"), 0644))

		logger := NewLoggerForTest(t)
		modified := make(chan struct{})
		w, err := NewFileWatcher(logger, "./"+path.Base(filename), func(filename string) {
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
	filename := path.Join(tmpdir, "test.txt")
	require.NoError(os.WriteFile(filename, []byte("Hello world!"), 0644))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
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
	sourceFilename := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename, []byte("Hello world!"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename, filename))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
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
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
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
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	sourceFilename2 := path.Join(tmpdir, "test2.txt")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	filename := path.Join(tmpdir, "symlink.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
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
	sourceFilename1 := path.Join(tmpdir, "test1.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	filename := path.Join(tmpdir, "test.txt")
	require.NoError(os.Symlink(sourceFilename1, filename))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
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

func TestFileWatcher_UpdateSymlinkFolder(t *testing.T) {
	// This mimics what k8s is doing with configmaps / secrets.
	require := require.New(t)
	assert := assert.New(t)
	tmpdir := t.TempDir()

	// File is in a versioned folder.
	version1Path := path.Join(tmpdir, "version1")
	require.NoError(os.Mkdir(version1Path, 0755))
	sourceFilename1 := path.Join(version1Path, "test.txt")
	require.NoError(os.WriteFile(sourceFilename1, []byte("Hello world!"), 0644))

	// Versioned folder is symlinked to a generic "data" folder.
	dataPath := path.Join(tmpdir, "data")
	require.NoError(os.Symlink("version1", dataPath))

	// File in root is symlinked from generic "data" folder.
	filename := path.Join(tmpdir, "test.txt")
	require.NoError(os.Symlink("data/test.txt", filename))

	logger := NewLoggerForTest(t)
	modified := make(chan struct{})
	w, err := NewFileWatcher(logger, filename, func(filename string) {
		modified <- struct{}{}
	})
	require.NoError(err)
	defer w.Close()

	// New file is created in a new versioned subfolder.
	version2Path := path.Join(tmpdir, "version2")
	require.NoError(os.Mkdir(version2Path, 0755))

	sourceFilename2 := path.Join(version2Path, "test.txt")
	require.NoError(os.WriteFile(sourceFilename2, []byte("Updated"), 0644))

	ctxTimeout, cancel := context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}

	// Create temporary symlink to new versioned subfolder...
	require.NoError(os.Symlink("version2", dataPath+".tmp"))
	// ...atomically update generic "data" symlink...
	require.NoError(os.Rename(dataPath+".tmp", dataPath))
	// ...and old versioned subfolder is removed (this will trigger the event).
	require.NoError(os.RemoveAll(version1Path))

	<-modified

	// Another new file is created in a new versioned subfolder.
	version3Path := path.Join(tmpdir, "version3")
	require.NoError(os.Mkdir(version3Path, 0755))

	sourceFilename3 := path.Join(version3Path, "test.txt")
	require.NoError(os.WriteFile(sourceFilename3, []byte("Updated again"), 0644))

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}

	// Create temporary symlink to new versioned subfolder...
	require.NoError(os.Symlink("version3", dataPath+".tmp"))
	// ...atomically update generic "data" symlink...
	require.NoError(os.Rename(dataPath+".tmp", dataPath))
	// ...and old versioned subfolder is removed (this will trigger the event).
	require.NoError(os.RemoveAll(version2Path))

	<-modified

	ctxTimeout, cancel = context.WithTimeout(context.Background(), testWatcherNoEventTimeout)
	defer cancel()

	select {
	case <-modified:
		assert.Fail("should not have received another event")
	case <-ctxTimeout.Done():
	}
}
