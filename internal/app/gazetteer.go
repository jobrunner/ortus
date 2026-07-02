package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	"github.com/jobrunner/ortus/internal/application/gazetteer"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// buildGazetteer wires the optional gazetteer (reverse geocode + bearing) from
// config. When disabled it is a no-op (App.Gazetteer stays nil, so no route is
// registered). When enabled it reads the manifest and optional level-reference
// sidecar, opens the dedicated GeoPackage, and constructs the service. Any
// misconfiguration fails startup loudly rather than leaving the feature silently
// broken. The GeoPackage is opened separately from the generic source pool — it
// is read "out of competition", not as a PiP source.
func (a *App) buildGazetteer(ctx context.Context) error {
	cfg := a.Config.Gazetteer
	if !cfg.Enabled {
		return nil
	}

	manifestData, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		return fmt.Errorf("reading gazetteer manifest: %w", err)
	}
	manifest, err := gazetteer.ParseManifest(manifestData)
	if err != nil {
		return err
	}

	var levels gazetteer.LevelResolver
	if cfg.LevelReferencePath != "" {
		refData, err := os.ReadFile(cfg.LevelReferencePath)
		if err != nil {
			return fmt.Errorf("reading gazetteer level reference: %w", err)
		}
		if levels, err = gazetteer.ParseLevelReference(refData); err != nil {
			return err
		}
	}

	idx, err := geopackage.OpenGazetteerIndex(ctx, cfg.GeoPackagePath, geopackage.Options{
		CacheMode:     a.Config.Query.SQLite.CacheMode,
		BusyTimeoutMS: a.Config.Query.SQLite.BusyTimeoutMS,
		JournalMode:   a.Config.Query.SQLite.JournalMode,
		MaxOpenConns:  a.Config.Query.SQLite.MaxOpenConns,
		MaxIdleConns:  a.Config.Query.SQLite.MaxIdleConns,
	})
	if err != nil {
		return fmt.Errorf("opening gazetteer GeoPackage: %w", err)
	}

	// Probe the SRID metadata: if ellipsoidal Distance can't resolve EPSG:4326,
	// the KNN radius silently drops every row. Warn loudly but don't fail — Locate
	// (point-in-polygon) still works without it.
	if err := idx.VerifySRID(ctx); err != nil {
		a.Logger.Warn("gazetteer SRID check failed — bearings may return nothing", "error", err)
	}

	a.Gazetteer = gazetteer.NewService(idx, manifest, levels, nil, true)
	a.gazetteerClose = idx.Close
	// Build the bearing policy from the tuning knobs (config) + the constraint
	// tier (manifest, dataset-bound). Handlers pass this to Bearing().
	b := cfg.Bearing
	a.gazetteerPolicy = domain.BearingPolicy{
		Reach: map[domain.PlaceClass]float64{
			domain.ClassVillage: b.ReachVillageKM,
			domain.ClassTown:    b.ReachTownKM,
			domain.ClassCity:    b.ReachCityKM,
		},
		PreferNearestKM: b.PreferNearestKM,
		ConstraintTier:  manifest.ConstraintTier,
		InsideLabelKM:   b.InsideLabelKM,
		CompassPoints:   b.CompassPoints,
	}
	a.Logger.Info("gazetteer enabled",
		"geopackage", cfg.GeoPackagePath,
		"level_reference", cfg.LevelReferencePath != "",
	)
	return nil
}

// gazetteerPort returns the gazetteer as its input port, guarding the typed-nil
// trap: a nil *gazetteer.Service placed in an input.Gazetteer interface is not
// == nil, which would defeat the adapters' nil checks (spuriously registering
// the route/tool on a disabled feature). Returns a genuine nil interface when
// the feature is off.
func (a *App) gazetteerPort() input.Gazetteer {
	if a.Gazetteer == nil {
		return nil
	}
	return a.Gazetteer
}

// closeGazetteer releases the gazetteer index connection. Best-effort; a second
// call is a no-op.
func (a *App) closeGazetteer() {
	if a.gazetteerClose == nil {
		return
	}
	if err := a.gazetteerClose(); err != nil {
		a.Logger.Error("gazetteer index close error", "error", err)
	}
	a.gazetteerClose = nil
}
