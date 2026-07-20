package gazetteer

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// planeSampler is an ElevationSampler over a planar surface: elevation rises by
// northGradM meters per meter northward and eastGradM per meter eastward, so a
// service test can recover a known slope/aspect end-to-end (through the lon/lat
// ↔ meters offset arithmetic).
type planeSampler struct {
	northGradM float64
	eastGradM  float64
	ok         bool
	err        error
	license    domain.License
}

func (p planeSampler) ElevationAt(_ context.Context, c domain.Coordinate) (output.ElevationReading, bool, error) {
	if p.err != nil {
		return output.ElevationReading{}, false, p.err
	}
	if !p.ok {
		return output.ElevationReading{}, false, nil
	}
	// East–west meters per degree shrink with latitude (cos), matching the service's
	// lon/lat↔meters scaling, so eastGradM is genuinely "meters per meter eastward".
	z := p.northGradM*c.Y*metersPerDegLat + p.eastGradM*c.X*metersPerDegLat*math.Cos(c.Y*math.Pi/180)
	return output.ElevationReading{Meters: z}, true, nil
}
func (p planeSampler) License() domain.License { return p.license }

func TestExposureUnwired(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	got, err := svc.Exposure(context.Background(), wgs84(9.93, 49.79))
	if err != nil || got != nil {
		t.Fatalf("Exposure() = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestExposureSouthFacingPlane wires a plane rising 10° toward the north; the
// service must recover a 10° slope facing south, carrying the DEM license and
// the sample spacing. (A south-facing plane avoids the cos(lat) E-W subtlety, so
// the recovery is exact; the directional convention is covered exhaustively in
// exposure_test.go.)
func TestExposureSouthFacingPlane(t *testing.T) {
	lic := domain.License{Name: "Copernicus DEM GLO-30", Attribution: "© DLR/Airbus/ESA"}
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(planeSampler{northGradM: math.Tan(10 * math.Pi / 180), ok: true, license: lic}, elevMeta())

	got, err := svc.Exposure(context.Background(), wgs84(9.93, 49.79))
	if err != nil {
		t.Fatalf("Exposure() error: %v", err)
	}
	if got == nil {
		t.Fatal("Exposure() = nil, want a result")
	}
	if math.Abs(got.SlopeDeg-10) > 0.05 {
		t.Errorf("slope = %.3f°, want 10°", got.SlopeDeg)
	}
	if got.Flat {
		t.Error("Flat=true on a 10° slope")
	}
	if got.AspectCompass != "S" {
		t.Errorf("aspect compass = %q, want S (downhill south)", got.AspectCompass)
	}
	if d := math.Mod(math.Abs(got.AspectDeg-180), 360); d > 1 {
		t.Errorf("aspect = %.2f°, want ~180°", got.AspectDeg)
	}
	if got.SampleSpacingM != exposureSampleSpacingM {
		t.Errorf("sample spacing = %v, want %v", got.SampleSpacingM, exposureSampleSpacingM)
	}
	if got.License.Name != "Copernicus DEM GLO-30" {
		t.Errorf("license = %+v, want Copernicus", got.License)
	}
}

// TestExposureNoCoverageReturnsNil: if any window sample has no DEM coverage
// (ok=false, e.g. a coastal edge) the gradient is unreliable, so exposure is nil.
func TestExposureNoCoverageReturnsNil(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(planeSampler{ok: false}, elevMeta())
	got, err := svc.Exposure(context.Background(), wgs84(8.0, 54.0))
	if err != nil || got != nil {
		t.Fatalf("Exposure() = (%v, %v), want (nil, nil) on no coverage", got, err)
	}
}

// TestExposureNearPoleReturnsNil: beyond exposureMaxLatitude the E-W spacing
// collapses, so exposure is reported as unavailable rather than sampling a
// meaningless window.
func TestExposureNearPoleReturnsNil(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(planeSampler{northGradM: 0.1, ok: true}, elevMeta())
	got, err := svc.Exposure(context.Background(), wgs84(10.0, 89.0))
	if err != nil || got != nil {
		t.Fatalf("Exposure() near pole = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestExposureSamplerError(t *testing.T) {
	sentinel := errors.New("read failed")
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(planeSampler{err: sentinel}, elevMeta())
	if _, err := svc.Exposure(context.Background(), wgs84(9.93, 49.79)); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestExposureRejectsNonWGS84(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(planeSampler{ok: true}, elevMeta())
	_, err := svc.Exposure(context.Background(), domain.Coordinate{X: 4000000, Y: 3000000, SRID: 3035})
	if !errors.Is(err, domain.ErrUnsupportedProjection) {
		t.Fatalf("err = %v, want ErrUnsupportedProjection", err)
	}
}
