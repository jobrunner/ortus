package app

import "context"

// bindGazetteerBuiltUp opens the gazetteer-owned built-up raster and wires it as the
// gazetteer's built-up sampler (used to gate the "in <place>" bearing decision). Like
// the elevation DEM it is opened "out of competition" — directly via the raster
// repository, never through the source registry — so it never appears under
// GET /api/v1/sources and is never point-in-polygon queried. It runs after
// CleanupOrphaned + LoadAll in Start().
//
// Deliberately non-fatal (no error return): an empty bundle_path (gate off) or an
// unopenable/invalid bundle logs and returns, leaving the sampler unset — so the "in"
// decision simply falls back to distance alone rather than breaking startup.
func (a *App) bindGazetteerBuiltUp(ctx context.Context) {
	if a.Gazetteer == nil {
		return
	}
	bc := a.Config.Gazetteer.BuiltUp
	if bc.BundlePath == "" {
		a.Logger.Debug("gazetteer built-up gate off (no bundle_path)")
		return
	}

	src, err := a.RasterRepository.Open(ctx, bc.BundlePath)
	if err != nil {
		a.Logger.Warn("gazetteer built-up gate disabled — could not open bundle; 'in <place>' falls back to distance only",
			"bundle_path", bc.BundlePath, "error", err)
		return
	}
	if a.Registry.IsLoaded(src.ID) {
		// Also present in the sources pool (operator left the zip in the sources dir):
		// borrow it as a sampler only and let the registry's unload close it — do not
		// take ownership (leaving gazetteerBuiltUpSourceID unset keeps close a no-op).
		a.Logger.Warn("gazetteer built-up bundle is also in the sources pool — remove the zip from the storage dir so it stops appearing in /api/v1/sources and being double-queried",
			"id", src.ID)
	} else {
		a.gazetteerBuiltUpSourceID = src.ID // opened exclusively → we close it on shutdown
	}

	layer := bc.Layer
	if layer == "" {
		layer = "builtup"
	}
	sampler, err := a.RasterRepository.NewBuiltUpSource(src.ID, layer)
	if err != nil {
		a.Logger.Warn("gazetteer built-up gate disabled — bundle opened but its layer is unusable; 'in <place>' falls back to distance only",
			"bundle_path", bc.BundlePath, "layer", layer, "error", err)
		a.closeGazetteerBuiltUp(ctx) // release the just-opened bundle so it doesn't leak
		return
	}
	a.Gazetteer.SetBuiltUpSampler(sampler, bc.MinM2)
	a.Logger.Info("gazetteer built-up gate enabled",
		"bundle_path", bc.BundlePath, "id", src.ID, "layer", layer, "min_m2", bc.MinM2)
}

// closeGazetteerBuiltUp releases the gazetteer-owned built-up raster. Because it is
// opened out of competition (never in the source registry), the normal shutdown
// source-unload loop won't close it — this must. Best-effort; a no-op when none was
// opened, and Close on an unknown id is a no-op.
func (a *App) closeGazetteerBuiltUp(ctx context.Context) {
	if a.gazetteerBuiltUpSourceID == "" {
		return
	}
	if err := a.RasterRepository.Close(ctx, a.gazetteerBuiltUpSourceID); err != nil {
		a.Logger.Error("gazetteer built-up raster close error", "id", a.gazetteerBuiltUpSourceID, "error", err)
	}
	a.gazetteerBuiltUpSourceID = ""
}
