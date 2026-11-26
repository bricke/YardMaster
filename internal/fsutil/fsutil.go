// Package fsutil holds the one file helper several packages share.
package fsutil

import (
	"log/slog"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a new temporary file next to path, syncs it and renames
// it into place, then syncs the directory, so neither a crash nor a power loss leaves half
// a file behind. Each call uses its own temporary file, so concurrent writers to the same
// path never interleave: the last rename wins. The file gets exactly perm, whatever the
// umask.
//
// An error means path was left as it was. Once the rename has happened, path holds the
// complete new data and WriteFileAtomic succeeds: if the directory sync then fails, it is
// logged, since at worst a crash brings back the complete old file. Callers such as a
// config apply can rely on that: on an error there is nothing to roll back.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	// Opened first, so a directory that can't be synced stops the write before the rename.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := os.CreateTemp(dir.Name(), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if serr := dir.Sync(); serr != nil {
		slog.Warn("file replaced, but syncing its directory failed", "path", path, "err", serr)
	}
	return nil
}
