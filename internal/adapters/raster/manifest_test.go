package raster

import (
	"bytes"
	"os"
	"testing"
)

// TestEmbeddedSchemaMatchesCanonical guards against drift between the schema
// embedded in this package and the canonical one under docs/reference/ that
// the build pipeline validates against.
func TestEmbeddedSchemaMatchesCanonical(t *testing.T) {
	canonical, err := os.ReadFile("../../../docs/reference/ortus-raster.schema.json")
	if err != nil {
		t.Fatalf("read canonical schema: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(canonical), bytes.TrimSpace(schemaJSON)) {
		t.Error("embedded schema differs from docs/reference/ortus-raster.schema.json — copy the canonical schema into the package")
	}
}

func TestParseEPSG(t *testing.T) {
	cases := map[string]struct {
		want    int
		wantErr bool
	}{
		"EPSG:4326": {4326, false},
		"EPSG:3035": {3035, false},
		"WGS84":     {0, true},
		"EPSG:abc":  {0, true},
		"4326":      {0, true},
	}
	for in, exp := range cases {
		got, err := parseEPSG(in)
		if (err != nil) != exp.wantErr || got != exp.want {
			t.Errorf("parseEPSG(%q) = %d, %v; want %d, err=%v", in, got, err, exp.want, exp.wantErr)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	base := "/bundle"
	ok := []string{"a.tif", "sub/a.tif", "./a.tif"}
	for _, p := range ok {
		if _, err := safeJoin(base, p); err != nil {
			t.Errorf("safeJoin(%q) unexpected error: %v", p, err)
		}
	}
	bad := []string{"", "../escape", "../../etc/passwd", "/abs/path", "sub/../../escape"}
	for _, p := range bad {
		if _, err := safeJoin(base, p); err == nil {
			t.Errorf("safeJoin(%q) should have been rejected", p)
		}
	}
}
