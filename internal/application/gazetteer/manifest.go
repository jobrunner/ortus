package gazetteer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jobrunner/ortus/internal/domain"
)

// featureIDColumn is the GeoPackage primary key the adapter's SQL hardcodes.
const featureIDColumn = "fid"

// defaultConstraintTier is used when the manifest omits bearing_constraint_tier.
const defaultConstraintTier = "state"

// orDefault returns v when non-empty, else def — for optional manifest columns
// that fall back to their documented schema name.
func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// manifestYAML is the on-disk shape of ortus-gazetteer.yaml (§4 of the plan).
type manifestYAML struct {
	// Identify the BUILT PACKAGE. Purely informational for the query paths, but a
	// deployed .gpkg is otherwise indistinguishable from any other build, so this
	// is what lets an operator answer "which vintage is live?" without shell access
	// to the machine. Absent in packages built before the field existed.
	DatasetVersion string `yaml:"dataset_version"`
	Built          string `yaml:"built"`

	Places struct {
		Layer            string `yaml:"layer"`
		NameColumn       string `yaml:"name_column"`
		NameNativeColumn string `yaml:"name_native_column"`
		NameSourceColumn string `yaml:"name_source_column"`
		RankColumn       string `yaml:"rank_column"`
		AdminFK          string `yaml:"admin_fk"`
		CountryColumn    string `yaml:"country_column"`
		PopulationColumn string `yaml:"population_column"`
		CapitalColumn    string `yaml:"capital_column"`
		NotabilityColumn string `yaml:"notability_column"`
	} `yaml:"places"`
	Admin struct {
		Layer          string `yaml:"layer"`
		LevelColumn    string `yaml:"level_column"`
		NameColumn     string `yaml:"name_column"`
		ParentFK       string `yaml:"parent_fk"`
		CountryColumn  string `yaml:"country_column"`
		ConstraintTier string `yaml:"bearing_constraint_tier"`
	} `yaml:"admin"`
	// Islands is optional: an added layer of named island polygons. When omitted,
	// no island lookup runs and the response's islands field is null. name_column
	// defaults to the admin layer's when unset (the dataset shares column names
	// across layers).
	Islands struct {
		Layer      string `yaml:"layer"`
		NameColumn string `yaml:"name_column"`
	} `yaml:"islands"`
	// Mountains is optional: an added layer of mountain-range / single-mountain
	// polygons. When omitted, no mountain lookup runs and the response's mountains
	// field is null. name/landform/elevation/area columns default to the documented
	// schema names when unset (the dataset shares column conventions across layers).
	Mountains struct {
		Layer           string `yaml:"layer"`
		NameColumn      string `yaml:"name_column"`
		LandformColumn  string `yaml:"landform_column"`
		ElevationColumn string `yaml:"elevation_column"`
		AreaColumn      string `yaml:"area_column"`
	} `yaml:"mountains"`
	License struct {
		Name        string `yaml:"name"`
		URL         string `yaml:"url"`
		Attribution string `yaml:"attribution"`
	} `yaml:"license"`
}

// ParseManifest parses the gazetteer manifest YAML into a Manifest. It fails when
// a required layer/column mapping is missing, so a malformed manifest is caught
// at load time rather than surfacing as empty queries later.
func ParseManifest(data []byte) (Manifest, error) {
	var y manifestYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return Manifest{}, fmt.Errorf("parse gazetteer manifest: %w", err)
	}
	tier := y.Admin.ConstraintTier
	if tier == "" {
		tier = defaultConstraintTier
	}
	// country_column is shared; the admin layer's value wins, falling back to the
	// places layer's when only that is set.
	country := y.Admin.CountryColumn
	if country == "" {
		country = y.Places.CountryColumn
	}
	// islands.name_column defaults to the admin name column (shared convention)
	// when an islands layer is declared without its own name column.
	islandsName := y.Islands.NameColumn
	if y.Islands.Layer != "" && islandsName == "" {
		islandsName = y.Admin.NameColumn
	}
	// mountains column roles default to the documented schema names — but only when
	// a mountains layer is declared, so an absent block leaves every field empty
	// (like islands) and the service skips the lookup. name defaults to the
	// mountains schema column "name" (not admin.name_column) so overriding the admin
	// name column can't silently point the mountains lookup at the wrong column.
	var mtnName, mtnLandform, mtnElevation, mtnArea string
	if y.Mountains.Layer != "" {
		mtnName = orDefault(y.Mountains.NameColumn, "name")
		mtnLandform = orDefault(y.Mountains.LandformColumn, "landform")
		mtnElevation = orDefault(y.Mountains.ElevationColumn, "ele")
		mtnArea = orDefault(y.Mountains.AreaColumn, "area_km2")
	}
	m := Manifest{
		DatasetVersion:           y.DatasetVersion,
		Built:                    y.Built,
		PlacesLayer:              y.Places.Layer,
		RankColumn:               y.Places.RankColumn,
		NameColumn:               y.Places.NameColumn,
		AdminFKColumn:            y.Places.AdminFK,
		AdminLayer:               y.Admin.Layer,
		LevelColumn:              y.Admin.LevelColumn,
		AdminNameColumn:          y.Admin.NameColumn,
		ParentFKColumn:           y.Admin.ParentFK,
		IslandsLayer:             y.Islands.Layer,
		IslandsNameColumn:        islandsName,
		MountainsLayer:           y.Mountains.Layer,
		MountainsNameColumn:      mtnName,
		MountainsLandformColumn:  mtnLandform,
		MountainsElevationColumn: mtnElevation,
		MountainsAreaColumn:      mtnArea,
		CountryColumn:            country,
		NameNativeColumn:         y.Places.NameNativeColumn,
		NameSourceColumn:         y.Places.NameSourceColumn,
		PopulationColumn:         y.Places.PopulationColumn,
		CapitalColumn:            y.Places.CapitalColumn,
		NotabilityColumn:         y.Places.NotabilityColumn,
		ConstraintTier:           tier,
		License: domain.License{
			Name:        y.License.Name,
			URL:         y.License.URL,
			Attribution: y.License.Attribution,
		},
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// validate checks that the mappings the query paths depend on are present.
func (m Manifest) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"places.layer", m.PlacesLayer},
		{"places.rank_column", m.RankColumn},
		{"places.name_column", m.NameColumn},
		{"places.admin_fk", m.AdminFKColumn},
		{"admin.layer", m.AdminLayer},
		{"admin.level_column", m.LevelColumn},
		{"admin.name_column", m.AdminNameColumn},
		{"admin.parent_fk", m.ParentFKColumn},
		{"country_column", m.CountryColumn},
	}
	var missing []string
	for _, r := range required {
		if r.value == "" {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("gazetteer manifest: missing required field(s): %v", missing)
	}
	return nil
}

// ExpectedTables maps each layer the manifest declares to the columns it maps
// onto that layer, for a startup check against the actual GeoPackage (see
// GazetteerIndex.VerifyContract).
//
// Only declared layers appear: islands and mountains are optional, and an absent
// block must stay absent here so a manifest that predates those layers keeps
// working. Optional COLUMNS within a declared layer are included when set —
// declaring one is a promise that it exists, and a promise worth checking. The
// column list per layer is what the query paths actually read, so it is the same
// set whose absence would otherwise surface as an empty or wrong answer.
func (m Manifest) ExpectedTables() map[string][]string {
	want := map[string][]string{}
	// Roles are APPENDED per table, never assigned: nothing stops a manifest from
	// pointing two roles at one table (a merged dataset, or simply a copy-paste),
	// and assigning would then silently drop the first role's columns from the
	// check — turning a mismatch back into the deferred query-time failure this
	// exists to prevent.
	add := func(layer string, cols ...string) {
		if layer == "" {
			return
		}
		want[layer] = append(want[layer], cols...)
	}
	add(m.PlacesLayer,
		m.RankColumn, m.NameColumn, m.AdminFKColumn, m.CountryColumn,
		m.NameNativeColumn, m.NameSourceColumn,
		m.PopulationColumn, m.CapitalColumn, m.NotabilityColumn)
	// featureIDColumn is required on the admin layer on top of the mapped roles:
	// ResolveChain walks the parent chain with `fid` written literally into its SQL
	// (WHERE fid = ?, JOIN ON a.fid = chain.parent_id), so it is a hard dependency
	// of the adapter rather than a configurable role. A GeoPackage using a
	// different feature-id column would otherwise pass startup and fail the
	// bearing's chain lookup at query time.
	add(m.AdminLayer,
		m.LevelColumn, m.AdminNameColumn, m.ParentFKColumn, m.CountryColumn,
		m.NameNativeColumn, m.NameSourceColumn, featureIDColumn)
	add(m.IslandsLayer, m.IslandsNameColumn, m.NameNativeColumn, m.NameSourceColumn)
	add(m.MountainsLayer,
		m.MountainsNameColumn, m.MountainsLandformColumn,
		m.MountainsElevationColumn, m.MountainsAreaColumn,
		m.NameNativeColumn, m.NameSourceColumn)
	return want
}

// VerifyAgainst checks the manifest's mappings against a GeoPackage's actual
// schema, as reported by the adapter (table name -> column set; an absent table
// reports an empty set).
//
// ParseManifest can only see the manifest, so it catches a missing mapping but
// never a wrong one: `layer: does_not_exist` parses fine. The mistake then
// surfaces as empty results, because a missing layer is deliberately treated as
// "no result" so a manifest may outrun the dataset it ships with. That tolerance
// is right for an absent optional layer and wrong for a typo, and the two are
// indistinguishable at query time. Checking once at startup separates them.
//
// Every violation is collected rather than failing on the first, since a schema
// change usually breaks several mappings at once and one startup should report
// all of them. Unset optional mappings arrive as "" and mean "not declared", not
// "a column with an empty name".
func (m Manifest) VerifyAgainst(schema map[string]map[string]struct{}, spatial map[string]string) error {
	var problems []string
	want := m.ExpectedTables()
	for _, table := range slices.Sorted(maps.Keys(want)) {
		cols := schema[table]
		if len(cols) == 0 {
			problems = append(problems, fmt.Sprintf("table %q declared in the manifest does not exist", table))
			continue
		}
		// Columns alone do not make a queryable layer: every read resolves its
		// geometry column via gpkg_geometry_columns first. A plain SQLite table
		// with the right names would pass a columns-only check and then fail
		// every query with "layer not found".
		geom, registered := spatial[table]
		if !registered {
			problems = append(problems, fmt.Sprintf(
				"table %q exists but is not registered as a GeoPackage feature layer "+
					"(no gpkg_geometry_columns row), so every query on it would fail", table))
			continue
		}
		// A registration can be stale: a row naming a column the table no longer
		// has still looks registered, and every query would then select a column
		// that is not there.
		if _, ok := cols[geom]; !ok {
			problems = append(problems, fmt.Sprintf(
				"table %q is registered with geometry column %q, which the table does not have",
				table, geom))
		}
		for _, c := range want[table] {
			if _, ok := cols[c]; c != "" && !ok {
				problems = append(problems, fmt.Sprintf("table %q has no column %q", table, c))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("gazetteer manifest does not match the GeoPackage: %s",
			strings.Join(problems, "; "))
	}
	return nil
}

// levelRefYAML is the on-disk shape of the admin-level sidecar
// (admin_levels_west_palearctic.yaml): the generic equivalent_levels descriptions
// plus per-country levels → { name (local term), equivalent }.
type levelRefYAML struct {
	Version          int `yaml:"version"`
	EquivalentLevels map[string]struct {
		Description string `yaml:"description"`
	} `yaml:"equivalent_levels"`
	Countries map[string]struct {
		Levels map[int]struct {
			Name       string `yaml:"name"`
			Equivalent string `yaml:"equivalent"`
		} `yaml:"levels"`
	} `yaml:"countries"`
}

// levelReference is a LevelResolver backed by the parsed sidecar.
type levelReference struct {
	byCountry map[string]map[int]LevelMeaning
}

// Resolve implements LevelResolver.
func (r *levelReference) Resolve(countryISO string, level int) (LevelMeaning, bool) {
	levels, ok := r.byCountry[countryISO]
	if !ok {
		return LevelMeaning{}, false
	}
	m, ok := levels[level]
	return m, ok
}

// ParseLevelReference parses the admin-level sidecar YAML into a LevelResolver,
// pre-joining each (country, level) with the generic equivalent description.
func ParseLevelReference(data []byte) (LevelResolver, error) {
	var y levelRefYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parse admin-level reference: %w", err)
	}
	ref := &levelReference{byCountry: make(map[string]map[int]LevelMeaning, len(y.Countries))}
	for iso, c := range y.Countries {
		levels := make(map[int]LevelMeaning, len(c.Levels))
		for level, def := range c.Levels {
			if def.Equivalent == "" {
				continue
			}
			// A level's equivalent must resolve to an equivalent_levels entry, else
			// Equivalent would be set while Description stayed silently empty. Fail
			// at load so a malformed sidecar is caught here, not in every response.
			eq, ok := y.EquivalentLevels[def.Equivalent]
			if !ok {
				return nil, fmt.Errorf("parse admin-level reference: country %s level %d references equivalent %q not defined in equivalent_levels", iso, level, def.Equivalent)
			}
			levels[level] = LevelMeaning{
				Equivalent:  def.Equivalent,
				Description: eq.Description,
				LocalTerm:   def.Name,
			}
		}
		if len(levels) > 0 {
			ref.byCountry[iso] = levels
		}
	}
	return ref, nil
}

// nameSourceRefYAML is the on-disk shape of name_source_manifest.yaml.
type nameSourceRefYAML struct {
	Version int `yaml:"version"`
	Sources map[string]struct {
		Short    string `yaml:"short"`
		Long     string `yaml:"long"`
		Standard string `yaml:"standard"`
	} `yaml:"sources"`
}

// nameSourceReference resolves a name_source code to its description.
type nameSourceReference struct {
	byCode map[string]domain.NameProvenance
}

// Resolve returns the NameSource for a code; ok is false when unknown.
func (r *nameSourceReference) Resolve(code string) (domain.NameProvenance, bool) {
	ns, ok := r.byCode[code]
	return ns, ok
}

// NameSourceResolver maps a name_source code to its human description + citation
// standard, from the name-source manifest that ships beside the dataset.
type NameSourceResolver interface {
	Resolve(code string) (domain.NameProvenance, bool)
}

// ParseNameSources parses name_source_manifest.yaml into a NameSourceResolver.
func ParseNameSources(data []byte) (NameSourceResolver, error) {
	var y nameSourceRefYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parse name-source manifest: %w", err)
	}
	ref := &nameSourceReference{byCode: make(map[string]domain.NameProvenance, len(y.Sources))}
	for code, s := range y.Sources {
		ref.byCode[code] = domain.NameProvenance{Code: code, Short: s.Short, Long: s.Long, Standard: s.Standard}
	}
	return ref, nil
}
