package geopackage

import (
	"testing"
)

func TestExtractGeometryType(t *testing.T) {
	tests := []struct {
		name string
		wkt  string
		want string
	}{
		{
			name: "POINT",
			wkt:  "POINT(10 50)",
			want: "POINT",
		},
		{
			name: "POLYGON",
			wkt:  "POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))",
			want: "POLYGON",
		},
		{
			name: "LINESTRING",
			wkt:  "LINESTRING(0 0, 1 1, 2 0)",
			want: "LINESTRING",
		},
		{
			name: "MULTIPOLYGON",
			wkt:  "MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0)))",
			want: "MULTIPOLYGON",
		},
		{
			name: "POINT Z",
			wkt:  "POINT Z(10 50 100)",
			want: "POINT Z",
		},
		{
			name: "empty string",
			wkt:  "",
			want: "",
		},
		{
			name: "no parenthesis",
			wkt:  "INVALID",
			want: "",
		},
		{
			name: "only parenthesis",
			wkt:  "(0 0)",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractGeometryType(tt.wkt); got != tt.want {
				t.Errorf("extractGeometryType(%q) = %q, want %q", tt.wkt, got, tt.want)
			}
		})
	}
}

func TestGetSpatiaLiteLibraryPaths(t *testing.T) {
	paths := getSpatiaLiteLibraryPaths()

	// Should return at least some paths
	if len(paths) == 0 {
		t.Error("getSpatiaLiteLibraryPaths() returned empty slice")
	}

	// When SPATIALITE_LIBRARY_PATH env var is set, only that path is returned
	// Otherwise, should contain generic paths
	// Either way, we should get at least one path
	if len(paths) < 1 {
		t.Error("getSpatiaLiteLibraryPaths() should return at least one path")
	}
}

func TestNewRepository(t *testing.T) {
	repo := NewRepository(Options{})

	if repo == nil {
		t.Fatal("NewRepository() returned nil")
	}

	if repo.connections == nil {
		t.Error("connections map should be initialized")
	}

	if repo.sources == nil {
		t.Error("sources map should be initialized")
	}
}
