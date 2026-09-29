// Package fsutil holds the one file helper several packages share.
package fsutil

import "os"

// WriteFileAtomic writes data to a temporary file next to path and renames it into place,
// so a crash never leaves half a file behind.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
