package domain

import (
	"testing"
	"time"
)

func TestGeoPackageIsReady(t *testing.T) {
	tests := []struct {
		name string
		pkg  GeoPackage
		want bool
	}{
		{
			name: "indexed with all indexed layers",
			pkg: GeoPackage{
				Indexed: true,
				Layers: []Layer{
					{Name: "layer1", HasIndex: true},
					{Name: "layer2", HasIndex: true},
				},
			},
			want: true,
		},
		{
			name: "indexed but layer not indexed",
			pkg: GeoPackage{
				Indexed: true,
				Layers: []Layer{
					{Name: "layer1", HasIndex: true},
					{Name: "layer2", HasIndex: false},
				},
			},
			want: false,
		},
		{
			name: "not indexed",
			pkg: GeoPackage{
				Indexed: false,
				Layers: []Layer{
					{Name: "layer1", HasIndex: true},
				},
			},
			want: false,
		},
		{
			name: "indexed with no layers",
			pkg: GeoPackage{
				Indexed: true,
				Layers:  []Layer{},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pkg.IsReady(); got != tt.want {
				t.Errorf("IsReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeoPackageLayerCount(t *testing.T) {
	tests := []struct {
		name string
		pkg  GeoPackage
		want int
	}{
		{
			name: "no layers",
			pkg:  GeoPackage{},
			want: 0,
		},
		{
			name: "three layers",
			pkg: GeoPackage{
				Layers: []Layer{{Name: "a"}, {Name: "b"}, {Name: "c"}},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pkg.LayerCount(); got != tt.want {
				t.Errorf("LayerCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGeoPackageGetLayer(t *testing.T) {
	pkg := GeoPackage{
		Layers: []Layer{
			{Name: "buildings", GeometryType: "POLYGON"},
			{Name: "roads", GeometryType: "LINESTRING"},
			{Name: "points_of_interest", GeometryType: "POINT"},
		},
	}

	tests := []struct {
		name     string
		layerReq string
		wantOK   bool
		wantType string
	}{
		{"existing layer", "buildings", true, "POLYGON"},
		{"another existing layer", "roads", true, "LINESTRING"},
		{"non-existing layer", "missing", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layer, ok := pkg.GetLayer(tt.layerReq)
			if ok != tt.wantOK {
				t.Errorf("GetLayer(%q) ok = %v, want %v", tt.layerReq, ok, tt.wantOK)
			}
			if ok && layer.GeometryType != tt.wantType {
				t.Errorf("GetLayer(%q).GeometryType = %q, want %q", tt.layerReq, layer.GeometryType, tt.wantType)
			}
		})
	}
}

func TestLayerIsPointLayer(t *testing.T) {
	tests := []struct {
		geomType string
		want     bool
	}{
		{"POINT", true},
		{"MULTIPOINT", true},
		{"POLYGON", false},
		{"LINESTRING", false},
	}

	for _, tt := range tests {
		t.Run(tt.geomType, func(t *testing.T) {
			layer := Layer{GeometryType: tt.geomType}
			if got := layer.IsPointLayer(); got != tt.want {
				t.Errorf("IsPointLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLayerIsPolygonLayer(t *testing.T) {
	tests := []struct {
		geomType string
		want     bool
	}{
		{"POLYGON", true},
		{"MULTIPOLYGON", true},
		{"POINT", false},
		{"LINESTRING", false},
	}

	for _, tt := range tests {
		t.Run(tt.geomType, func(t *testing.T) {
			layer := Layer{GeometryType: tt.geomType}
			if got := layer.IsPolygonLayer(); got != tt.want {
				t.Errorf("IsPolygonLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLayerIsLineLayer(t *testing.T) {
	tests := []struct {
		geomType string
		want     bool
	}{
		{"LINESTRING", true},
		{"MULTILINESTRING", true},
		{"POINT", false},
		{"POLYGON", false},
	}

	for _, tt := range tests {
		t.Run(tt.geomType, func(t *testing.T) {
			layer := Layer{GeometryType: tt.geomType}
			if got := layer.IsLineLayer(); got != tt.want {
				t.Errorf("IsLineLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeoPackageStatus(t *testing.T) {
	// Test all status constants are defined and unique
	statuses := []GeoPackageStatus{
		StatusLoading,
		StatusIndexing,
		StatusReady,
		StatusError,
		StatusUnloading,
	}

	seen := make(map[GeoPackageStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("Duplicate status: %s", s)
		}
		seen[s] = true
		if s == "" {
			t.Error("Status should not be empty")
		}
	}
}

func TestGeoPackageMetadata(t *testing.T) {
	now := time.Now()
	pkg := GeoPackage{
		ID:       "test-pkg",
		Name:     "Test Package",
		Path:     "/data/test.gpkg",
		Size:     1024 * 1024,
		LoadedAt: now,
		Metadata: Metadata{
			Title:       "Test Data",
			Description: "Test GeoPackage for testing",
		},
		License: License{
			Name: "CC BY 4.0",
		},
	}

	if pkg.ID != "test-pkg" {
		t.Errorf("ID = %q, want %q", pkg.ID, "test-pkg")
	}
	if pkg.Size != 1024*1024 {
		t.Errorf("Size = %d, want %d", pkg.Size, 1024*1024)
	}
	if !pkg.LoadedAt.Equal(now) {
		t.Errorf("LoadedAt = %v, want %v", pkg.LoadedAt, now)
	}
	if pkg.Metadata.Title != "Test Data" {
		t.Errorf("Metadata.Title = %q, want %q", pkg.Metadata.Title, "Test Data")
	}
	if pkg.License.Name != "CC BY 4.0" {
		t.Errorf("License.Name = %q, want %q", pkg.License.Name, "CC BY 4.0")
	}
}

func TestLayerExtent(t *testing.T) {
	layer := Layer{
		Name:           "test",
		GeometryType:   "POLYGON",
		GeometryColumn: "geom",
		SRID:           4326,
		HasIndex:       true,
		FeatureCount:   1000,
		Extent: &Extent{
			MinX: 5.0,
			MinY: 47.0,
			MaxX: 15.0,
			MaxY: 55.0,
			SRID: 4326,
		},
	}

	if layer.Extent == nil {
		t.Fatal("Extent should not be nil")
	}

	if !layer.Extent.Contains(Coordinate{X: 10, Y: 50}) {
		t.Error("Extent should contain coordinate (10, 50)")
	}

	if layer.Extent.Contains(Coordinate{X: 0, Y: 50}) {
		t.Error("Extent should not contain coordinate (0, 50)")
	}
}
