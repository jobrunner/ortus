package raster

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// persistentRepo builds a Repository in persistent (content-addressed cache) mode
// rooted at cacheDir.
func persistentRepo(cacheDir string) *Repository {
	r := NewRepository(cacheDir)
	r.SetPersistent(true)
	return r
}

// cacheDirs returns the "regions@<fp>" extraction dirs under cacheDir (the test
// bundles all use id "regions").
func cacheDirs(t *testing.T, cacheDir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(cacheDir, "regions@*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPersistentReuseSkipsReextract: a second start against the same cache volume
// reuses the extraction instead of unpacking again — and Close keeps the dir.
func TestPersistentReuseSkipsReextract(t *testing.T) {
	ctx := context.Background()
	cache := t.TempDir()
	zipPath := buildBundle(t, t.TempDir(), "regions", validManifest)

	r1 := persistentRepo(cache)
	if _, err := r1.Open(ctx, zipPath); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	dirs := cacheDirs(t, cache)
	if len(dirs) != 1 {
		t.Fatalf("want exactly 1 cache dir, got %v", dirs)
	}
	dir := dirs[0]
	if !isComplete(dir) {
		t.Fatalf("cache dir %q missing completion marker", dir)
	}
	// Drop a sentinel; a genuine reuse leaves it untouched, a re-extract would not
	// produce it (the freshly-renamed dir wouldn't contain it).
	sentinel := filepath.Join(dir, "SENTINEL")
	if err := os.WriteFile(sentinel, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Close must KEEP the dir in persistent mode (it's the shared cache).
	if err := r1.Close(ctx, "regions"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("persistent Close must keep the cache dir, got %v", err)
	}

	// Fresh repo, same cache volume = a container restart.
	r2 := persistentRepo(cache)
	if _, err := r2.Open(ctx, zipPath); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if got := cacheDirs(t, cache); len(got) != 1 || got[0] != dir {
		t.Fatalf("reuse should keep exactly the same dir; before=%q after=%v", dir, got)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel gone → the bundle was re-extracted instead of reused: %v", err)
	}
}

// TestPersistentReextractsOnContentChange: a changed ZIP (different content) gets a
// new fingerprint dir; the old one is left in place (no prune by default).
func TestPersistentReextractsOnContentChange(t *testing.T) {
	ctx := context.Background()
	cache := t.TempDir()

	zip1 := buildBundle(t, t.TempDir(), "regions", validManifest)
	if _, err := persistentRepo(cache).Open(ctx, zip1); err != nil {
		t.Fatalf("open v1: %v", err)
	}

	// Same id/filename, changed manifest bytes → different central-dir CRC → new fp.
	v2 := validManifest + "\ndescription: changed\n"
	zip2 := buildBundle(t, t.TempDir(), "regions", v2)
	if _, err := persistentRepo(cache).Open(ctx, zip2); err != nil {
		t.Fatalf("open v2: %v", err)
	}

	if dirs := cacheDirs(t, cache); len(dirs) != 2 {
		t.Fatalf("content change must yield a new cache dir (2 total), got %v", dirs)
	}
}

// TestPersistentCleanupOrphaned: startup cleanup removes only partial .tmp-*
// extractions, never the completed <id>@<fp> caches (and ignores the ephemeral
// ortus-raster-* dirs, which belong to the other mode).
func TestPersistentCleanupOrphaned(t *testing.T) {
	cache := t.TempDir()
	partial := filepath.Join(cache, persistentTempPrefix+"regions-abc")
	kept := filepath.Join(cache, "regions@deadbeef")
	ephemeral := filepath.Join(cache, tempDirPrefix+"regions-xyz")
	mustMkdir(t, partial)
	mustMkdir(t, kept)
	mustMkdir(t, ephemeral)

	n, err := persistentRepo(cache).CleanupOrphaned()
	if err != nil {
		t.Fatalf("CleanupOrphaned: %v", err)
	}
	if n != 1 {
		t.Errorf("removed = %d, want 1 (only the partial .tmp dir)", n)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("partial extraction should be removed")
	}
	for _, keep := range []string{kept, ephemeral} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%q should be kept in persistent cleanup: %v", keep, err)
		}
	}
}

// TestPersistentPrune: with prune enabled, loading a new fingerprint removes the
// old fingerprint dir of the same source.
func TestPersistentPrune(t *testing.T) {
	ctx := context.Background()
	cache := t.TempDir()

	r1 := persistentRepo(cache)
	r1.SetPrune(true)
	if _, err := r1.Open(ctx, buildBundle(t, t.TempDir(), "regions", validManifest)); err != nil {
		t.Fatalf("open v1: %v", err)
	}

	r2 := persistentRepo(cache)
	r2.SetPrune(true)
	if _, err := r2.Open(ctx, buildBundle(t, t.TempDir(), "regions", validManifest+"\ndescription: v2\n")); err != nil {
		t.Fatalf("open v2: %v", err)
	}
	if dirs := cacheDirs(t, cache); len(dirs) != 1 {
		t.Fatalf("prune should leave exactly the active fingerprint dir, got %v", dirs)
	}
}

// TestZipFingerprintStableAndContentSensitive: the fingerprint is independent of
// entry order but changes when any entry's content changes.
func TestZipFingerprintStableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.zip")
	b := filepath.Join(dir, "b.zip")
	c := filepath.Join(dir, "c.zip")
	// a and b: same entries, opposite write order → same fingerprint.
	writeZip(t, a, [][2]string{{"m.yaml", "id: x"}, {"t.tif", "TILE"}})
	writeZip(t, b, [][2]string{{"t.tif", "TILE"}, {"m.yaml", "id: x"}})
	// c: one entry's content differs → different fingerprint.
	writeZip(t, c, [][2]string{{"m.yaml", "id: x"}, {"t.tif", "TILE-CHANGED"}})

	fpA, err := zipFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := zipFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	fpC, err := zipFingerprint(c)
	if err != nil {
		t.Fatal(err)
	}
	if fpA != fpB {
		t.Errorf("fingerprint must ignore entry order: %s != %s", fpA, fpB)
	}
	if fpA == fpC {
		t.Errorf("fingerprint must change when a tile changes: %s == %s", fpA, fpC)
	}
}

// TestPruneGlobSafe: a sourceID with glob metacharacters must not cause prune to
// match+delete the wrong directories (pruneOldVersions uses exact prefix matching,
// not filepath.Glob).
func TestPruneGlobSafe(t *testing.T) {
	cache := t.TempDir()
	r := persistentRepo(cache)
	// "weird[x]" contains a glob character class; only "weird[x]@old" should go.
	mustMkdir(t, filepath.Join(cache, "weird[x]@old"))
	mustMkdir(t, filepath.Join(cache, "weird[x]@active"))
	mustMkdir(t, filepath.Join(cache, "weirdx@keep")) // would be matched by the glob [x], must be kept
	mustMkdir(t, filepath.Join(cache, "other@keep"))

	r.pruneOldVersions("weird[x]", "active")

	gone := filepath.Join(cache, "weird[x]@old")
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("stale extraction %q should have been pruned", gone)
	}
	for _, keep := range []string{"weird[x]@active", "weirdx@keep", "other@keep"} {
		if _, err := os.Stat(filepath.Join(cache, keep)); err != nil {
			t.Errorf("%q must be kept: %v", keep, err)
		}
	}
}

func writeZip(t *testing.T, path string, entries [][2]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, werr := zw.Create(e[0])
		if werr != nil {
			t.Fatal(werr)
		}
		if _, werr := w.Write([]byte(e[1])); werr != nil {
			t.Fatal(werr)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}
