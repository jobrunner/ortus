package gazetteer

import (
	"strings"
	"testing"
)

const validManifest = `
places:
  layer: places
  name_column: name
  rank_column: place
  admin_fk: admin_id
  country_column: country_iso
admin:
  layer: admin_levels
  level_column: admin_level
  name_column: name
  parent_fk: parent_id
  country_column: country_iso
  bearing_constraint_tier: state
`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := Manifest{
		PlacesLayer: "places", RankColumn: "place", NameColumn: "name", AdminFKColumn: "admin_id",
		AdminLayer: "admin_levels", LevelColumn: "admin_level", AdminNameColumn: "name", ParentFKColumn: "parent_id",
		CountryColumn: "country_iso", ConstraintTier: "state",
	}
	if m != want {
		t.Errorf("manifest = %+v\nwant %+v", m, want)
	}
}

func TestParseManifestDefaultsTier(t *testing.T) {
	// bearing_constraint_tier omitted → defaults to "state".
	y := strings.Replace(validManifest, "  bearing_constraint_tier: state\n", "", 1)
	m, err := ParseManifest([]byte(y))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.ConstraintTier != "state" {
		t.Errorf("ConstraintTier = %q, want state (default)", m.ConstraintTier)
	}
}

func TestParseManifestMissingRequired(t *testing.T) {
	y := strings.Replace(validManifest, "  rank_column: place\n", "", 1)
	_, err := ParseManifest([]byte(y))
	if err == nil || !strings.Contains(err.Error(), "rank_column") {
		t.Errorf("err = %v, want a missing rank_column error", err)
	}
}

func TestParseManifestInvalidYAML(t *testing.T) {
	if _, err := ParseManifest([]byte("places: [unterminated")); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

const validLevelRef = `
version: 1
countries:
  DE:
    levels:
      2:
        equivalent: country
      4:
        equivalent: state
      8:
        equivalent: municipality
  AT:
    levels:
      2:
        equivalent: country
`

func TestParseLevelReference(t *testing.T) {
	r, err := ParseLevelReference([]byte(validLevelRef))
	if err != nil {
		t.Fatalf("ParseLevelReference: %v", err)
	}
	cases := []struct {
		iso    string
		level  int
		want   string
		wantOK bool
	}{
		{"DE", 4, "state", true},
		{"DE", 8, "municipality", true},
		{"AT", 2, "country", true},
		{"DE", 6, "", false}, // level not defined
		{"FR", 2, "", false}, // country not present
	}
	for _, tc := range cases {
		got, ok := r.Resolve(tc.iso, tc.level)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("Resolve(%q,%d) = %q,%v; want %q,%v", tc.iso, tc.level, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestParseLevelReferenceInvalidYAML(t *testing.T) {
	if _, err := ParseLevelReference([]byte("countries: [bad")); err == nil {
		t.Error("expected error for malformed YAML")
	}
}
