package gazetteer

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeSampler is a canned output.ElevationSampler for service tests.
type fakeSampler struct {
	reading output.ElevationReading
	ok      bool
	err     error
	license domain.License
}

func (f fakeSampler) ElevationAt(context.Context, domain.Coordinate) (reading output.ElevationReading, ok bool, err error) {
	return f.reading, f.ok, f.err
}
func (f fakeSampler) License() domain.License { return f.license }

func elevMeta() ElevationMeta {
	return ElevationMeta{
		VerticalDatum: "EGM2008", AccuracyM: 4.0, AccuracyBasis: "GLO-30 LE90 (absolute)",
		PerPointAccuracyBasis: "Copernicus HEM (per-pixel 1σ)", HorizontalM: 6.0, SurfaceModel: "DSM",
	}
}

func wgs84(lon, lat float64) domain.Coordinate {
	return domain.Coordinate{X: lon, Y: lat, SRID: domain.SRIDWGS84}
}

// TestElevationUnwired: without a sampler the method returns (nil, nil) so the
// response omits the block rather than erroring.
func TestElevationUnwired(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	got, err := svc.Elevation(context.Background(), wgs84(9.93, 49.79))
	if err != nil || got != nil {
		t.Fatalf("Elevation() = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestElevationSampled: a covered point carries the meters plus the constant
// metadata and the DEM license (distinct from the gazetteer's own).
func TestElevationSampled(t *testing.T) {
	lic := domain.License{Name: "Copernicus DEM GLO-30", Attribution: "© DLR/Airbus/ESA"}
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(fakeSampler{reading: output.ElevationReading{Meters: 177.5}, ok: true, license: lic}, elevMeta())

	got, err := svc.Elevation(context.Background(), wgs84(9.93, 49.79))
	if err != nil {
		t.Fatalf("Elevation() error: %v", err)
	}
	if got.Meters != 177.5 || got.SeaLevel {
		t.Errorf("meters/sea_level = %v/%v, want 177.5/false", got.Meters, got.SeaLevel)
	}
	// No per-point accuracy → the dataset constant + its basis.
	if got.VerticalDatum != "EGM2008" || got.AccuracyM != 4.0 || got.AccuracyBasis != "GLO-30 LE90 (absolute)" ||
		got.HorizontalM != 6.0 || got.SurfaceModel != "DSM" {
		t.Errorf("metadata = %+v, want EGM2008/4/LE90/6/DSM", got)
	}
	if got.License.Name != "Copernicus DEM GLO-30" {
		t.Errorf("license = %+v, want Copernicus", got.License)
	}
}

// TestElevationPerPointAccuracy: when the sampler supplies a per-point accuracy
// (HEM), the response uses it plus the per-point basis, not the constant.
func TestElevationPerPointAccuracy(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(fakeSampler{
		reading: output.ElevationReading{Meters: 312, AccuracyM: 2.4, HasAccuracy: true}, ok: true,
	}, elevMeta())

	got, err := svc.Elevation(context.Background(), wgs84(9.93, 49.79))
	if err != nil {
		t.Fatalf("Elevation() error: %v", err)
	}
	if got.AccuracyM != 2.4 || got.AccuracyBasis != "Copernicus HEM (per-pixel 1σ)" {
		t.Errorf("accuracy = %v/%q, want 2.4 / HEM basis", got.AccuracyM, got.AccuracyBasis)
	}
}

// TestElevationSeaLevel: ok=false (no tile) yields the sea-level convention.
func TestElevationSeaLevel(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(fakeSampler{reading: output.ElevationReading{}, ok: false}, elevMeta())

	got, err := svc.Elevation(context.Background(), wgs84(8.0, 54.0))
	if err != nil {
		t.Fatalf("Elevation() error: %v", err)
	}
	if !got.SeaLevel || got.Meters != 0 {
		t.Errorf("got %+v, want sea_level true / meters 0", got)
	}
}

// TestElevationSamplerError propagates a real sampler error.
func TestElevationSamplerError(t *testing.T) {
	sentinel := errors.New("read failed")
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(fakeSampler{err: sentinel}, elevMeta())

	if _, err := svc.Elevation(context.Background(), wgs84(9.93, 49.79)); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

// TestElevationRejectsNonWGS84 mirrors Locate/Bearing: a non-4326 SRID is refused.
func TestElevationRejectsNonWGS84(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	svc.SetElevationSampler(fakeSampler{reading: output.ElevationReading{Meters: 100}, ok: true}, elevMeta())

	_, err := svc.Elevation(context.Background(), domain.Coordinate{X: 4000000, Y: 3000000, SRID: 3035})
	if !errors.Is(err, domain.ErrUnsupportedProjection) {
		t.Fatalf("err = %v, want ErrUnsupportedProjection", err)
	}
}
