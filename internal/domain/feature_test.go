package domain

import "testing"

func TestFeatureGetProperty(t *testing.T) {
	feature := Feature{
		ID:        1,
		LayerName: "test",
		Properties: map[string]interface{}{
			"name":  "test feature",
			"count": 42,
			"nil":   nil,
		},
	}

	tests := []struct {
		name    string
		key     string
		wantVal interface{}
		wantOK  bool
	}{
		{"existing string", "name", "test feature", true},
		{"existing int", "count", 42, true},
		{"existing nil", "nil", nil, true},
		{"non-existing", "missing", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := feature.GetProperty(tt.key)
			if ok != tt.wantOK {
				t.Errorf("GetProperty(%q) ok = %v, want %v", tt.key, ok, tt.wantOK)
			}
			if val != tt.wantVal {
				t.Errorf("GetProperty(%q) val = %v, want %v", tt.key, val, tt.wantVal)
			}
		})
	}
}

func TestFeatureGetPropertyNilMap(t *testing.T) {
	feature := Feature{ID: 1, LayerName: "test"}

	val, ok := feature.GetProperty("anything")
	if ok {
		t.Error("GetProperty on nil map should return false")
	}
	if val != nil {
		t.Error("GetProperty on nil map should return nil")
	}
}

func TestFeatureGetStringProperty(t *testing.T) {
	feature := Feature{
		Properties: map[string]interface{}{
			"string": "hello",
			"int":    42,
		},
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"existing string", "string", "hello"},
		{"non-string type", "int", ""},
		{"missing key", "missing", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := feature.GetStringProperty(tt.key); got != tt.want {
				t.Errorf("GetStringProperty(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestFeatureGetIntProperty(t *testing.T) {
	feature := Feature{
		Properties: map[string]interface{}{
			"int":     42,
			"int64":   int64(100),
			"float64": float64(3.14),
			"string":  "text",
		},
	}

	tests := []struct {
		name string
		key  string
		want int
	}{
		{"int", "int", 42},
		{"int64", "int64", 100},
		{"float64 truncated", "float64", 3},
		{"string", "string", 0},
		{"missing", "missing", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := feature.GetIntProperty(tt.key); got != tt.want {
				t.Errorf("GetIntProperty(%q) = %d, want %d", tt.key, got, tt.want)
			}
		})
	}
}

func TestFeatureGetFloatProperty(t *testing.T) {
	feature := Feature{
		Properties: map[string]interface{}{
			"float64": float64(3.14),
			"float32": float32(2.5),
			"int":     42,
			"int64":   int64(100),
			"string":  "text",
		},
	}

	tests := []struct {
		name string
		key  string
		want float64
	}{
		{"float64", "float64", 3.14},
		{"float32", "float32", 2.5},
		{"int", "int", 42},
		{"int64", "int64", 100},
		{"string", "string", 0},
		{"missing", "missing", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := feature.GetFloatProperty(tt.key); got != tt.want {
				t.Errorf("GetFloatProperty(%q) = %f, want %f", tt.key, got, tt.want)
			}
		})
	}
}

func TestGeometryIsPoint(t *testing.T) {
	tests := []struct {
		name string
		geom Geometry
		want bool
	}{
		{"POINT", Geometry{Type: "POINT"}, true},
		{"Point", Geometry{Type: "Point"}, true},
		{"POLYGON", Geometry{Type: "POLYGON"}, false},
		{"empty", Geometry{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.geom.IsPoint(); got != tt.want {
				t.Errorf("IsPoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeometryIsPolygon(t *testing.T) {
	tests := []struct {
		name string
		geom Geometry
		want bool
	}{
		{"POLYGON", Geometry{Type: "POLYGON"}, true},
		{"Polygon", Geometry{Type: "Polygon"}, true},
		{"MULTIPOLYGON", Geometry{Type: "MULTIPOLYGON"}, true},
		{"MultiPolygon", Geometry{Type: "MultiPolygon"}, true},
		{"POINT", Geometry{Type: "POINT"}, false},
		{"empty", Geometry{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.geom.IsPolygon(); got != tt.want {
				t.Errorf("IsPolygon() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeometryIsLine(t *testing.T) {
	tests := []struct {
		name string
		geom Geometry
		want bool
	}{
		{"LINESTRING", Geometry{Type: "LINESTRING"}, true},
		{"LineString", Geometry{Type: "LineString"}, true},
		{"MULTILINESTRING", Geometry{Type: "MULTILINESTRING"}, true},
		{"MultiLineString", Geometry{Type: "MultiLineString"}, true},
		{"POINT", Geometry{Type: "POINT"}, false},
		{"empty", Geometry{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.geom.IsLine(); got != tt.want {
				t.Errorf("IsLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
