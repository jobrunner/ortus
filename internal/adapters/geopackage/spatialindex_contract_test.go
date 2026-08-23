package geopackage

import (
	"context"
	"strings"
	"testing"
)

// The fixture built by buildGazetteerFixture is a deliberately minimal stub:
// places(fid, place, name, admin_id, geom), admin_levels(fid, parent_id,
// admin_level, name, country_iso, geom), and no islands or mountains layer —
// which is also the shape a manifest that outruns its dataset gets paired with.

func TestVerifyContract_AcceptsTheDeclaredShape(t *testing.T) {
	idx := openFixtureIndex(t, false)
	err := idx.VerifyContract(context.Background(), map[string][]string{
		"places":       {"name", "place", "admin_id"},
		"admin_levels": {"name", "admin_level", "parent_id", "country_iso"},
	})
	if err != nil {
		t.Fatalf("VerifyContract on a matching manifest: %v", err)
	}
}

func TestVerifyContract_IgnoresUndeclaredOptionalLayers(t *testing.T) {
	// The fixture has no mountains layer. A manifest that does not declare one
	// must still pass: that tolerance is what lets a manifest ship ahead of the
	// dataset. (A manifest that DOES declare it is covered below.)
	idx := openFixtureIndex(t, false)
	if err := idx.VerifyContract(context.Background(), map[string][]string{
		"places":       {"name"},
		"admin_levels": {"name"},
	}); err != nil {
		t.Fatalf("VerifyContract without optional layers: %v", err)
	}
}

func TestVerifyContract_RejectsAMissingTable(t *testing.T) {
	idx := openFixtureIndex(t, false)
	err := idx.VerifyContract(context.Background(), map[string][]string{
		"places":    {"name"},
		"mountains": {"name", "landform"},
	})
	if err == nil {
		t.Fatal("declared a table the GeoPackage does not have, want an error")
	}
	if !strings.Contains(err.Error(), `table "mountains"`) {
		t.Errorf("error should name the missing table, got: %v", err)
	}
}

func TestVerifyContract_RejectsAMissingColumn(t *testing.T) {
	idx := openFixtureIndex(t, false)
	err := idx.VerifyContract(context.Background(), map[string][]string{
		"places": {"name", "typo_column"},
	})
	if err == nil {
		t.Fatal("declared a column the table does not have, want an error")
	}
	if !strings.Contains(err.Error(), `no column "typo_column"`) {
		t.Errorf("error should name the missing column, got: %v", err)
	}
}

func TestVerifyContract_ReportsEveryViolationAtOnce(t *testing.T) {
	// A schema change tends to break several mappings together; one startup
	// should list all of them rather than making the operator fix and restart
	// once per problem.
	idx := openFixtureIndex(t, false)
	err := idx.VerifyContract(context.Background(), map[string][]string{
		"places":    {"nope_a", "nope_b"},
		"mountains": {"name"},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"nope_a", "nope_b", `table "mountains"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestVerifyContract_SkipsEmptyColumnNames(t *testing.T) {
	// Unset optional mappings arrive as "" and mean "not declared", not "a column
	// with an empty name".
	idx := openFixtureIndex(t, false)
	if err := idx.VerifyContract(context.Background(), map[string][]string{
		"places": {"name", "", ""},
	}); err != nil {
		t.Fatalf("empty column names should be skipped: %v", err)
	}
}
