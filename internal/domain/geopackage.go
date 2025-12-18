package domain

import "time"

// GeoPackage represents a registered GeoPackage file.
type GeoPackage struct {
	ID          string    // Unique identifier (derived from filename)
	Name        string    // Display name
	Path        string    // File path
	Size        int64     // File size in bytes
	Layers      []Layer   // Feature layers
	Metadata    Metadata  // Package metadata
	License     License   // License information
	Indexed     bool      // Are all spatial indices created?
	LoadedAt    time.Time // Load timestamp
	LastQueried time.Time // Last query timestamp
}

// IsReady returns true if the GeoPackage is fully indexed and ready for queries.
func (g *GeoPackage) IsReady() bool {
	if !g.Indexed {
		return false
	}
	for _, layer := range g.Layers {
		if !layer.HasIndex {
			return false
		}
	}
	return true
}

// LayerCount returns the number of feature layers.
func (g *GeoPackage) LayerCount() int {
	return len(g.Layers)
}

// GetLayer returns a layer by name.
func (g *GeoPackage) GetLayer(name string) (*Layer, bool) {
	for i := range g.Layers {
		if g.Layers[i].Name == name {
			return &g.Layers[i], true
		}
	}
	return nil, false
}

// Layer represents a feature layer within a GeoPackage.
type Layer struct {
	Name           string  // Layer name from gpkg_contents.table_name
	Description    string  // Layer description
	GeometryColumn string  // Name of the geometry column
	GeometryType   string  // Geometry type (POINT, POLYGON, etc.)
	SRID           int     // Spatial Reference ID
	HasIndex       bool    // Has spatial index?
	FeatureCount   int64   // Number of features
	Extent         *Extent // Bounding box (optional)
}

// IsPointLayer returns true if the layer contains point geometries.
func (l *Layer) IsPointLayer() bool {
	return l.GeometryType == "POINT" || l.GeometryType == "MULTIPOINT"
}

// IsPolygonLayer returns true if the layer contains polygon geometries.
func (l *Layer) IsPolygonLayer() bool {
	return l.GeometryType == "POLYGON" || l.GeometryType == "MULTIPOLYGON"
}

// IsLineLayer returns true if the layer contains line geometries.
func (l *Layer) IsLineLayer() bool {
	return l.GeometryType == "LINESTRING" || l.GeometryType == "MULTILINESTRING"
}

// GeoPackageStatus represents the status of a GeoPackage.
type GeoPackageStatus string

const (
	StatusLoading   GeoPackageStatus = "loading"
	StatusIndexing  GeoPackageStatus = "indexing"
	StatusReady     GeoPackageStatus = "ready"
	StatusError     GeoPackageStatus = "error"
	StatusUnloading GeoPackageStatus = "unloading"
)
