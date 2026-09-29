package store

import (
	"path/filepath"
	"testing"
)

// OpenTest opens a fresh database in a temporary folder for tests.
func OpenTest(t testing.TB) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
