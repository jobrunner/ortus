package domain

import (
	"errors"
	"testing"
)

func TestParseMGRS(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantZone int
		wantHemi Hemisphere
		wantE    float64
		wantN    float64
		wantPrec int
	}{
		{
			name:     "zone 31N, 1m precision, spaced",
			input:    "31U CT 03760 87415",
			wantZone: 31, wantHemi: HemisphereNorth,
			wantE: 303760, wantN: 5787415, wantPrec: 5,
		},
		{
			name:     "zone 31N, 1m precision, no spaces",
			input:    "31UCT0376087415",
			wantZone: 31, wantHemi: HemisphereNorth,
			wantE: 303760, wantN: 5787415, wantPrec: 5,
		},
		{
			name:     "zone 31N, 1m precision, mixed spacing",
			input:    "31U CT0376087415",
			wantZone: 31, wantHemi: HemisphereNorth,
			wantE: 303760, wantN: 5787415, wantPrec: 5,
		},
		{
			name:     "zone 48N (Hanoi), 1m precision",
			input:    "48Q WJ 86683 25450",
			wantZone: 48, wantHemi: HemisphereNorth,
			wantE: 586683, wantN: 2325450, wantPrec: 5,
		},
		{
			// Baghdad, per NGA/wutools worked example. Band S is 32-40°N — north,
			// not south, despite the "38S" grid zone designation looking similar
			// to a hemisphere letter.
			name:     "zone 38N (Baghdad), 1km precision",
			input:    "38S MB 44 88",
			wantZone: 38, wantHemi: HemisphereNorth,
			wantE: 444000, wantN: 3688000, wantPrec: 2,
		},
		{
			name:     "lowercase input",
			input:    "31u ct 03760 87415",
			wantZone: 31, wantHemi: HemisphereNorth,
			wantE: 303760, wantN: 5787415, wantPrec: 5,
		},
		{
			// Same point as the first case, at 10 km precision: truncates to the
			// south-west corner of the 10 km cell, not the 1 m point.
			name:     "10km precision, single digit per axis",
			input:    "31U CT 0 8",
			wantZone: 31, wantHemi: HemisphereNorth,
			wantE: 300000, wantN: 5780000, wantPrec: 1,
		},
		{
			// Honolulu, per Wikipedia's MGRS article worked example (4Q FJ 1234
			// 6789) — covers the 'A'-origin column set on an even zone.
			name:     "zone 4N (Honolulu), 100m precision",
			input:    "4Q FJ 1234 6789",
			wantZone: 4, wantHemi: HemisphereNorth,
			wantE: 612340, wantN: 2367890, wantPrec: 4,
		},
		{
			// Las Vegas, per proj4js/mgrs test suite (11SPA7234911844) — covers
			// the 'J'-origin column set on an odd zone.
			name:     "zone 11N (Las Vegas), 1m precision",
			input:    "11S PA 72349 11844",
			wantZone: 11, wantHemi: HemisphereNorth,
			wantE: 672349, wantN: 4011844, wantPrec: 5,
		},
		{
			// Sydney Opera House. Independently cross-checked against the
			// standard Snyder/EPSG forward Transverse Mercator formula (not
			// derived from this package), confirming the southern false-northing
			// path (10,000,000 m at the equator) and the 'H' latitude band.
			name:     "zone 56S (Sydney), 1m precision",
			input:    "56H LH 34900 52288",
			wantZone: 56, wantHemi: HemisphereSouth,
			wantE: 334900, wantN: 6252288, wantPrec: 5,
		},
		{
			// Cape Town, same independent cross-check method as Sydney.
			name:     "zone 34S (Cape Town), 1m precision",
			input:    "34H BH 61881 43182",
			wantZone: 34, wantHemi: HemisphereSouth,
			wantE: 261881, wantN: 6243182, wantPrec: 5,
		},
		{
			// Rio de Janeiro, same independent cross-check method as Sydney.
			name:     "zone 23S (Rio de Janeiro), 1m precision",
			input:    "23K PQ 87394 65634",
			wantZone: 23, wantHemi: HemisphereSouth,
			wantE: 687394, wantN: 7465634, wantPrec: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMGRS(tt.input)
			if err != nil {
				t.Fatalf("ParseMGRS(%q) unexpected error: %v", tt.input, err)
			}
			if got.Zone != tt.wantZone {
				t.Errorf("Zone = %d, want %d", got.Zone, tt.wantZone)
			}
			if got.Hemisphere != tt.wantHemi {
				t.Errorf("Hemisphere = %v, want %v", got.Hemisphere, tt.wantHemi)
			}
			if got.Easting != tt.wantE {
				t.Errorf("Easting = %f, want %f", got.Easting, tt.wantE)
			}
			if got.Northing != tt.wantN {
				t.Errorf("Northing = %f, want %f", got.Northing, tt.wantN)
			}
			if got.Precision != tt.wantPrec {
				t.Errorf("Precision = %d, want %d", got.Precision, tt.wantPrec)
			}
		})
	}
}

func TestParseMGRSErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"garbage", "not an mgrs string"},
		{"zone 0", "0U CT 03760 87415"},
		{"zone 61", "61U CT 03760 87415"},
		{"band I excluded", "31I CT 03760 87415"},
		{"band O excluded", "31O CT 03760 87415"},
		{"polar band A out of scope", "31A CT 03760 87415"},
		{"polar band Z out of scope", "31Z CT 03760 87415"},
		{"grid square column I excluded", "31U IT 03760 87415"},
		{"grid square row beyond V", "31U CX 03760 87415"},
		{"unequal digit groups", "31U CT 0376 87415"},
		{"odd total digit count", "31U CT 123"},
		{"more than 5 digits per axis", "31U CT 037600 874150"},
		{"no digits at all (100km precision unsupported)", "31U CT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMGRS(tt.input)
			if err == nil {
				t.Fatalf("ParseMGRS(%q) expected error, got nil", tt.input)
			}
			if !errors.Is(err, ErrInvalidMGRS) {
				t.Errorf("ParseMGRS(%q) error = %v, want wrapping ErrInvalidMGRS", tt.input, err)
			}
		})
	}
}

func TestUTMSRIDForZone(t *testing.T) {
	tests := []struct {
		name    string
		zone    int
		hemi    Hemisphere
		want    int
		wantErr bool
	}{
		{"zone 31 north", 31, HemisphereNorth, 32631, false},
		{"zone 48 north", 48, HemisphereNorth, 32648, false},
		{"zone 38 south", 38, HemisphereSouth, 32738, false},
		{"zone 1 north", 1, HemisphereNorth, 32601, false},
		{"zone 60 south", 60, HemisphereSouth, 32760, false},
		{"zone 0 invalid", 0, HemisphereNorth, 0, true},
		{"zone 61 invalid", 61, HemisphereNorth, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UTMSRIDForZone(tt.zone, tt.hemi)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UTMSRIDForZone(%d, %v) error = %v, wantErr %v", tt.zone, tt.hemi, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("UTMSRIDForZone(%d, %v) = %d, want %d", tt.zone, tt.hemi, got, tt.want)
			}
		})
	}
}
