package raster

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupOrphaned(t *testing.T) {
	cache := t.TempDir()
	// Two orphaned unpack dirs, one unrelated dir, and a same-prefix file.
	mustMkdir(t, filepath.Join(cache, tempDirPrefix+"foo-111"))
	mustMkdir(t, filepath.Join(cache, tempDirPrefix+"bar-222"))
	mustMkdir(t, filepath.Join(cache, "unrelated-dir"))
	if err := os.WriteFile(filepath.Join(cache, tempDirPrefix+"stray-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(cache)
	n, err := repo.CleanupOrphaned()
	if err != nil {
		t.Fatalf("CleanupOrphaned: %v", err)
	}
	if n != 2 {
		t.Errorf("removed = %d, want 2", n)
	}
	// Orphaned dirs gone; unrelated dir and the file kept (only dirs are removed).
	for _, gone := range []string{tempDirPrefix + "foo-111", tempDirPrefix + "bar-222"} {
		if _, err := os.Stat(filepath.Join(cache, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{"unrelated-dir", tempDirPrefix + "stray-file"} {
		if _, err := os.Stat(filepath.Join(cache, kept)); err != nil {
			t.Errorf("%s should have been kept: %v", kept, err)
		}
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
}
