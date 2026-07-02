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
//
// newRealGazetteer builds the gazetteer service against the real dataset, or
// skips the test when ORTUS_GAZETTEER_GPKG is unset (so CI stays green).
func newRealGazetteer(t *testing.T) *gazetteer.Service {
	t.Helper()
	gpkgPath := os.Getenv("ORTUS_GAZETTEER_GPKG")
	if gpkgPath == "" {
		t.Skip("set ORTUS_GAZETTEER_GPKG to run the real-data gazetteer test")
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
	if err := idx.VerifySRID(ctx); err != nil {
		t.Fatalf("VerifySRID on real file: %v", err)
	}
	return gazetteer.NewService(idx, manifest, levels, nil, true)
}

// stateOf returns the name of the state-tier unit in a resolved locality, or "".
func stateOf(loc *domain.Locality) string {
	for _, u := range loc.Chain {
		if u.Equivalent == "state" {
			return u.Name
		}
	}
	return ""
}

func TestGazetteerEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc := newRealGazetteer(t)

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

// TestGazetteerStateBorder exercises the state boundary constraint on real field
// coordinates straddling the Bavaria / Baden-Württemberg border along the Main
// (Kreuzwertheim BY vs Wertheim BW). It asserts that each point reverse-geocodes
// to the correct state, and that the chosen bearing anchor stays within that same
// state — even where the neighboring town across the river is geometrically
// closer. Opt-in (same env gating as TestGazetteerEndToEnd).
func TestGazetteerStateBorder(t *testing.T) {
	ctx := context.Background()
	svc := newRealGazetteer(t)

	type pt struct {
		group    string
		lat, lon float64
	}
	const (
		by = "Bayern"
		bw = "Baden-Württemberg"
	)
	cases := map[string][]pt{
		by: {
			{"Kreuzwertheim/Am Wasser", 49.763074, 9.519156},
			{"Kreuzwertheim/Am Wasser", 49.761402, 9.523088},
			{"Kreuzwertheim/Am Wasser", 49.761567, 9.524038},
			{"Kreuzwertheim/Am Wasser", 49.761646, 9.525050},
			{"Kreuzwertheim/Waldstück", 49.765911, 9.527868},
			{"Kreuzwertheim/Waldstück", 49.766437, 9.526784},
			{"Kreuzwertheim/Waldstück", 49.767391, 9.528226},
		},
		bw: {
			{"Wertheim/Nah am Wasser", 49.760679, 9.517655},
			{"Wertheim/Nah am Wasser", 49.760348, 9.522176},
			{"Wertheim/Nah am Wasser", 49.760412, 9.521955},
			{"Wertheim/Nah am Wasser", 49.760284, 9.523162},
			{"Wertheim/Nah am Wasser", 49.760363, 9.523871},
			{"Wertheim/Nah am Wasser", 49.759922, 9.523486},
			{"Wertheim/Im Wald", 49.756422, 9.523618},
			{"Wertheim/Im Wald", 49.756928, 9.524287},
			{"Wertheim/Im Wald", 49.756212, 9.524243},
			{"Wertheim/Im Wald", 49.756588, 9.521765},
		},
	}

	for wantState, pts := range cases {
		for _, p := range pts {
			coord := domain.NewWGS84Coordinate(p.lon, p.lat)

			loc, err := svc.Locate(ctx, coord)
			if err != nil {
				t.Errorf("%s (%.6f,%.6f): Locate: %v", p.group, p.lat, p.lon, err)
				continue
			}
			gotState := stateOf(loc)

			fix, err := svc.Bearing(ctx, coord, domain.DefaultBearingPolicy())
			if err != nil {
				t.Errorf("%s (%.6f,%.6f): Bearing: %v", p.group, p.lat, p.lon, err)
				continue
			}

			// The anchor must sit in the same state as the query point.
			refLoc, err := svc.Locate(ctx, fix.Reference.At)
			anchorState := ""
			if err == nil {
				anchorState = stateOf(refLoc)
			}

			t.Logf("%-24s (%.5f,%.5f) state=%-18s → %-28s [anchor state=%s]",
				p.group, p.lat, p.lon, gotState, fix.Label, anchorState)

			if gotState != wantState {
				t.Errorf("%s (%.6f,%.6f): point state = %q, want %q", p.group, p.lat, p.lon, gotState, wantState)
			}
			if anchorState != wantState {
				t.Errorf("%s (%.6f,%.6f): anchor %q is in state %q, want %q (boundary constraint breached)",
					p.group, p.lat, p.lon, fix.Reference.Name, anchorState, wantState)
			}
		}
	}
}
