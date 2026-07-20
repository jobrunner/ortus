package gazetteer

import (
	"math"
	"testing"
)

// tiltedPlane builds a 3×3 window (spacing sp) of a planar surface whose gradient
// is (gx east, gy north) meters per meter, centered at height c0. Elevation at an
// offset (dx east, dy north) meters is c0 + gx*dx + gy*dy.
func tiltedPlane(c0, gx, gy, sp float64) horn3x3 {
	at := func(dx, dy float64) float64 { return c0 + gx*dx + gy*dy }
	return horn3x3{
		nw: at(-sp, +sp), n: at(0, +sp), ne: at(+sp, +sp),
		w: at(-sp, 0), c: at(0, 0), e: at(+sp, 0),
		sw: at(-sp, -sp), s: at(0, -sp), se: at(+sp, -sp),
	}
}

func TestComputeExposureFlat(t *testing.T) {
	got := computeExposure(tiltedPlane(200, 0, 0, exposureSampleSpacingM), exposureSampleSpacingM)
	if !got.Flat {
		t.Errorf("flat surface: Flat=false, want true")
	}
	if got.SlopeDeg != 0 || got.SlopePercent != 0 {
		t.Errorf("flat surface slope = %v°/%v%%, want 0/0", got.SlopeDeg, got.SlopePercent)
	}
	if got.AspectCompass != "" {
		t.Errorf("flat surface aspect compass = %q, want empty", got.AspectCompass)
	}
}

// TestComputeExposureAspectDirections verifies slope recovery and the aspect
// convention (downslope azimuth, 0=N/90=E) for planes tilted toward each cardinal
// and one diagonal direction. A 10° slope is well above the flat threshold.
func TestComputeExposureAspectDirections(t *testing.T) {
	g := math.Tan(10 * math.Pi / 180) // gradient magnitude for a 10° slope
	sp := exposureSampleSpacingM

	cases := []struct {
		name        string
		gx, gy      float64 // uphill gradient (east, north)
		wantAspect  float64 // expected downslope azimuth
		wantCompass string
	}{
		// Uphill north (gy>0) ⇒ downhill south ⇒ aspect 180.
		{"faces south", 0, +g, 180, "S"},
		{"faces north", 0, -g, 0, "N"},
		{"faces east", -g, 0, 90, "E"},
		{"faces west", +g, 0, 270, "W"},
		// Uphill toward NW (gx<0 east means uphill west; gy>0 uphill north) ⇒
		// downhill SE ⇒ aspect 135.
		{"faces southeast", -g / math.Sqrt2, +g / math.Sqrt2, 135, "SE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeExposure(tiltedPlane(500, tc.gx, tc.gy, sp), sp)
			if got.Flat {
				t.Fatalf("10° slope reported Flat")
			}
			if math.Abs(got.SlopeDeg-10) > 0.01 {
				t.Errorf("slope = %.4f°, want 10°", got.SlopeDeg)
			}
			if d := math.Abs(got.SlopePercent - g*100); d > 0.01 {
				t.Errorf("slope_percent = %.4f, want %.4f", got.SlopePercent, g*100)
			}
			// Aspect within a degree (compare on the circle).
			diff := math.Mod(math.Abs(got.AspectDeg-tc.wantAspect), 360)
			if diff > 180 {
				diff = 360 - diff
			}
			if diff > 1.0 {
				t.Errorf("aspect = %.2f°, want %.0f°", got.AspectDeg, tc.wantAspect)
			}
			if got.AspectCompass != tc.wantCompass {
				t.Errorf("aspect compass = %q, want %q", got.AspectCompass, tc.wantCompass)
			}
		})
	}
}

// TestComputeExposureFlatThreshold checks the boundary: a slope just below the
// threshold is flagged Flat (aspect suppressed), just above keeps its aspect.
func TestComputeExposureFlatThreshold(t *testing.T) {
	sp := exposureSampleSpacingM
	below := math.Tan((exposureFlatThresholdDeg - 0.5) * math.Pi / 180)
	above := math.Tan((exposureFlatThresholdDeg + 0.5) * math.Pi / 180)

	if got := computeExposure(tiltedPlane(100, 0, below, sp), sp); !got.Flat || got.AspectCompass != "" {
		t.Errorf("below threshold: Flat=%v compass=%q, want Flat=true empty", got.Flat, got.AspectCompass)
	}
	if got := computeExposure(tiltedPlane(100, 0, above, sp), sp); got.Flat || got.AspectCompass == "" {
		t.Errorf("above threshold: Flat=%v compass=%q, want Flat=false non-empty", got.Flat, got.AspectCompass)
	}
}

// TestComputeExposureNoiseSensitivity documents (and guards) the Copernicus
// accuracy analysis: on gentle real terrain the DEM's per-pixel noise dominates
// the gradient. With ~2 m of random error on a nominally flat window over the
// 60 m Horn baseline, the recovered slope is several degrees — which is why the
// flat threshold suppresses aspect below ~2°. This is deterministic (fixed
// perturbation), not randomized.
func TestComputeExposureNoiseSensitivity(t *testing.T) {
	sp := exposureSampleSpacingM
	w := tiltedPlane(300, 0, 0, sp) // truly flat …
	w.n += 2.0                      // … but the north sample reads 2 m high (noise)
	w.s -= 2.0                      // and the south sample 2 m low
	got := computeExposure(w, sp)
	// dzdy = (2*2 - 2*(-2)) / (8*30) = 8/240 ≈ 0.0333 ⇒ slope ≈ 1.9°.
	if got.SlopeDeg < 1.5 || got.SlopeDeg > 2.5 {
		t.Errorf("noise-driven slope = %.2f°, expected ~1.9° (2 m over the 60 m baseline)", got.SlopeDeg)
	}
	if !got.Flat {
		t.Logf("note: %.2f° noise slope is above the %.1f° flat threshold — aspect would be spurious",
			got.SlopeDeg, exposureFlatThresholdDeg)
	}
}
