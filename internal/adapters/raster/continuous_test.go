package raster

import (
	"math"
	"testing"

	"github.com/tingold/gocog"
)

// bit-pattern helpers force runtime conversions so signed negatives can be
// packed into an unsigned raw sample without constant-overflow at compile time.
func u8(v int8) uint64   { return uint64(uint8(v)) }
func u16(v int16) uint64 { return uint64(uint16(v)) }
func u32(v int32) uint64 { return uint64(uint32(v)) }

// TestSampleToFloat guards the continuous-value decode: integer bands come back
// as their true value, IEEE float bands as bit patterns that must be
// reinterpreted, and unsupported types report ok=false. A regression here would
// silently corrupt elevation values.
func TestSampleToFloat(t *testing.T) {
	cases := []struct {
		name string
		dt   gocog.DataType
		raw  uint64
		want float64
		ok   bool
	}{
		{"byte", gocog.DTByte, 200, 200, true},
		{"sbyte-negative", gocog.DTSByte, u8(-5), -5, true},
		{"uint16", gocog.DTSShort, 40000, 40000, true},
		{"int16-negative", gocog.DTSShortS, u16(-412), -412, true},
		{"int16-elevation", gocog.DTSShortS, u16(928), 928, true},
		{"uint32", gocog.DTSLong, 3000000000, 3000000000, true},
		{"int32-negative", gocog.DTSLongS, u32(-100000), -100000, true},
		{"float32", gocog.DTFloat, uint64(math.Float32bits(312.5)), 312.5, true},
		{"float64", gocog.DTDouble, math.Float64bits(-6.25), -6.25, true},
		{"ascii-unsupported", gocog.DTASCII, 65, 0, false},
		{"rational-unsupported", gocog.DTRational, 1, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sampleToFloat(c.dt, c.raw)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestIsNumericDataType ensures categorical-only/rational types are rejected for
// continuous layers while all integer and float types are accepted.
func TestIsNumericDataType(t *testing.T) {
	numeric := []gocog.DataType{
		gocog.DTByte, gocog.DTSByte, gocog.DTSShort, gocog.DTSShortS,
		gocog.DTSLong, gocog.DTSLongS, gocog.DTFloat, gocog.DTDouble,
	}
	for _, dt := range numeric {
		if !isNumericDataType(dt) {
			t.Errorf("data type %d should be numeric", dt)
		}
	}
	nonNumeric := []gocog.DataType{gocog.DTASCII, gocog.DTUndefined, gocog.DTRational, gocog.DTSRational}
	for _, dt := range nonNumeric {
		if isNumericDataType(dt) {
			t.Errorf("data type %d should not be numeric", dt)
		}
	}
}
