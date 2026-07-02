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

	// admin_levels layer
	AdminLayer      string // e.g. "admin_levels"
	LevelColumn     string // e.g. "admin_level"
	AdminNameColumn string // e.g. "name"
	ParentFKColumn  string // e.g. "parent_id" (walked by ResolveChain)

	// shared
	CountryColumn string // e.g. "country_iso" (present on both layers)

	// bearing
	ConstraintTier string // semantic tier anchors must share, e.g. "state"
}

// LevelResolver maps a raw OSM admin level to its semantic meaning, per country,
// from the sidecar reference (admin_levels_west_palearctic.yaml). It is an
// injected seam so the service does not depend on the sidecar file format.
type LevelResolver interface {
	// Resolve returns the semantic equivalent (country|state|…|municipality) for
	// an (ISO 3166-1 alpha-2, admin_level) pair; ok is false when unmapped.
	Resolve(countryISO string, level int) (equivalent string, ok bool)
}

// noopLevelResolver leaves every level unenriched. It is the safe default when
// no sidecar is wired, so Locate still returns the raw hierarchy.
type noopLevelResolver struct{}

func (noopLevelResolver) Resolve(string, int) (string, bool) { return "", false }

// Service is the GazetteerService: reverse geocoding (Locate) and bearing.
type Service struct {
	index    output.SpatialIndex
	manifest Manifest
	levels   LevelResolver
	salience SalienceStrategy
	enabled  bool
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
		equivalent, _ := s.levels.Resolve(iso, level)
		chain = append(chain, domain.AdminUnit{
			Level:      level,
			Name:       f.GetStringProperty(s.manifest.AdminNameColumn),
			Equivalent: equivalent,
		})
	}
	// Most-local first (highest admin_level → country last).
	sort.SliceStable(chain, func(i, j int) bool { return chain[i].Level > chain[j].Level })

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
