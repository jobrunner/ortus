package domain

import (
	"testing"
)

func TestNewWGS84Coordinate(t *testing.T) {
	c := NewWGS84Coordinate(9.9, 52.5)

	if c.X != 9.9 {
		t.Errorf("expected X=9.9, got %f", c.X)
	}
	if c.Y != 52.5 {
		t.Errorf("expected Y=52.5, got %f", c.Y)
	}
	if c.SRID != SRIDWGS84 {
		t.Errorf("expected SRID=%d, got %d", SRIDWGS84, c.SRID)
	}
}

func TestNewCoordinate(t *testing.T) {
	c := NewCoordinate(500000, 5700000, SRIDETRS89UTM32N)

	if c.X != 500000 {
		t.Errorf("expected X=500000, got %f", c.X)
	}
	if c.Y != 5700000 {
		t.Errorf("expected Y=5700000, got %f", c.Y)
	}
	if c.SRID != SRIDETRS89UTM32N {
		t.Errorf("expected SRID=%d, got %d", SRIDETRS89UTM32N, c.SRID)
	}
}

func TestCoordinateValidate(t *testing.T) {
	tests := []struct {
		name    string
		coord   Coordinate
		wantErr bool
	}{
		{
			name:    "valid WGS84 coordinate",
			coord:   NewWGS84Coordinate(9.9, 52.5),
			wantErr: false,
		},
		{
			name:    "valid WGS84 at origin",
			coord:   NewWGS84Coordinate(0, 0),
			wantErr: false,
		},
		{
			name:    "valid WGS84 at max bounds",
			coord:   NewWGS84Coordinate(180, 90),
			wantErr: false,
		},
		{
			name:    "valid WGS84 at min bounds",
			coord:   NewWGS84Coordinate(-180, -90),
			wantErr: false,
		},
		{
			name:    "invalid longitude too high",
			coord:   NewWGS84Coordinate(181, 52.5),
			wantErr: true,
		},
		{
			name:    "invalid longitude too low",
			coord:   NewWGS84Coordinate(-181, 52.5),
			wantErr: true,
		},
		{
			name:    "invalid latitude too high",
			coord:   NewWGS84Coordinate(9.9, 91),
			wantErr: true,
		},
		{
			name:    "invalid latitude too low",
			coord:   NewWGS84Coordinate(9.9, -91),
			wantErr: true,
		},
		{
			name:    "non-WGS84 coordinate is always valid",
			coord:   NewCoordinate(500000, 5700000, SRIDETRS89UTM32N),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.coord.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCoordinateIsZero(t *testing.T) {
	tests := []struct {
		name  string
		coord Coordinate
		want  bool
	}{
		{
			name:  "zero coordinate",
			coord: Coordinate{},
			want:  true,
		},
		{
			name:  "non-zero X",
			coord: Coordinate{X: 1},
			want:  false,
		},
		{
			name:  "non-zero Y",
			coord: Coordinate{Y: 1},
			want:  false,
		},
		{
			name:  "non-zero SRID",
			coord: Coordinate{SRID: 4326},
			want:  false,
		},
		{
			name:  "non-zero Z does not affect IsZero",
			coord: Coordinate{Z: 100},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.coord.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoordinateString(t *testing.T) {
	tests := []struct {
		name  string
		coord Coordinate
		want  string
	}{
		{
			name:  "2D coordinate",
			coord: NewWGS84Coordinate(9.9, 52.5),
			want:  "POINT(9.900000 52.500000) SRID=4326",
		},
		{
			name:  "3D coordinate",
			coord: Coordinate{X: 9.9, Y: 52.5, Z: 100.5, SRID: SRIDWGS84},
			want:  "POINT Z(9.900000 52.500000 100.500000) SRID=4326",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.coord.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCoordinateWKT(t *testing.T) {
	tests := []struct {
		name  string
		coord Coordinate
		want  string
	}{
		{
			name:  "2D coordinate",
			coord: NewWGS84Coordinate(9.9, 52.5),
			want:  "POINT(9.900000 52.500000)",
		},
		{
			name:  "3D coordinate",
			coord: Coordinate{X: 9.9, Y: 52.5, Z: 100.5, SRID: SRIDWGS84},
			want:  "POINT Z(9.900000 52.500000 100.500000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.coord.WKT(); got != tt.want {
				t.Errorf("WKT() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsKnownSRID(t *testing.T) {
	tests := []struct {
		name string
		srid int
		want bool
	}{
		{"WGS84", SRIDWGS84, true},
		{"WebMercator", SRIDWebMercator, true},
		{"ETRS89 UTM32N", SRIDETRS89UTM32N, true},
		{"unknown SRID", 99999, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKnownSRID(tt.srid); got != tt.want {
				t.Errorf("IsKnownSRID(%d) = %v, want %v", tt.srid, got, tt.want)
			}
		})
	}
}

func TestExtentContains(t *testing.T) {
	extent := Extent{
		MinX: 0,
		MinY: 0,
		MaxX: 100,
		MaxY: 100,
		SRID: SRIDWGS84,
	}

	tests := []struct {
		name  string
		coord Coordinate
		want  bool
	}{
		{
			name:  "inside",
			coord: Coordinate{X: 50, Y: 50},
			want:  true,
		},
		{
			name:  "on min corner",
			coord: Coordinate{X: 0, Y: 0},
			want:  true,
		},
		{
			name:  "on max corner",
			coord: Coordinate{X: 100, Y: 100},
			want:  true,
		},
		{
			name:  "outside X",
			coord: Coordinate{X: 101, Y: 50},
			want:  false,
		},
		{
			name:  "outside Y",
			coord: Coordinate{X: 50, Y: 101},
			want:  false,
		},
		{
			name:  "outside both",
			coord: Coordinate{X: -1, Y: -1},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extent.Contains(tt.coord); got != tt.want {
				t.Errorf("Contains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtentIsValid(t *testing.T) {
	tests := []struct {
		name   string
		extent Extent
		want   bool
	}{
		{
			name:   "valid extent",
			extent: Extent{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100},
			want:   true,
		},
		{
			name:   "zero extent",
			extent: Extent{MinX: 50, MinY: 50, MaxX: 50, MaxY: 50},
			want:   true,
		},
		{
			name:   "invalid X",
			extent: Extent{MinX: 100, MinY: 0, MaxX: 0, MaxY: 100},
			want:   false,
		},
		{
			name:   "invalid Y",
			extent: Extent{MinX: 0, MinY: 100, MaxX: 100, MaxY: 0},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.extent.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtentDimensions(t *testing.T) {
	extent := Extent{MinX: 10, MinY: 20, MaxX: 50, MaxY: 80}

	if got := extent.Width(); got != 40 {
		t.Errorf("Width() = %f, want 40", got)
	}

	if got := extent.Height(); got != 60 {
		t.Errorf("Height() = %f, want 60", got)
	}
}

func TestExtentCenter(t *testing.T) {
	extent := Extent{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100, SRID: SRIDWGS84}
	center := extent.Center()

	if center.X != 50 {
		t.Errorf("Center().X = %f, want 50", center.X)
	}
	if center.Y != 50 {
		t.Errorf("Center().Y = %f, want 50", center.Y)
	}
	if center.SRID != SRIDWGS84 {
		t.Errorf("Center().SRID = %d, want %d", center.SRID, SRIDWGS84)
	}
}
