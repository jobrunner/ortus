package app

import (
	"context"

	"github.com/jobrunner/ortus/internal/application/gazetteer"
)

// bindGazetteerElevation opens the gazetteer-owned elevation DEM and wires it as
// the gazetteer's elevation sampler. The DEM is opened "out of competition" —
// directly via the raster repository, NOT through the source registry — so it
// never appears under GET /api/v1/sources and is never point-in-polygon queried;
// it is reachable only through the gazetteer's Elevation/Exposure. It runs after
// CleanupOrphaned + LoadAll in Start() so the freshly-unpacked bundle survives and
// the pool-collision check below is meaningful.
//
// It is deliberately non-fatal (no error return): an empty bundle_path (feature
// off) or an unopenable / invalid bundle logs and returns, leaving the sampler
// unset — so Elevation and Exposure stay silent (null) rather than breaking
// startup. This is why a missing elevation layer is never a problem: the dependent
// features simply go quiet.
func (a *App) bindGazetteerElevation(ctx context.Context) {
	if a.Gazetteer == nil {
		return
	}
	ec := a.Config.Gazetteer.Elevation
	if ec.BundlePath == "" {
		a.Logger.Debug("gazetteer elevation off (no bundle_path)")
		return
	}

	// Open the DEM directly into the raster repository — out of competition, never
	// registered as a pool source. Failure is non-fatal: elevation + exposure stay silent.
	src, err := a.RasterRepository.Open(ctx, ec.BundlePath)
	if err != nil {
		a.Logger.Warn("gazetteer elevation disabled — could not open DEM bundle; elevation and exposure will be silent",
			"bundle_path", ec.BundlePath, "error", err)
		return
	}
	if a.Registry.IsLoaded(src.ID) {
		// The DEM is ALSO registered as a pool source (operator left the zip in the
		// sources dir), so Open returned the shared, pool-owned bundle. Do NOT take
		// ownership: borrow it as a sampler only and let the registry's unload close
		// it. Leaving gazetteerElevationSourceID unset keeps closeGazetteerElevation a
		// no-op for it, so we never close a bundle out from under the pool.
		a.Logger.Warn("gazetteer elevation bundle is also present in the sources pool — remove the zip from the storage dir so it stops appearing in /api/v1/sources and being double-queried",
			"id", src.ID)
	} else {
		a.gazetteerElevationSourceID = src.ID // we opened it exclusively → we close it on shutdown
	}

	layer := ec.Layer
	if layer == "" {
		layer = "elevation"
	}
	sampler, err := a.RasterRepository.NewElevationSource(src.ID, layer, ec.AccuracyLayer)
	if err != nil {
		a.Logger.Warn("gazetteer elevation disabled — DEM opened but its elevation layer is unusable; elevation and exposure will be silent",
			"bundle_path", ec.BundlePath, "layer", layer, "error", err)
		a.closeGazetteerElevation(ctx) // release the just-opened bundle so it doesn't leak
		return
	}
	a.Gazetteer.SetElevationSampler(sampler, gazetteer.ElevationMeta{
		VerticalDatum:         ec.VerticalDatum,
		AccuracyM:             ec.AccuracyM,
		AccuracyBasis:         ec.AccuracyBasis,
		PerPointAccuracyBasis: ec.PerPointAccuracyBasis,
		HorizontalM:           ec.HorizontalM,
		SurfaceModel:          ec.SurfaceModel,
	})
	a.Logger.Info("gazetteer elevation enabled",
		"bundle_path", ec.BundlePath,
		"id", src.ID,
		"layer", layer,
		"accuracy_layer", ec.AccuracyLayer,
		"vertical_datum", ec.VerticalDatum,
	)
}

// closeGazetteerElevation releases the gazetteer-owned elevation DEM. Because that
// bundle is opened out of competition (never in the source registry), the normal
// shutdown source-unload loop won't close it — this must. Best-effort; a no-op
// when no out-of-competition DEM was opened, and Close on an unknown id is a no-op.
func (a *App) closeGazetteerElevation(ctx context.Context) {
	if a.gazetteerElevationSourceID == "" {
		return
	}
	if err := a.RasterRepository.Close(ctx, a.gazetteerElevationSourceID); err != nil {
		a.Logger.Error("gazetteer elevation DEM close error", "id", a.gazetteerElevationSourceID, "error", err)
	}
	a.gazetteerElevationSourceID = ""
}
