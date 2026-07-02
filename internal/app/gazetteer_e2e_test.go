package app

import (
	"context"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	"github.com/jobrunner/ortus/internal/application/gazetteer"
	"github.com/jobrunner/ortus/internal/domain"
)

// TestGazetteerEndToEnd exercises the real pipeline against the actual
// osm-admin-places GeoPackage + sidecar. It is opt-in: set ORTUS_GAZETTEER_GPKG
// (and optionally _MANIFEST / _SIDECAR) to run it, otherwise it skips — so CI,
// which has no 3 GiB dataset, stays green.
//
//	ORTUS_GAZETTEER_GPKG=data/gazetteer/osm-admin-places.gpkg \
//	ORTUS_GAZETTEER_MANIFEST=data/gazetteer/ortus-gazetteer.yaml \
//	ORTUS_GAZETTEER_SIDECAR=data/gazetteer/admin_levels_west_palearctic.yaml \
//	go test ./internal/app -run TestGazetteerEndToEnd -v
func TestGazetteerEndToEnd(t *testing.T) {
	gpkgPath := os.Getenv("ORTUS_GAZETTEER_GPKG")
	if gpkgPath == "" {
		t.Skip("set ORTUS_GAZETTEER_GPKG to run the real-data gazetteer e2e test")
	}
	manifestPath := envOr("ORTUS_GAZETTEER_MANIFEST", "data/gazetteer/ortus-gazetteer.yaml")
	sidecarPath := envOr("ORTUS_GAZETTEER_SIDECAR", "data/gazetteer/admin_levels_west_palearctic.yaml")
	ctx := context.Background()

	manifest, err := gazetteer.ParseManifest(mustRead(t, manifestPath))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	levels, err := gazetteer.ParseLevelReference(mustRead(t, sidecarPath))
	if err != nil {
		t.Fatalf("ParseLevelReference: %v", err)
	}

	idx, err := geopackage.OpenGazetteerIndex(ctx, gpkgPath, geopackage.Options{})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	// The whole bearing metric depends on this: ellipsoidal Distance must resolve
	// SRID 4326 on the real file.
	if err := idx.VerifySRID(ctx); err != nil {
		t.Fatalf("VerifySRID on real file: %v", err)
	}

	svc := gazetteer.NewService(idx, manifest, levels, nil, true)

	// Locate: Würzburg city center → an admin chain reaching the state tier.
	wuerzburg := domain.NewWGS84Coordinate(9.9294, 49.7913)
	loc, err := svc.Locate(ctx, wuerzburg)
	if err != nil {
		t.Fatalf("Locate(Würzburg): %v", err)
	}
	if loc.CountryISO == "" || len(loc.Chain) == 0 {
		t.Errorf("Locate returned empty locality: %+v", loc)
	}
	var hasState bool
	for _, u := range loc.Chain {
		t.Logf("admin L%d %-14s %s", u.Level, u.Equivalent, u.Name)
		if u.Equivalent == "state" {
			hasState = true
		}
	}
	if loc.CountryISO != "DE" {
		t.Errorf("country = %q, want DE", loc.CountryISO)
	}
	if !hasState {
		t.Errorf("no 'state' tier in the Würzburg chain — sidecar mapping or parent chain incomplete")
	}

	// Bearing: a rural point east of Würzburg → a real, findable anchor.
	rural := domain.NewWGS84Coordinate(10.15, 49.83)
	fix, err := svc.Bearing(ctx, rural, domain.DefaultBearingPolicy())
	if err != nil {
		t.Fatalf("Bearing(rural): %v", err)
	}
	t.Logf("bearing: reference=%q class=%s dist=%.2fkm compass=%s label=%q",
		fix.Reference.Name, fix.Reference.Class, fix.DistanceKM, fix.Compass, fix.Label)
	if fix.Label == "" || fix.Reference.Name == "" {
		t.Errorf("empty bearing fix: %+v", fix)
	}

	// Calibration sweep (log-only): eyeball the default reach radii across varied
	// points — a city anchor from afar, a town/village closer in, an inside hit.
	pol := domain.DefaultBearingPolicy()
	for _, p := range []struct {
		name     string
		lon, lat float64
	}{
		{"Würzburg center", 9.9294, 49.7913},
		{"rural E of Würzburg", 10.15, 49.83},
		{"Volkach area", 10.23, 49.86},
		{"near Nürnberg", 11.05, 49.45},
		{"rural Rhön", 10.05, 50.42},
	} {
		f, err := svc.Bearing(ctx, domain.NewWGS84Coordinate(p.lon, p.lat), pol)
		switch {
		case err == nil:
			t.Logf("sweep %-22s → %s", p.name, f.Label)
		case err.Error() != "":
			t.Logf("sweep %-22s → (none: %v)", p.name, err)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
