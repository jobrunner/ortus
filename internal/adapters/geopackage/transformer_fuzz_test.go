package geopackage

import (
	"strings"
	"testing"
)

// FuzzExtractGeometryType feeds arbitrary WKT-ish strings through the geometry
// type extractor (which runs over text derived from layer data). It must never
// panic and must return a substring of the input (no fabricated output).
func FuzzExtractGeometryType(f *testing.F) {
	for _, s := range []string{
		"", "POINT(0 0)", "POLYGON ((0 0, 1 1))", "(", ")", "()",
		"MULTIPOLYGON", "  LINESTRING (1 2)", "\x00(", "Ünïcode(x)",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, wkt string) {
		got := extractGeometryType(wkt) // must not panic
		if got != "" && !strings.Contains(wkt, got) {
			t.Errorf("extractGeometryType(%q) = %q not contained in input", wkt, got)
		}
	})
}
