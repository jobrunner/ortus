package gazetteer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// defaultConstraintTier is used when the manifest omits bearing_constraint_tier.
const defaultConstraintTier = "state"

// manifestYAML is the on-disk shape of ortus-gazetteer.yaml (§4 of the plan).
type manifestYAML struct {
	Places struct {
		Layer         string `yaml:"layer"`
		NameColumn    string `yaml:"name_column"`
		RankColumn    string `yaml:"rank_column"`
		AdminFK       string `yaml:"admin_fk"`
		CountryColumn string `yaml:"country_column"`
	} `yaml:"places"`
	Admin struct {
		Layer          string `yaml:"layer"`
		LevelColumn    string `yaml:"level_column"`
		NameColumn     string `yaml:"name_column"`
		CountryColumn  string `yaml:"country_column"`
		ConstraintTier string `yaml:"bearing_constraint_tier"`
	} `yaml:"admin"`
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
		PlacesLayer:     y.Places.Layer,
		RankColumn:      y.Places.RankColumn,
		NameColumn:      y.Places.NameColumn,
		AdminFKColumn:   y.Places.AdminFK,
		AdminLayer:      y.Admin.Layer,
		LevelColumn:     y.Admin.LevelColumn,
		AdminNameColumn: y.Admin.NameColumn,
		CountryColumn:   country,
		ConstraintTier:  tier,
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
		{"admin.layer", m.AdminLayer},
		{"admin.level_column", m.LevelColumn},
		{"admin.name_column", m.AdminNameColumn},
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
// (admin_levels_west_palearctic.yaml): countries → levels → { equivalent }.
type levelRefYAML struct {
	Version   int `yaml:"version"`
	Countries map[string]struct {
		Levels map[int]struct {
			Equivalent string `yaml:"equivalent"`
		} `yaml:"levels"`
	} `yaml:"countries"`
}

// levelReference is a LevelResolver backed by the parsed sidecar: ISO → level →
// semantic equivalent.
type levelReference struct {
	equiv map[string]map[int]string
}

// Resolve implements LevelResolver.
func (r *levelReference) Resolve(countryISO string, level int) (string, bool) {
	levels, ok := r.equiv[countryISO]
	if !ok {
		return "", false
	}
	eq, ok := levels[level]
	return eq, ok
}

// ParseLevelReference parses the admin-level sidecar YAML into a LevelResolver.
func ParseLevelReference(data []byte) (LevelResolver, error) {
	var y levelRefYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parse admin-level reference: %w", err)
	}
	ref := &levelReference{equiv: make(map[string]map[int]string, len(y.Countries))}
	for iso, c := range y.Countries {
		levels := make(map[int]string, len(c.Levels))
		for level, def := range c.Levels {
			if def.Equivalent != "" {
				levels[level] = def.Equivalent
			}
		}
		if len(levels) > 0 {
			ref.equiv[iso] = levels
		}
	}
	return ref, nil
}
