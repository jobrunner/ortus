package domain

import "time"

// SourceKind identifies the kind of spatial data source backing a Source.
type SourceKind string

// Source kind constants.
const (
	// SourceKindVector is a vector source (GeoPackage served by SpatiaLite).
	SourceKindVector SourceKind = "vector"
	// SourceKindRaster is a raster source (GeoTIFF/COG bundle).
	SourceKindRaster SourceKind = "raster"
)

// Source represents a registered spatial data source — a GeoPackage (vector)
// or a raster bundle (raster). It is the common currency of the registry and
// query service; the Kind field discriminates the backing adapter.
type Source struct {
	ID          string     // Unique identifier (derived from filename / bundle id)
	Name        string     // Display name
	Path        string     // File path
	Kind        SourceKind // Backing source kind (vector or raster)
	Size        int64      // File size in bytes
	Layers      []Layer    // Feature layers
	Metadata    Metadata   // Source metadata
	License     License    // License information
	Indexed     bool       // Are all spatial indices created / is the source prepared?
	LoadedAt    time.Time  // Load timestamp
	LastQueried time.Time  // Last query timestamp
}

// IsReady returns true if the source is fully indexed/prepared and ready for queries.
func (s *Source) IsReady() bool {
	if !s.Indexed {
		return false
	}
	for _, layer := range s.Layers {
		if !layer.HasIndex {
			return false
		}
	}
	return true
}

// LayerCount returns the number of feature layers.
func (s *Source) LayerCount() int {
	return len(s.Layers)
}

// GetLayer returns a layer by name.
func (s *Source) GetLayer(name string) (*Layer, bool) {
	for i := range s.Layers {
		if s.Layers[i].Name == name {
			return &s.Layers[i], true
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
	return l.GeometryType == string(GeomPoint) || l.GeometryType == string(GeomMultiPoint)
}

// IsPolygonLayer returns true if the layer contains polygon geometries.
func (l *Layer) IsPolygonLayer() bool {
	return l.GeometryType == string(GeomPolygon) || l.GeometryType == string(GeomMultiPolygon)
}

// IsLineLayer returns true if the layer contains line geometries.
func (l *Layer) IsLineLayer() bool {
	return l.GeometryType == string(GeomLineString) || l.GeometryType == string(GeomMultiLineString)
}

// SourceStatus represents the lifecycle status of a Source.
type SourceStatus string

// Source status constants.
const (
	StatusLoading   SourceStatus = "loading"
	StatusIndexing  SourceStatus = "indexing"
	StatusReady     SourceStatus = "ready"
	StatusError     SourceStatus = "error"
	StatusUnloading SourceStatus = "unloading"
)
