package raster

import "testing"

// TestTileFileName covers the SW-corner naming: hemisphere letters and the
// 2-digit latitude / 3-digit longitude zero-padding, including negatives.
func TestTileFileName(t *testing.T) {
	const pattern = "{ns}{lat}_{ew}{lon}.tif"
	cases := []struct {
		lat, lon int
		want     string
	}{
		{49, 10, "N49_E010.tif"},
		{49, 9, "N49_E009.tif"},
		{50, 10, "N50_E010.tif"},
		{0, 0, "N00_E000.tif"},
		{-1, -1, "S01_W001.tif"},
		{-34, 151, "S34_E151.tif"},
		{72, -25, "N72_W025.tif"},
	}
	for _, c := range cases {
		if got := tileFileName(pattern, c.lat, c.lon); got != c.want {
			t.Errorf("tileFileName(%d,%d) = %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
}

// TestCellFor covers the floor-to-SW-corner routing, incl. negatives and a
// non-unit grid.
func TestCellFor(t *testing.T) {
	ts := &tileset{gridDeg: 1}
	cases := []struct {
		lon, lat         float64
		wantLat, wantLon int
	}{
		{10.3, 49.7, 49, 10},
		{9.999, 49.001, 49, 9},
		{10.0, 50.0, 50, 10},   // exact corner belongs to that cell
		{-0.5, -0.5, -1, -1},   // just SW of the origin
		{-24.2, 71.9, 71, -25}, // NW quadrant
	}
	for _, c := range cases {
		gotLat, gotLon := ts.cellFor(c.lon, c.lat)
		if gotLat != c.wantLat || gotLon != c.wantLon {
			t.Errorf("cellFor(%g,%g) = (%d,%d), want (%d,%d)", c.lon, c.lat, gotLat, gotLon, c.wantLat, c.wantLon)
		}
	}

	// grid_deg 2 snaps to even degrees.
	ts2 := &tileset{gridDeg: 2}
	if lat, lon := ts2.cellFor(11.0, 49.0); lat != 48 || lon != 10 {
		t.Errorf("cellFor grid2 = (%d,%d), want (48,10)", lat, lon)
	}
}

// TestPresent reflects the captured directory listing (the hot-path coverage
// check that avoids a filesystem stat per query).
func TestPresent(t *testing.T) {
	ts := &tileset{files: map[string]bool{"N49_E010.tif": true}}
	if !ts.present("N49_E010.tif") {
		t.Error("present should be true for a listed tile")
	}
	if ts.present("N50_E010.tif") {
		t.Error("present should be false for an unlisted tile (→ sea level)")
	}
}
