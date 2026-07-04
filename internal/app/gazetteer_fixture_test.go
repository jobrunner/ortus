package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	"github.com/jobrunner/ortus/internal/application/gazetteer"
	"github.com/jobrunner/ortus/internal/domain"
)

// Committed fixture (built by cmd/gazetteer-fixture from the real GeoPackage): a
// small, simplified extract covering curated points across Cyprus, the
// Kreuzwertheim/Wertheim BY↔BW border, and level-8 units in Greece, Israel, an
// Arabic region (UAE) and Russia (Cyrillic). Unlike the env-gated e2e tests, this
// runs in CI and asserts every point's full admin chain + real bearing against a
// golden snapshot — proving multi-script names and the boundary constraint on
// real data without shipping the 3 GiB dataset.
//
// Regenerate after a dataset change (the generator is //go:build ignore, so run
// it by file path, not by package):
//
//	go run cmd/gazetteer-fixture/main.go -simplify 0.002
type fixtureChainLevel struct {
	Level          int    `json:"level"`
	Equivalent     string `json:"equivalent"`
	Name           string `json:"name"`
	NameNative     string `json:"name_native"`
	NameSource     string `json:"name_source"`
	LocalTerm      string `json:"local_term"`
	EquivalentDesc string `json:"equivalent_description"`
}
type fixtureBearing struct {
	Label      string `json:"label"`
	Reference  string `json:"reference"`
	NameNative string `json:"name_native"`
	NameSource string `json:"name_source"`
}
type fixtureGolden struct {
	Point struct {
		Label string  `json:"label"`
		Lat   float64 `json:"lat"`
		Lon   float64 `json:"lon"`
	} `json:"point"`
	Country string              `json:"country_iso"`
	Chain   []fixtureChainLevel `json:"chain"`
	Bearing fixtureBearing      `json:"bearing"`
}

func TestGazetteerFixtureGolden(t *testing.T) {
	ctx := context.Background()

	manifest, err := gazetteer.ParseManifest(mustRead(t, "testdata/gazetteer-manifest.yaml"))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	levels, err := gazetteer.ParseLevelReference(mustRead(t, "testdata/gazetteer-sidecar.yaml"))
	if err != nil {
		t.Fatalf("ParseLevelReference: %v", err)
	}
	nameSources, err := gazetteer.ParseNameSources(mustRead(t, "testdata/gazetteer-name-sources.yaml"))
	if err != nil {
		t.Fatalf("ParseNameSources: %v", err)
	}
	idx, err := geopackage.OpenGazetteerIndex(ctx, "testdata/gazetteer-fixture.gpkg", geopackage.Options{})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.VerifySRID(ctx); err != nil {
		t.Fatalf("VerifySRID on fixture: %v", err)
	}
	svc := gazetteer.NewService(idx, manifest, levels, nil, true)
	svc.SetNameSources(nameSources)

	var golden []fixtureGolden
	if err := json.Unmarshal(mustRead(t, "testdata/gazetteer-golden.json"), &golden); err != nil {
		t.Fatalf("golden: %v", err)
	}
	if len(golden) == 0 {
		t.Fatal("golden is empty")
	}

	for _, want := range golden {
		t.Run(want.Point.Label, func(t *testing.T) {
			coord := domain.NewWGS84Coordinate(want.Point.Lon, want.Point.Lat)

			loc, err := svc.Locate(ctx, coord)
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if loc.CountryISO != want.Country {
				t.Errorf("country = %q, want %q", loc.CountryISO, want.Country)
			}
			if len(loc.Chain) != len(want.Chain) {
				t.Fatalf("chain length = %d, want %d\n got %v", len(loc.Chain), len(want.Chain), loc.Chain)
			}
			for i, u := range loc.Chain {
				w := want.Chain[i]
				if u.Level != w.Level || u.Name != w.Name || u.Equivalent != w.Equivalent {
					t.Errorf("chain[%d] = {L%d %s %q}, want {L%d %s %q}",
						i, u.Level, u.Equivalent, u.Name, w.Level, w.Equivalent, w.Name)
				}
				// Provenance + tier meaning: multi-script names, romanization code,
				// country-specific term and generic equivalent description.
				if u.NameNative != w.NameNative || u.NameSource.Code != w.NameSource {
					t.Errorf("chain[%d] provenance = {native %q source %q}, want {native %q source %q}",
						i, u.NameNative, u.NameSource.Code, w.NameNative, w.NameSource)
				}
				if u.LocalTerm != w.LocalTerm || u.EquivalentDesc != w.EquivalentDesc {
					t.Errorf("chain[%d] meaning = {local %q desc %q}, want {local %q desc %q}",
						i, u.LocalTerm, u.EquivalentDesc, w.LocalTerm, w.EquivalentDesc)
				}
			}

			fix, err := svc.Bearing(ctx, coord, domain.DefaultBearingPolicy())
			if err != nil {
				t.Fatalf("Bearing: %v", err)
			}
			if fix.Label != want.Bearing.Label {
				t.Errorf("bearing = %q, want %q", fix.Label, want.Bearing.Label)
			}
			if fix.Reference.NameNative != want.Bearing.NameNative || fix.Reference.NameSource.Code != want.Bearing.NameSource {
				t.Errorf("bearing provenance = {native %q source %q}, want {native %q source %q}",
					fix.Reference.NameNative, fix.Reference.NameSource.Code, want.Bearing.NameNative, want.Bearing.NameSource)
			}
		})
	}
}
