// Package domain contains the core business entities and value objects.
package domain

import (
	"fmt"
	"math"
)

// Coordinate represents a geographic coordinate with optional height.
type Coordinate struct {
	X    float64 // Longitude or Easting
	Y    float64 // Latitude or Northing
	Z    float64 // Height (optional)
	SRID int     // Spatial Reference ID
}

// NewWGS84Coordinate creates a WGS84 (EPSG:4326) coordinate.
func NewWGS84Coordinate(lon, lat float64) Coordinate {
	return Coordinate{X: lon, Y: lat, SRID: SRIDWGS84}
}

// NewCoordinate creates a coordinate with the specified SRID.
func NewCoordinate(x, y float64, srid int) Coordinate {
	return Coordinate{X: x, Y: y, SRID: srid}
}

// Validate checks if the coordinate is valid for its SRID.
func (c Coordinate) Validate() error {
	if c.SRID == SRIDWGS84 {
		if c.X < -180 || c.X > 180 {
			return &ValidationError{
				Field:      "longitude",
				Value:      c.X,
				Constraint: "[-180, 180]",
				Message:    "longitude must be between -180 and 180",
			}
		}
		if c.Y < -90 || c.Y > 90 {
			return &ValidationError{
				Field:      "latitude",
				Value:      c.Y,
				Constraint: "[-90, 90]",
				Message:    "latitude must be between -90 and 90",
			}
		}
	}
	return nil
}

// IsZero returns true if the coordinate is unset.
func (c Coordinate) IsZero() bool {
	return c.X == 0 && c.Y == 0 && c.SRID == 0
}

// String returns a string representation of the coordinate.
func (c Coordinate) String() string {
	if c.Z != 0 {
		return fmt.Sprintf("POINT Z(%f %f %f) SRID=%d", c.X, c.Y, c.Z, c.SRID)
	}
	return fmt.Sprintf("POINT(%f %f) SRID=%d", c.X, c.Y, c.SRID)
}

// WKT returns the Well-Known Text representation.
func (c Coordinate) WKT() string {
	if c.Z != 0 {
		return fmt.Sprintf("POINT Z(%f %f %f)", c.X, c.Y, c.Z)
	}
	return fmt.Sprintf("POINT(%f %f)", c.X, c.Y)
}

// Projection represents a coordinate reference system.
type Projection struct {
	SRID int    // EPSG Code
	Name string // Human-readable name
}

// Common SRID constants.
const (
	SRIDWGS84        = 4326  // WGS 84
	SRIDWebMercator  = 3857  // Web Mercator
	SRIDETRS89UTM32N = 25832 // ETRS89 / UTM zone 32N
	SRIDETRS89UTM33N = 25833 // ETRS89 / UTM zone 33N
	SRIDDHDN3GK2     = 31466 // DHDN / Gauß-Krüger zone 2
	SRIDDHDN3GK3     = 31467 // DHDN / Gauß-Krüger zone 3
)

// CommonProjections contains frequently used projections.
var CommonProjections = map[int]Projection{
	SRIDWGS84:        {SRID: SRIDWGS84, Name: "WGS 84"},
	SRIDWebMercator:  {SRID: SRIDWebMercator, Name: "Web Mercator"},
	SRIDETRS89UTM32N: {SRID: SRIDETRS89UTM32N, Name: "ETRS89 / UTM zone 32N"},
	SRIDETRS89UTM33N: {SRID: SRIDETRS89UTM33N, Name: "ETRS89 / UTM zone 33N"},
	SRIDDHDN3GK2:     {SRID: SRIDDHDN3GK2, Name: "DHDN / Gauß-Krüger zone 2"},
	SRIDDHDN3GK3:     {SRID: SRIDDHDN3GK3, Name: "DHDN / Gauß-Krüger zone 3"},
}

// IsKnownSRID returns true if the SRID is in the common projections list.
func IsKnownSRID(srid int) bool {
	_, ok := CommonProjections[srid]
	return ok
}

// Extent represents a spatial bounding box.
type Extent struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
	SRID int
}

// Contains checks if a coordinate is within the extent.
func (e Extent) Contains(c Coordinate) bool {
	return c.X >= e.MinX && c.X <= e.MaxX && c.Y >= e.MinY && c.Y <= e.MaxY
}

// IsValid checks if the extent has valid dimensions.
func (e Extent) IsValid() bool {
	return e.MinX <= e.MaxX && e.MinY <= e.MaxY
}

// Width returns the width of the extent.
func (e Extent) Width() float64 {
	return math.Abs(e.MaxX - e.MinX)
}

// Height returns the height of the extent.
func (e Extent) Height() float64 {
	return math.Abs(e.MaxY - e.MinY)
}

// Center returns the center coordinate of the extent.
func (e Extent) Center() Coordinate {
	return Coordinate{
		X:    (e.MinX + e.MaxX) / 2,
		Y:    (e.MinY + e.MaxY) / 2,
		SRID: e.SRID,
	}
}
