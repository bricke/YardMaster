// Package fsutil holds the one file helper several packages share.
package fsutil

import (
	"log/slog"
	"os"
	"path/filepath"
)

// tempPrefix starts the name of every temporary file WriteFileAtomic creates. Leftovers
// from a crash can be recognised by it.
const tempPrefix = ".yardmaster-tmp-"

// WriteFileAtomic writes data to a new temporary file next to path, syncs it and renames
// it into place, then syncs the directory, so neither a crash nor a power loss leaves half
// a file behind. Each call uses its own temporary file, so concurrent writers to the same
// path never interleave: the last rename wins. The file gets exactly perm, whatever the
// umask.
//
// An error means path was left as it was, so callers such as a config apply have nothing
// to roll back. Once the rename has happened, WriteFileAtomic succeeds and path holds the
// complete new data. If the directory sync then fails, that is logged and the rename may
// not survive a crash: afterwards path may hold the old file, or be missing if it didn't
// exist before. Never a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	// Opened first, so a directory that can't be synced stops the write before the rename.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	// A fixed prefix, not one built from the target's name, so a long name can't push
	// the temporary name past the file system's limit.
	f, err := os.CreateTemp(dir.Name(), tempPrefix+"*")
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
		slog.Warn("file replaced, but syncing its directory failed: the change may not survive a crash", "path", path, "err", serr)
	}
	return nil
}
