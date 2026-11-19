package raster

import "github.com/paulmach/orb"

// tileHit is a point resolved to the acquired tile that holds it; the caller
// must release(key) once done with tile.
type tileHit struct {
	key    [2]int
	tile   *openTile
	px, py int
}

// locate finds the tile holding (lon, lat) and the pixel within it.
//
// The grid cell alone is not enough: a PixelIsPoint tile (Copernicus DEM) puts
// its outer pixel centers ON the cell edges, so its footprint is shifted half a
// pixel and a point in the rim along one edge belongs to the neighboring tile.
// locate therefore tries the cell's tile first and, if the point falls outside
// it, the neighbors — the one in the direction it fell out first, since that is
// almost always the answer. The rest still matter: where adjacent tiles differ in
// longitude spacing (Copernicus widens it at 50°/60°/70° N) a coarse tile's rim
// ends short of the finer one's, so a point can leave the coarse tile to the
// south-east and yet belong to the tile straight south. When the cell has no
// tile, there is no direction and all neighbors are tried. Misses are rare and
// sit on tile edges, so the extra probes cost nothing on the common path.
// found=false means no present tile holds the point.
func (t *tileset) locate(lon, lat float64) (hit tileHit, found bool, err error) {
	latDeg, lonDeg := t.cellFor(lon, lat)
	lonDeg, lon = wrapLon(lonDeg, lon) // +180° is the -180° meridian: cell W180, not E180
	hit, found, dir, err := t.probe(latDeg, lonDeg, lon, lat)
	if err != nil || found {
		return hit, found, err
	}
	for _, d := range neighborOrder(dir) {
		nLonDeg, qLon := wrapLon(lonDeg+d[1]*t.gridDeg, lon)
		hit, found, _, err = t.probe(latDeg+d[0]*t.gridDeg, nLonDeg, qLon, lat)
		if err != nil || found {
			return hit, found, err
		}
	}
	return tileHit{}, false, nil
}

// neighborOrder lists the eight adjacent cell steps ({dLat, dLon}), first one
// first unless it is zero.
func neighborOrder(first [2]int) [][2]int {
	steps := make([][2]int, 0, 8)
	if first != ([2]int{}) {
		steps = append(steps, first)
	}
	for dLat := -1; dLat <= 1; dLat++ {
		for dLon := -1; dLon <= 1; dLon++ {
			if d := [2]int{dLat, dLon}; d != ([2]int{}) && d != first {
				steps = append(steps, d)
			}
		}
	}
	return steps
}

// wrapLon folds a neighbor cell's longitude into [-180, 180) across the
// antimeridian and turns the query longitude by the same 360°, so E179's east
// rim continues into W180 (whose rim reaches below -180) rather than a
// nonexistent E180.
func wrapLon(lonDeg int, lon float64) (cellLon int, queryLon float64) {
	switch {
	case lonDeg >= 180:
		return lonDeg - 360, lon - 360
	case lonDeg < -180:
		return lonDeg + 360, lon + 360
	}
	return lonDeg, lon
}

// probe checks whether the tile of one cell holds (lon, lat). On a hit the tile
// stays acquired. On a miss against a present tile, dir is the cell step
// ({dLat, dLon}, each -1/0/+1) toward the side the point fell out; a zero dir
// means the cell has no tile.
func (t *tileset) probe(latDeg, lonDeg int, lon, lat float64) (hit tileHit, found bool, dir [2]int, err error) {
	name := tileFileName(t.pattern, latDeg, lonDeg)
	if !t.present(name) {
		return tileHit{}, false, [2]int{}, nil
	}
	key := [2]int{latDeg, lonDeg}
	ot, err := t.acquire(key, name)
	if err != nil {
		return tileHit{}, false, [2]int{}, err
	}
	px, py := ot.cog.PixelFromPoint(orb.Point{lon, lat}, 0)
	w, h := ot.cog.Width(), ot.cog.Height()
	if px >= 0 && py >= 0 && px < w && py < h {
		return tileHit{key: key, tile: ot, px: px, py: py}, true, [2]int{}, nil
	}
	t.release(key)
	// Rows grow southward, so falling out below the last row steps one cell south.
	switch {
	case py >= h:
		dir[0] = -1
	case py < 0:
		dir[0] = 1
	}
	switch {
	case px >= w:
		dir[1] = 1
	case px < 0:
		dir[1] = -1
	}
	return tileHit{}, false, dir, nil
}
