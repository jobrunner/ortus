package domain

// Feature represents a geo feature with geometry and properties.
type Feature struct {
	ID         int64                  // Feature ID (fid)
	LayerName  string                 // Associated layer name
	Geometry   Geometry               // Geometry data
	Properties map[string]interface{} // Attribute data
}

// GetProperty returns a property value by key.
func (f *Feature) GetProperty(key string) (interface{}, bool) {
	if f.Properties == nil {
		return nil, false
	}
	v, ok := f.Properties[key]
	return v, ok
}

// GetStringProperty returns a property as string.
func (f *Feature) GetStringProperty(key string) string {
	if v, ok := f.GetProperty(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetIntProperty returns a property as int.
func (f *Feature) GetIntProperty(key string) int {
	if v, ok := f.GetProperty(key); ok {
		switch i := v.(type) {
		case int:
			return i
		case int64:
			return int(i)
		case float64:
			return int(i)
		}
	}
	return 0
}

// GetFloatProperty returns a property as float64.
func (f *Feature) GetFloatProperty(key string) float64 {
	if v, ok := f.GetProperty(key); ok {
		switch i := v.(type) {
		case float64:
			return i
		case float32:
			return float64(i)
		case int:
			return float64(i)
		case int64:
			return float64(i)
		}
	}
	return 0
}

// Geometry represents a geometry object.
type Geometry struct {
	Type        string     // WKT type (Point, Polygon, etc.)
	WKT         string     // Well-Known Text representation
	WKB         []byte     // Well-Known Binary representation
	SRID        int        // Spatial Reference ID
	Coordinates Coordinate // For point geometries
}

// IsPoint returns true if the geometry is a point.
func (g *Geometry) IsPoint() bool {
	return g.Type == "POINT" || g.Type == "Point"
}

// IsPolygon returns true if the geometry is a polygon.
func (g *Geometry) IsPolygon() bool {
	return g.Type == "POLYGON" || g.Type == "Polygon" ||
		g.Type == "MULTIPOLYGON" || g.Type == "MultiPolygon"
}

// IsLine returns true if the geometry is a line.
func (g *Geometry) IsLine() bool {
	return g.Type == "LINESTRING" || g.Type == "LineString" ||
		g.Type == "MULTILINESTRING" || g.Type == "MultiLineString"
}

// GeometryType represents the type of a geometry.
type GeometryType string

// Geometry type constants.
const (
	GeomPoint              GeometryType = "POINT"
	GeomLineString         GeometryType = "LINESTRING"
	GeomPolygon            GeometryType = "POLYGON"
	GeomMultiPoint         GeometryType = "MULTIPOINT"
	GeomMultiLineString    GeometryType = "MULTILINESTRING"
	GeomMultiPolygon       GeometryType = "MULTIPOLYGON"
	GeomGeometryCollection GeometryType = "GEOMETRYCOLLECTION"
)
