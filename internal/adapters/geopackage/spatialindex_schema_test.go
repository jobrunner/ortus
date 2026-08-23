package geopackage

import (
	"context"
	"testing"
)

// These cover the two fact-reporting methods the startup contract check is built
// on. The judgement about whether a schema satisfies a manifest lives in
// Manifest.VerifyAgainst and is tested there without a database; what has to be
// proven *here* is that the facts are reported faithfully — above all that the two
// ways a layer can be unusable are distinguishable.
//
// The fixture from buildGazetteerFixture has places(place, name, admin_id, geom)
// and admin_levels(parent_id, admin_level, name, country_iso, geom) — plus the
// primary key, which no manifest role maps, so it is not asserted on — both
// registered in gpkg_geometry_columns, and no islands or mountains layer.

func TestTableColumns_ReportsTheActualColumns(t *testing.T) {
	idx := openFixtureIndex(t, false)
	got, err := idx.TableColumns(context.Background(), []string{"places", "admin_levels"})
	if err != nil {
		t.Fatalf("TableColumns: %v", err)
	}
	for table, want := range map[string][]string{
		"places":       {"place", "name", "admin_id", "geom"},
		"admin_levels": {"parent_id", "admin_level", "name", "country_iso", "geom"},
	} {
		for _, c := range want {
			if _, ok := got[table][c]; !ok {
				t.Errorf("%s: column %q missing from the report, got %v", table, c, got[table])
			}
		}
	}
	if _, ok := got["places"]["country_iso"]; ok {
		t.Error("places has no country_iso in the fixture; reporting one would make the check useless")
	}
}

func TestTableColumns_ReportsAMissingTableAsAnEmptySet(t *testing.T) {
	// PRAGMA table_info yields no rows for an unknown table rather than erroring.
	// The caller needs "present but empty", not an absent key or a failure, so it
	// can tell "table not there" from "table there, column missing".
	idx := openFixtureIndex(t, false)
	got, err := idx.TableColumns(context.Background(), []string{"places", "mountains"})
	if err != nil {
		t.Fatalf("a missing table must not be an error: %v", err)
	}
	cols, present := got["mountains"]
	if !present {
		t.Fatal("the key must be present so the caller sees the table was looked up")
	}
	if len(cols) != 0 {
		t.Errorf("a missing table must report no columns, got %v", cols)
	}
	if len(got["places"]) == 0 {
		t.Error("the existing table must still be reported")
	}
}

func TestSpatialLayers_ListsOnlyRegisteredFeatureLayers(t *testing.T) {
	// This is the distinction a columns-only check cannot make: a table can carry
	// every mapped column and still be unqueryable, because reads resolve their
	// geometry column through gpkg_geometry_columns first.
	idx := openFixtureIndex(t, false)
	got, err := idx.SpatialLayers(context.Background())
	if err != nil {
		t.Fatalf("SpatialLayers: %v", err)
	}
	// The geometry COLUMN is reported, not just the row's existence, so a caller
	// can catch a stale registration naming a column the table no longer has.
	for _, want := range []string{"places", "admin_levels"} {
		if got[want] != "geom" {
			t.Errorf("layer %q: geometry column = %q, want \"geom\" (got map %v)", want, got[want], got)
		}
	}
	// gpkg_contents / gpkg_geometry_columns themselves are plain tables, not
	// feature layers — reporting them would defeat the point of the check.
	for _, unwanted := range []string{"gpkg_contents", "gpkg_geometry_columns", "mountains"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%q is not a feature layer but was reported as one", unwanted)
		}
	}
}
