package gazetteer

import (
	"slices"
	"strings"
	"testing"
)

const minimalManifest = `
places:
  layer: places
  rank_column: place
  name_column: name
  admin_fk: admin_id
  country_column: country_iso
admin:
  layer: admin_levels
  level_column: admin_level
  name_column: name
  parent_fk: parent_id
`

func TestExpectedTables_OmitsUndeclaredOptionalLayers(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := m.ExpectedTables()
	for _, absent := range []string{"islands", "mountains", ""} {
		if _, ok := want[absent]; ok {
			t.Errorf("layer %q must not be required — an undeclared optional layer is "+
				"what lets a manifest ship ahead of the dataset", absent)
		}
	}
	if len(want) != 2 {
		t.Errorf("want places + admin_levels only, got %d tables: %v", len(want), want)
	}
}

func TestExpectedTables_RequiresDeclaredOptionalLayers(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest + `
mountains:
  layer: mountains
islands:
  layer: islands
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := m.ExpectedTables()
	cols, ok := want["mountains"]
	if !ok {
		t.Fatal("a declared mountains layer must be checked")
	}
	// The mountains column roles default when the layer is declared, so the
	// defaults are part of the promise and have to be verified too.
	for _, c := range []string{"name", "landform", "ele", "area_km2"} {
		if !slices.Contains(cols, c) {
			t.Errorf("mountains should require the defaulted column %q, got %v", c, cols)
		}
	}
	if _, ok := want["islands"]; !ok {
		t.Error("a declared islands layer must be checked")
	}
}

func TestExpectedTables_SkipsUnsetOptionalColumns(t *testing.T) {
	// The minimal manifest sets no name_native/name_source/population columns, so
	// they must not be demanded of the GeoPackage.
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	for _, c := range m.ExpectedTables()["places"] {
		if c == "" {
			continue // VerifyAgainst skips these
		}
		if !slices.Contains([]string{"place", "name", "admin_id", "country_iso"}, c) {
			t.Errorf("unset optional column leaked into the contract: %q", c)
		}
	}
}

// schemaOf is a stand-in for what the adapter reports: table -> column set.
func schemaOf(t map[string][]string) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for table, cols := range t {
		set := map[string]struct{}{}
		for _, c := range cols {
			set[c] = struct{}{}
		}
		out[table] = set
	}
	return out
}

func manifestWithMountains(t *testing.T) Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(minimalManifest + "mountains:\n  layer: mountains\n"))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

func TestVerifyAgainst_AcceptsAMatchingSchema(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	err = m.VerifyAgainst(schemaOf(map[string][]string{
		"places":       {"place", "name", "admin_id", "country_iso"},
		"admin_levels": {"admin_level", "name", "parent_id", "country_iso"},
	}))
	if err != nil {
		t.Fatalf("matching schema rejected: %v", err)
	}
}

func TestVerifyAgainst_RejectsAMissingTable(t *testing.T) {
	m := manifestWithMountains(t)
	// The mountains layer is declared but absent from the file — the typo case,
	// as opposed to an omitted optional block, which is legitimate.
	err := m.VerifyAgainst(schemaOf(map[string][]string{
		"places":       {"place", "name", "admin_id", "country_iso"},
		"admin_levels": {"admin_level", "name", "parent_id", "country_iso"},
	}))
	if err == nil {
		t.Fatal("a declared-but-absent table must be rejected")
	}
	if !strings.Contains(err.Error(), `table "mountains"`) {
		t.Errorf("the error should name the table, got: %v", err)
	}
}

func TestVerifyAgainst_RejectsAMissingColumn(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	err = m.VerifyAgainst(schemaOf(map[string][]string{
		"places":       {"place", "name", "admin_id"}, // country_iso missing
		"admin_levels": {"admin_level", "name", "parent_id", "country_iso"},
	}))
	if err == nil {
		t.Fatal("a mapped-but-absent column must be rejected")
	}
	if !strings.Contains(err.Error(), `no column "country_iso"`) {
		t.Errorf("the error should name the column, got: %v", err)
	}
}

func TestVerifyAgainst_ReportsEveryViolationAtOnce(t *testing.T) {
	// A schema change tends to break several mappings together; one startup should
	// list all of them rather than making the operator fix and restart per problem.
	m := manifestWithMountains(t)
	err := m.VerifyAgainst(schemaOf(map[string][]string{
		"places":       {"place"}, // name, admin_id, country_iso missing
		"admin_levels": {"admin_level", "name", "parent_id", "country_iso"},
	}))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`no column "name"`, `no column "admin_id"`, `table "mountains"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestVerifyAgainst_IgnoresUndeclaredOptionalLayers(t *testing.T) {
	// No mountains block: the file having no mountains table must still pass, since
	// that tolerance is what lets a manifest ship ahead of its dataset.
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := m.VerifyAgainst(schemaOf(map[string][]string{
		"places":       {"place", "name", "admin_id", "country_iso"},
		"admin_levels": {"admin_level", "name", "parent_id", "country_iso"},
	})); err != nil {
		t.Fatalf("an undeclared optional layer must not be required: %v", err)
	}
}
