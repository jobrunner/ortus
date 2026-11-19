package gocog

import (
	"math"
	"testing"

	"github.com/paulmach/orb"
)

// testdata/point-4x4.tif is laid out like a Copernicus DEM tile: 4×4 pixels of
// 0.25°, AREA_OR_POINT=Point, the tiepoint (pixel 0,0) on (10.0, 48.0) — which is
// that pixel's CENTER, not its corner. Each value is 100 + row*10 + col.
const pointFixture = "testdata/point-4x4.tif"

func openPointFixture(t *testing.T) *COG {
	t.Helper()
	c, err := Open(pointFixture, nil)
	if err != nil {
		t.Fatalf("open %s: %v", pointFixture, err)
	}
	return c
}

// TestPixelIsPointBounds: a PixelIsPoint raster's footprint reaches half a pixel
// beyond its outer pixel centers, as GDAL reports it (origin 9.875/48.125). Read
// as PixelIsArea, the tiepoint would be the corner and the footprint 10–11/47–48.
func TestPixelIsPointBounds(t *testing.T) {
	b := openPointFixture(t).Bounds()
	want := orb.Bound{Min: orb.Point{9.875, 47.125}, Max: orb.Point{10.875, 48.125}}
	const eps = 1e-9
	if math.Abs(b.Min[0]-want.Min[0]) > eps || math.Abs(b.Min[1]-want.Min[1]) > eps ||
		math.Abs(b.Max[0]-want.Max[0]) > eps || math.Abs(b.Max[1]-want.Max[1]) > eps {
		t.Fatalf("Bounds = %v, want %v", b, want)
	}
}

// TestPixelIsPointNearestPixel: on a PixelIsPoint raster, PixelFromPoint must pick
// the pixel whose CENTER is nearest — the one GDAL samples. Treating the tiepoint
// as a corner instead picks the pixel up to one whole pixel north-west of the
// point, a systematic half-pixel shift on average.
func TestPixelIsPointNearestPixel(t *testing.T) {
	c := openPointFixture(t)
	cases := []struct {
		name           string
		lon, lat       float64
		wantPx, wantPy int
		wantValue      uint64 // raw Int16 sample; all fixture values are positive
	}{
		{"on a center", 10.0, 48.0, 0, 0, 100},
		{"nearer the next center SE", 10.2, 47.8, 1, 1, 111},        // corner reading: (0,0)
		{"just inside the NW half-pixel rim", 9.9, 48.1, 0, 0, 100}, // corner reading: (-1,-1), outside
		{"last center", 10.75, 47.25, 3, 3, 133},
	}
	for _, tc := range cases {
		px, py := c.PixelFromPoint(orb.Point{tc.lon, tc.lat}, 0)
		if px != tc.wantPx || py != tc.wantPy {
			t.Errorf("%s: PixelFromPoint(%g,%g) = (%d,%d), want (%d,%d)", tc.name, tc.lon, tc.lat, px, py, tc.wantPx, tc.wantPy)
			continue
		}
		rd, err := c.ReadWindow(Rectangle{X: px, Y: py, Width: 1, Height: 1})
		if err != nil {
			t.Fatalf("%s: ReadWindow: %v", tc.name, err)
		}
		if got := rd.At(0, 0, 0); got != tc.wantValue {
			t.Errorf("%s: value = %v, want %v", tc.name, got, tc.wantValue)
		}
	}
}
