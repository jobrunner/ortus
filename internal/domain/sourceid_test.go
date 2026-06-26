package domain

import "testing"

func TestDeriveSourceID(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"normal gpkg", "test.gpkg", "test"},
		{"normal zip", "bundle.zip", "bundle"},
		{"full path", "/data/buildings.gpkg", "buildings"},
		{"object key with prefix", "gpkg/districts.gpkg", "districts"},
		{"filename with space", "/data/my source.gpkg", "my source"},
		{"no extension", "/data/plain", "plain"},
		{"extension-only basename", ".gpkg", ".gpkg"},
		{"empty", "", ""},
		{"dot", ".", ""},
		{"multi-dot", "/data/a.b.gpkg", "a.b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveSourceID(tt.path); got != tt.want {
				t.Errorf("DeriveSourceID(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsSupportedSourceFile(t *testing.T) {
	supported := []string{"x.gpkg", "X.GPKG", "bundle.zip", "/data/a.gpkg", "key.ZIP"}
	for _, n := range supported {
		if !IsSupportedSourceFile(n) {
			t.Errorf("IsSupportedSourceFile(%q) = false, want true", n)
		}
	}
	unsupported := []string{"x.tif", "readme.md", "noext", "x.gpkg.bak", ""}
	for _, n := range unsupported {
		if IsSupportedSourceFile(n) {
			t.Errorf("IsSupportedSourceFile(%q) = true, want false", n)
		}
	}
}
