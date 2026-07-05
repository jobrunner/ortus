package gazetteer

import (
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
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

func TestParseManifestLicense(t *testing.T) {
	const y = validManifest + `
license:
  name: "ODbL-1.0"
  url: "https://opendatacommons.org/licenses/odbl/1-0/"
  attribution: "© OpenStreetMap contributors (ODbL 1.0); GeoNames (CC BY 4.0)"
`
	m, err := ParseManifest([]byte(y))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := domain.License{
		Name:        "ODbL-1.0",
		URL:         "https://opendatacommons.org/licenses/odbl/1-0/",
		Attribution: "© OpenStreetMap contributors (ODbL 1.0); GeoNames (CC BY 4.0)",
	}
	if m.License != want {
		t.Errorf("license = %+v, want %+v", m.License, want)
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
	// Each of these, when removed, must fail validation (the query paths rely on them).
	for line, want := range map[string]string{
		"  rank_column: place\n":   "rank_column",
		"  admin_fk: admin_id\n":   "admin_fk",
		"  parent_fk: parent_id\n": "parent_fk",
	} {
		y := strings.Replace(validManifest, line, "", 1)
		if _, err := ParseManifest([]byte(y)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("removing %q: err = %v, want mention of %q", strings.TrimSpace(line), err, want)
		}
	}

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
equivalent_levels:
  country:
    description: "Sovereign state (admin_level 2)"
  state:
    description: "First-order subdivision"
  municipality:
    description: "Municipality / commune (basic local government unit)"
countries:
  DE:
    levels:
      2:
        name: "Deutschland"
        equivalent: country
      4:
        name: "Bundesland"
        equivalent: state
      8:
        name: "Stadt / Gemeinde"
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
		want   LevelMeaning
		wantOK bool
	}{
		{"DE", 4, LevelMeaning{Equivalent: "state", Description: "First-order subdivision", LocalTerm: "Bundesland"}, true},
		{"DE", 8, LevelMeaning{Equivalent: "municipality", Description: "Municipality / commune (basic local government unit)", LocalTerm: "Stadt / Gemeinde"}, true},
		{"AT", 2, LevelMeaning{Equivalent: "country", Description: "Sovereign state (admin_level 2)"}, true},
		{"DE", 6, LevelMeaning{}, false}, // level not defined
		{"FR", 2, LevelMeaning{}, false}, // country not present
	}
	for _, tc := range cases {
		got, ok := r.Resolve(tc.iso, tc.level)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("Resolve(%q,%d) = %+v,%v; want %+v,%v", tc.iso, tc.level, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestParseNameSources(t *testing.T) {
	const y = `
version: 1
sources:
  latin-osm:
    short: "OSM name (already Latin)"
    long: "Taken verbatim from the OSM name tag."
    standard: null
  translit-el-843:
    short: "Greek ELOT 743"
    long: "Romanized from Greek using ELOT 743."
    standard: "ELOT 743 / UN / ISO 843"
`
	r, err := ParseNameSources([]byte(y))
	if err != nil {
		t.Fatalf("ParseNameSources: %v", err)
	}
	ns, ok := r.Resolve("translit-el-843")
	if !ok || ns.Standard != "ELOT 743 / UN / ISO 843" || ns.Short != "Greek ELOT 743" {
		t.Errorf("Resolve(translit-el-843) = %+v, ok=%v", ns, ok)
	}
	if _, ok := r.Resolve("nope"); ok {
		t.Error("unknown code should return ok=false")
	}
}

func TestParseLevelReferenceInvalidYAML(t *testing.T) {
	if _, err := ParseLevelReference([]byte("countries: [bad")); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

// TestParseLevelReferenceUndefinedEquivalent asserts a level whose equivalent is
// not declared in equivalent_levels fails at parse time rather than silently
// yielding an empty description in every response.
func TestParseLevelReferenceUndefinedEquivalent(t *testing.T) {
	const y = `
version: 1
equivalent_levels:
  country:
    description: "Sovereign state"
countries:
  DE:
    levels:
      4:
        name: "Bundesland"
        equivalent: state
`
	_, err := ParseLevelReference([]byte(y))
	if err == nil {
		t.Fatal("expected error for equivalent not defined in equivalent_levels")
	}
	if !strings.Contains(err.Error(), "state") || !strings.Contains(err.Error(), "equivalent_levels") {
		t.Errorf("error = %q, want it to name the missing equivalent + equivalent_levels", err)
	}
}
