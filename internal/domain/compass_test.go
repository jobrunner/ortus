package domain

import (
	"math"
	"testing"
)

func TestCompass8(t *testing.T) {
	cases := map[float64]string{
		0:   "N",
		22:  "N",  // below the 22.5° N/NE boundary
		45:  "NE", // exactly on a point
		90:  "E",
		135: "SE",
		180: "S",
		225: "SW",
		270: "W",
		315: "NW",
		359: "N", // wraps back to N
		360: "N", // normalized to 0
		-90: "W", // negative normalized to 270
		720: "N", // multiple turns
	}
	for az, want := range cases {
		if got := Compass(az, 8); got != want {
			t.Errorf("Compass(%v, 8) = %q, want %q", az, got, want)
		}
	}
}

func TestCompass16(t *testing.T) {
	cases := map[float64]string{
		0:   "N",
		22:  "NNE",
		45:  "NE",
		67:  "ENE",
		90:  "E",
		338: "NNW",
		360: "N",
	}
	for az, want := range cases {
		if got := Compass(az, 16); got != want {
			t.Errorf("Compass(%v, 16) = %q, want %q", az, got, want)
		}
	}
}

func TestCompassUnknownPointsFallsBackToEight(t *testing.T) {
	// Any points value other than 16 uses the 8-point rose.
	if got := Compass(45, 5); got != "NE" {
		t.Errorf("Compass(45, 5) = %q, want NE (8-point fallback)", got)
	}
}

func TestRoundDistanceKM(t *testing.T) {
	cases := map[float64]float64{
		0.24: 0,
		0.25: 0.5,
		3.7:  3.5,
		3.8:  4,
		9.9:  10,
		10.4: 10,
		10.6: 11,
		42.3: 42,
	}
	for in, want := range cases {
		if got := RoundDistanceKM(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("RoundDistanceKM(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestFormatBearingLabel(t *testing.T) {
	if got := FormatBearingLabel(4, "E", "Würzburg"); got != "4 km E Würzburg" {
		t.Errorf("got %q", got)
	}
	if got := FormatBearingLabel(3.5, "SW", "Volkach"); got != "3.5 km SW Volkach" {
		t.Errorf("got %q", got)
	}
}
