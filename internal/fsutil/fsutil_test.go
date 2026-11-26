package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A leftover temporary file from an older version must not decide the permissions.
	if err := os.WriteFile(path+".tmp", nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "second" {
		t.Fatalf("content = %q, %v; want \"second\"", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v; want 0640", info.Mode().Perm(), err)
	}
	assertOnly(t, dir, "config.toml", "config.toml.tmp")
}

func TestWriteFileAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider-keys")
	contents := make([][]byte, 8)
	for i := range contents {
		contents[i] = bytes.Repeat([]byte{byte('a' + i)}, 64<<10)
	}

	var wg sync.WaitGroup
	for _, c := range contents {
		wg.Go(func() {
			if err := WriteFileAtomic(path, c, 0o600); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contents {
		if bytes.Equal(got, c) {
			assertOnly(t, dir, "provider-keys")
			return
		}
	}
	t.Fatalf("file holds a mix of writers' data (%d bytes)", len(got))
}

func TestWriteFileAtomicFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	// The target is a directory, so the rename fails after the temporary file is written.
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(filepath.Join(dir, "target"), []byte("x"), 0o600); err == nil {
		t.Fatal("want an error writing over a directory")
	}
	assertOnly(t, dir, "target")
}

// assertOnly fails unless dir holds exactly the named entries.
func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, names) {
		t.Fatalf("dir holds %v, want %v", got, names)
	}
}
