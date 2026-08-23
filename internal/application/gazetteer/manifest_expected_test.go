package gazetteer

import (
	"slices"
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
			continue // VerifyContract skips these
		}
		if !slices.Contains([]string{"place", "name", "admin_id", "country_iso"}, c) {
			t.Errorf("unset optional column leaked into the contract: %q", c)
		}
	}
}
