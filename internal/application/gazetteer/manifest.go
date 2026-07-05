package gazetteer

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/jobrunner/ortus/internal/domain"
)

// defaultConstraintTier is used when the manifest omits bearing_constraint_tier.
const defaultConstraintTier = "state"

// manifestYAML is the on-disk shape of ortus-gazetteer.yaml (§4 of the plan).
type manifestYAML struct {
	Places struct {
		Layer            string `yaml:"layer"`
		NameColumn       string `yaml:"name_column"`
		NameNativeColumn string `yaml:"name_native_column"`
		NameSourceColumn string `yaml:"name_source_column"`
		RankColumn       string `yaml:"rank_column"`
		AdminFK          string `yaml:"admin_fk"`
		CountryColumn    string `yaml:"country_column"`
	} `yaml:"places"`
	Admin struct {
		Layer          string `yaml:"layer"`
		LevelColumn    string `yaml:"level_column"`
		NameColumn     string `yaml:"name_column"`
		ParentFK       string `yaml:"parent_fk"`
		CountryColumn  string `yaml:"country_column"`
		ConstraintTier string `yaml:"bearing_constraint_tier"`
	} `yaml:"admin"`
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
	m := Manifest{
		PlacesLayer:      y.Places.Layer,
		RankColumn:       y.Places.RankColumn,
		NameColumn:       y.Places.NameColumn,
		AdminFKColumn:    y.Places.AdminFK,
		AdminLayer:       y.Admin.Layer,
		LevelColumn:      y.Admin.LevelColumn,
		AdminNameColumn:  y.Admin.NameColumn,
		ParentFKColumn:   y.Admin.ParentFK,
		CountryColumn:    country,
		NameNativeColumn: y.Places.NameNativeColumn,
		NameSourceColumn: y.Places.NameSourceColumn,
		ConstraintTier:   tier,
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
