package app

import (
	"context"
	"errors"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
)

// warmGazetteerTimeout bounds the startup warmup so a slow/misconfigured DEM
// can't wedge startup — warmup is best-effort, not a gate on correctness.
const warmGazetteerTimeout = 30 * time.Second

// warmGazetteer runs the internal gazetteer lookups (Locate, Islands, Bearing,
// Exposure, Elevation) once at startup so the FIRST real request isn't cold — it
// exercises every path a /query enrichment touches, not a single call. The cold
// cost is the SpatiaLite connection + mod_spatialite
// init + rtree/page-cache warm-up and the first DEM tile open; deferring it to the
// first client request is what made that request time out ("Load failed", then
// fine) after every deploy.
//
// It runs synchronously before the listener starts, so it DOES delay readiness —
// but only by at most warmGazetteerTimeout (it never blocks indefinitely). A
// warmup that hits a real error (e.g. a timeout or DEM failure) is logged at WARN
// so a still-cold first request is diagnosable; "no result" at the warmup point
// (ErrNotFound / an unwired optional feature) is normal and not treated as failure.
func (a *App) warmGazetteer(ctx context.Context) {
	w := a.Config.Gazetteer.Warmup
	if a.Gazetteer == nil || !w.Enabled {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, warmGazetteerTimeout)
	defer cancel()
	coord := domain.NewWGS84Coordinate(w.Lon, w.Lat)
	start := time.Now()

	var firstErr error
	record := func(err error) {
		// ErrNotFound (no coverage at the warmup point) and nil are expected; only a
		// real failure (timeout, I/O, decode) signals the path may still be cold.
		if err != nil && !errors.Is(err, domain.ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	// Exercise every path a /query enrichment touches; results are irrelevant — the
	// point is to warm the machinery, not to assert coverage at the warmup point.
	_, err := a.Gazetteer.Locate(wctx, coord)
	record(err)
	_, err = a.Gazetteer.Islands(wctx, coord)
	record(err)
	_, err = a.Gazetteer.Bearing(wctx, coord, a.gazetteerPolicy.OrDefault())
	record(err)
	_, err = a.Gazetteer.Exposure(wctx, coord)
	record(err)
	_, err = a.Gazetteer.Elevation(wctx, coord)
	record(err)

	if firstErr != nil {
		a.Logger.Warn("gazetteer warmup hit an error — the first real request may still be cold",
			"lon", w.Lon, "lat", w.Lat, "duration", time.Since(start), "error", firstErr)
		return
	}
	a.Logger.Info("gazetteer warmup complete",
		"lon", w.Lon, "lat", w.Lat, "duration", time.Since(start))
}
