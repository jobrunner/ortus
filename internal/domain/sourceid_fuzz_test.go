package domain

import (
	"strings"
	"testing"
)

// FuzzDeriveSourceID feeds arbitrary file paths (which arrive from storage
// listings — local dirs, S3/Azure keys, HTTP index lines) through the two
// functions that parse them. They must never panic, and a derived id must be
// non-empty for a supported file.
func FuzzDeriveSourceID(f *testing.F) {
	for _, s := range []string{
		"", ".", "..", "/", "regions.gpkg", "eu/de/regions.GPKG",
		"a.zip", "weird name (1).gpkg", "no-ext", ".gpkg",
		"deeply/nested/../path/x.gpkg", "x.gpkg\x00.txt", "ünïcödé.gpkg",
		strings.Repeat("a", 4096) + ".gpkg",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		id := DeriveSourceID(path)               // must not panic
		supported := IsSupportedSourceFile(path) // must not panic
		if supported && id == "" {
			t.Errorf("supported file %q derived an empty id", path)
		}
	})
}
