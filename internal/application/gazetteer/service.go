// Package gazetteer provides the reverse-geocoding and bearing ("Peilung")
// service. It orchestrates the spatial-index output port and (from M3) a pure
// salience strategy. The service is inert until it is enabled and wired with a
// spatial index, so the composition root can leave it unwired with no effect on
// the generic query path.
package gazetteer

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ErrDisabled is returned by an inert gazetteer service — one that has not been
// enabled or has not been wired with a spatial index.
var ErrDisabled = fmt.Errorf("gazetteer disabled: %w", domain.ErrUnavailable)

// Manifest declares which layer/column of the gazetteer GeoPackage plays which
// role, so ortus stays schema-agnostic about the concrete names. It mirrors the
// ortus-gazetteer.yaml contract (§4 of the plan).
type Manifest struct {
	// places layer
	PlacesLayer   string // e.g. "places"
	RankColumn    string // e.g. "place" (village|town|city)
	NameColumn    string // e.g. "name"
	AdminFKColumn string // e.g. "admin_id" → admin_levels.fid

	// places prominence signals for CompositeSalience — all optional (empty when the
	// package predates enrichment; CompositeSalience then scores by class prior, not
	// a switch to RankedSalience).
	PopulationColumn string // e.g. "population" (integer)
	CapitalColumn    string // e.g. "capital" (OSM capital= rank of the seat)
	NotabilityColumn string // e.g. "wikidata" (QID; presence = notable)

	// admin_levels layer
	AdminLayer      string // e.g. "admin_levels"
	LevelColumn     string // e.g. "admin_level"
	AdminNameColumn string // e.g. "name"
	ParentFKColumn  string // e.g. "parent_id" (walked by ResolveChain)

	// shared (same column names on both layers)
	CountryColumn    string // e.g. "country_iso"
	NameNativeColumn string // e.g. "name_native" (original-script name)
	NameSourceColumn string // e.g. "name_source" (romanization/provenance code)

	// bearing
	ConstraintTier string // semantic tier anchors must share, e.g. "state"

	// License is the dataset-wide license/attribution for the gazetteer data
	// (e.g. OSM/ODbL + GeoNames + Natural Earth), surfaced in responses so a
	// client has the attribution it must display. Empty when unset.
	License domain.License
}

// LevelMeaning is what an (ISO, admin_level) pair means, from the sidecar: the
// generic Equivalent class, its Description, and the country-specific LocalTerm.
type LevelMeaning struct {
	Equivalent  string // country | state | … | municipality
	Description string // generic description of Equivalent
	LocalTerm   string // country-specific term (e.g. "Landkreis / Kreis / kreisfreie Stadt")
}

// LevelResolver maps a raw OSM admin level to its meaning, per country, from the
// sidecar reference. It is an injected seam so the service does not depend on the
// sidecar file format.
type LevelResolver interface {
	// Resolve returns the meaning for an (ISO 3166-1 alpha-2, admin_level) pair;
	// ok is false when unmapped.
	Resolve(countryISO string, level int) (m LevelMeaning, ok bool)
}

// noopLevelResolver leaves every level unenriched. It is the safe default when
// no sidecar is wired, so Locate still returns the raw hierarchy.
type noopLevelResolver struct{}

func (noopLevelResolver) Resolve(string, int) (LevelMeaning, bool) { return LevelMeaning{}, false }

// Service is the GazetteerService: reverse geocoding (Locate) and bearing.
type Service struct {
	index       output.SpatialIndex
	manifest    Manifest
	levels      LevelResolver
	nameSources NameSourceResolver // optional; nil ⇒ names carry only their code
	salience    SalienceStrategy
	enabled     bool
}

// SetNameSources wires the optional name-source resolver so resolved name
// provenance (short/long/standard) is attached to each name. Without it, names
// still carry their raw provenance code.
func (s *Service) SetNameSources(r NameSourceResolver) { s.nameSources = r }

// resolveNameSource turns a raw provenance code into a NameProvenance, enriched
// from the manifest when one is wired and the code is known.
func (s *Service) resolveNameSource(code string) domain.NameProvenance {
	if code == "" {
		return domain.NameProvenance{}
	}
	if s.nameSources != nil {
		if ns, ok := s.nameSources.Resolve(code); ok {
			return ns
		}
	}
	return domain.NameProvenance{Code: code}
}

// NewService creates a gazetteer service. It is inert unless enabled is true and
// a spatial index is supplied; an inert service returns ErrDisabled from its
// query methods. A nil LevelResolver leaves admin levels unenriched; a nil
// Strategy uses the rank-based default.
func NewService(index output.SpatialIndex, manifest Manifest, levels LevelResolver, strategy SalienceStrategy, enabled bool) *Service {
	if levels == nil {
		levels = noopLevelResolver{}
	}
	if strategy == nil {
		strategy = RankedSalience{}
	}
	return &Service{index: index, manifest: manifest, levels: levels, salience: strategy, enabled: enabled}
}

// Locate reverse-geocodes a coordinate to its administrative hierarchy. It uses a
// point-in-polygon query against the admin layer — which returns every polygon
// containing the point across levels — then orders them most-local-first and
// enriches each with its semantic meaning from the level resolver.
func (s *Service) Locate(ctx context.Context, p domain.Coordinate) (*domain.Locality, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	features, err := s.index.PointInPolygon(ctx, s.manifest.AdminLayer, p)
	if err != nil {
		return nil, err
	}
	if len(features) == 0 {
		return nil, fmt.Errorf("locate (%v): %w", p, domain.ErrNotFound)
	}

	var (
		chain      []domain.AdminUnit
		countryISO string
		deepest    = -1 // level of the most-local unit whose country_iso is set
	)
	for i := range features {
		f := &features[i]
		iso := f.GetStringProperty(s.manifest.CountryColumn)
		// admin_level is stored as text ("8"); coverage fills carry a non-numeric
		// or empty value and are skipped from the ordered hierarchy.
		level, err := strconv.Atoi(f.GetStringProperty(s.manifest.LevelColumn))
		if err != nil {
			continue
		}
		// The locality's country is the most-local unit's — deterministic (not
		// dependent on PiP row order) and correct where a coarse polygon carries a
		// different code than the local units (e.g. the IL/PS L2 boundary quirk).
		if level > deepest && iso != "" {
			deepest, countryISO = level, iso
		}
		// Resolve each level's meaning by its own country+level.
		meaning, _ := s.levels.Resolve(iso, level)
		chain = append(chain, domain.AdminUnit{
			Level:          level,
			Name:           f.GetStringProperty(s.manifest.AdminNameColumn),
			NameNative:     f.GetStringProperty(s.manifest.NameNativeColumn),
			NameSource:     s.resolveNameSource(f.GetStringProperty(s.manifest.NameSourceColumn)),
			Equivalent:     meaning.Equivalent,
			LocalTerm:      meaning.LocalTerm,
			EquivalentDesc: meaning.Description,
		})
	}
	// Most-local first (highest admin_level → country last), with a Name tie-break
	// so equal-level units order deterministically rather than by PiP row order.
	sort.SliceStable(chain, func(i, j int) bool {
		if chain[i].Level != chain[j].Level {
			return chain[i].Level > chain[j].Level
		}
		return chain[i].Name < chain[j].Name
	})

	return &domain.Locality{CountryISO: countryISO, Chain: chain}, nil
}

// requireWGS84 rejects coordinates that are not WGS84 (EPSG:4326). The gazetteer
// dataset is 4326 and the service does not reproject, so a non-4326 coordinate
// would be misread as lon/lat and yield nonsense. SRID 0 is treated as unset
// (the coordinate constructors default to WGS84).
func requireWGS84(p domain.Coordinate) error {
	if p.SRID != 0 && p.SRID != domain.SRIDWGS84 {
		return fmt.Errorf("gazetteer: coordinate SRID %d unsupported, expected WGS84 (%d): %w",
			p.SRID, domain.SRIDWGS84, domain.ErrUnsupportedProjection)
	}
	return nil
}

// ready reports whether the service has been enabled and wired with a spatial
// index; until both hold it is inert and returns ErrDisabled.
func (s *Service) ready() error {
	if !s.enabled || s.index == nil {
		return ErrDisabled
	}
	return nil
}

// Compile-time assertion that the service satisfies its driving port.
var _ input.Gazetteer = (*Service)(nil)
