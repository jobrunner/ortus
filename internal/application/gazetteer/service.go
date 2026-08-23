// Package gazetteer provides the reverse-geocoding and bearing ("Peilung")
// service. It orchestrates the spatial-index output port and (from M3) a pure
// salience strategy. The service is inert until it is enabled and wired with a
// spatial index, so the composition root can leave it unwired with no effect on
// the generic query path.
package gazetteer

import (
	"context"
	"errors"
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

	// Identity of the built package this manifest ships with (optional; empty in
	// packages predating the fields). Surfaced by the sources endpoint so a
	// deployed dataset can be told apart from any other build.
	DatasetVersion string // e.g. "0.2.0"
	Built          string // e.g. "2026-08-23"

	// admin_levels layer
	AdminLayer      string // e.g. "admin_levels"
	LevelColumn     string // e.g. "admin_level"
	AdminNameColumn string // e.g. "name"
	ParentFKColumn  string // e.g. "parent_id" (walked by ResolveChain)

	// islands layer (optional; empty ⇒ no island lookup, so the response's islands
	// block is null). The name-native/name-source columns are the shared ones below.
	IslandsLayer      string // e.g. "islands"
	IslandsNameColumn string // e.g. "name"

	// mountains layer (optional; empty ⇒ no mountain lookup, so the response's
	// mountains block is null). Smallest containing feature wins PER landform. The
	// name-native/name-source columns are the shared ones below.
	MountainsLayer           string // e.g. "mountains"
	MountainsNameColumn      string // e.g. "name"
	MountainsLandformColumn  string // e.g. "landform" (range|mountain)
	MountainsElevationColumn string // e.g. "ele" (summit height; NULL on range rows)
	MountainsAreaColumn      string // e.g. "area_km2" (drives "smallest wins")

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

// ElevationMeta is the constant metadata attached to every elevation result:
// the vertical datum, the accuracy figures, and the surface-model note. It is
// config-provided (dataset-wide), distinct from the per-point Meters and the DEM
// license (which comes from the bound sampler).
type ElevationMeta struct {
	VerticalDatum string  // e.g. "EGM2008"
	AccuracyM     float64 // dataset vertical accuracy constant (LE90), used when no per-point value
	AccuracyBasis string  // basis for the constant, e.g. "GLO-30 LE90 (absolute)"
	// PerPointAccuracyBasis describes the accuracy when the sampler supplies a
	// per-point value (e.g. "Copernicus HEM (per-pixel 1σ)").
	PerPointAccuracyBasis string
	HorizontalM           float64 // horizontal accuracy (LE90)
	SurfaceModel          string  // e.g. "DSM"
}

// Service is the GazetteerService: reverse geocoding (Locate) and bearing.
type Service struct {
	index       output.SpatialIndex
	manifest    Manifest
	levels      LevelResolver
	nameSources NameSourceResolver // optional; nil ⇒ names carry only their code
	salience    SalienceStrategy
	enabled     bool

	elevation output.ElevationSampler // optional; nil ⇒ no elevation in responses
	elevMeta  ElevationMeta

	builtUp      output.BuiltUpSampler // optional; nil ⇒ "in {X}" uses distance alone (no built-up gate)
	builtUpMinM2 float64               // min built-up value for a point to count as "in" a settlement
}

// SetNameSources wires the optional name-source resolver so resolved name
// provenance (short/long/standard) is attached to each name. Without it, names
// still carry their raw provenance code.
func (s *Service) SetNameSources(r NameSourceResolver) { s.nameSources = r }

// SetElevationSampler wires the optional elevation sampler and its constant
// metadata. Until it is called, Elevation returns (nil, nil) so the response
// simply omits the elevation block.
func (s *Service) SetElevationSampler(sampler output.ElevationSampler, meta ElevationMeta) {
	s.elevation = sampler
	s.elevMeta = meta
}

// SetBuiltUpSampler wires the optional built-up sampler that gates the "in {X}"
// decision: a point within a settlement's radius must also sit on built-up fabric
// (>= minM2) to be labeled "in". Until it is called, "in {X}" uses distance alone.
func (s *Service) SetBuiltUpSampler(sampler output.BuiltUpSampler, minM2 float64) {
	s.builtUp = sampler
	s.builtUpMinM2 = minM2
}

// builtUpAllows reports whether the "in {X}" path may run at p:
//   - no sampler wired            → true  (feature off; distance alone decides "in")
//   - no coverage at p (ok=false) → true  (outside the bundle extent: documented
//     graceful degradation, fall back to the distance-only decision)
//   - sampler error               → false (fail CLOSED: the raster is unhealthy, so
//     rather than risk an unverified "in <place>" — the very thing this gate exists
//     to suppress — degrade to prope/directional; a point genuinely in a settlement
//     is at worst reported as "prope", never as a wrong "in")
//   - covered                     → built-up value meets the configured minimum
//
// The fail-closed error branch is deliberate: a false "in <village>" (a point on
// fields within a place's radius) is exactly the confusion the gate removes, so a
// sampler failure must not silently re-open that path.
func (s *Service) builtUpAllows(ctx context.Context, p domain.Coordinate) bool {
	if s.builtUp == nil {
		return true
	}
	v, ok, err := s.builtUp.BuiltUpAt(ctx, p)
	if err != nil {
		return false
	}
	if !ok {
		return true
	}
	return v >= s.builtUpMinM2
}

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

// Islands returns the named island(s) whose polygon contains the query point,
// via a point-in-polygon query against the optional islands layer. It returns
// (nil, nil) when no islands layer is configured (the dataset predates the
// feature) or when the point lies on no island; adapters then render a null
// islands block. Island lookup is independent of admin coverage: a point on a
// small island outside any admin polygon still resolves its island name.
func (s *Service) Islands(ctx context.Context, p domain.Coordinate) ([]domain.Island, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	if s.manifest.IslandsLayer == "" {
		return nil, nil // islands not configured — omit from the response
	}
	features, err := s.index.PointInPolygon(ctx, s.manifest.IslandsLayer, p)
	if err != nil {
		// Treat a missing layer like "no result" (as Locate/Elevation do) so an
		// islands mapping that outruns the deployed dataset degrades gracefully.
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var islands []domain.Island
	seen := make(map[string]bool)
	for i := range features {
		f := &features[i]
		name := f.GetStringProperty(s.manifest.IslandsNameColumn)
		// Skip unnamed coverage fills, and report each island once per distinct name:
		// a point can lie on several NESTED islands (islet within an archipelago), but
		// those have different names. Two same-name polygons are always the same island
		// duplicated in the data (e.g. two OSM objects for La Palma), never a meaningful
		// second result — the property-level PiP dedup misses them because they differ
		// by osm_id.
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		islands = append(islands, domain.Island{
			Name:       name,
			NameNative: f.GetStringProperty(s.manifest.NameNativeColumn),
			NameSource: s.resolveNameSource(f.GetStringProperty(s.manifest.NameSourceColumn)),
		})
	}
	// Deterministic order (PiP row order is unspecified); smallest name first so
	// nested islands read naturally and tests stay stable.
	sort.SliceStable(islands, func(i, j int) bool { return islands[i].Name < islands[j].Name })
	return islands, nil
}

// Mountain landform vocabulary (closed set, per the mountains layer contract).
const (
	landformRange    = "range"    // a mountain range (Gebirgszug) polygon
	landformMountain = "mountain" // a single-mountain DEM-derived territory
)

// Capabilities reports which optional blocks this deployment can answer, so an
// adapter can render "not part of this dataset" distinctly from "no result here".
//
// Availability is read from what was actually wired, not from config intent: a
// declared layer is only usable because VerifyContract confirmed it exists at
// startup, and elevation/exposure depend on a sampler being set rather than on a
// path being configured. An inert service (feature disabled) reports everything
// false.
func (s *Service) Capabilities() domain.GazetteerCapabilities {
	if s.ready() != nil {
		return domain.GazetteerCapabilities{}
	}
	return domain.GazetteerCapabilities{
		Islands:   s.manifest.IslandsLayer != "",
		Mountains: s.manifest.MountainsLayer != "",
		Elevation: s.elevation != nil,
		// Exposure is derived from the same DEM, so it stands or falls with it.
		Exposure: s.elevation != nil,
	}
}

// Mountains returns the smallest (most specific) containing mountain range AND
// single-mountain territory, selected independently per landform, via a
// point-in-polygon query against the optional mountains layer. It returns
// (nil, nil) when no mountains layer is configured (the dataset predates the
// feature) or when the point is on no mountain feature; adapters then render a
// null mountains block. Like islands, a missing layer degrades to (nil, nil) so
// a manifest that outruns the deployed dataset does not error.
func (s *Service) Mountains(ctx context.Context, p domain.Coordinate) (*domain.MountainResult, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	if s.manifest.MountainsLayer == "" {
		return nil, nil // mountains not configured — omit from the response
	}
	features, err := s.index.PointInPolygon(ctx, s.manifest.MountainsLayer, p)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	rng := s.smallestByLandform(features, landformRange)
	mtn := s.smallestByLandform(features, landformMountain)
	if rng == nil && mtn == nil {
		return nil, nil
	}
	return &domain.MountainResult{Range: rng, Mountain: mtn}, nil
}

// smallestByLandform returns the most specific (smallest-area) named feature of
// the given landform among the point-in-polygon results, or nil when none match.
// A known area always beats an unknown one; equal areas break by name so nested
// features order deterministically regardless of PiP row order.
func (s *Service) smallestByLandform(features []domain.Feature, landform string) *domain.Mountain {
	var (
		best     *domain.Feature
		bestArea float64
		bestName string
	)
	for i := range features {
		f := &features[i]
		if f.GetStringProperty(s.manifest.MountainsLandformColumn) != landform {
			continue
		}
		name := f.GetStringProperty(s.manifest.MountainsNameColumn)
		if name == "" {
			continue // coverage fills / unnamed polygons carry no name
		}
		area := f.GetFloatProperty(s.manifest.MountainsAreaColumn)
		if best == nil || moreSpecific(area, name, bestArea, bestName) {
			best, bestArea, bestName = f, area, name
		}
	}
	if best == nil {
		return nil
	}
	return s.mountainFrom(best, landform == landformMountain)
}

// mountainFrom builds a domain.Mountain from a feature. Elevation is attached only
// for a single-mountain (withElevation) and only when the column is actually set
// (range rows carry a NULL ele).
func (s *Service) mountainFrom(f *domain.Feature, withElevation bool) *domain.Mountain {
	m := &domain.Mountain{
		Name:       f.GetStringProperty(s.manifest.MountainsNameColumn),
		NameNative: f.GetStringProperty(s.manifest.NameNativeColumn),
		NameSource: s.resolveNameSource(f.GetStringProperty(s.manifest.NameSourceColumn)),
	}
	if withElevation {
		if v, ok := f.GetProperty(s.manifest.MountainsElevationColumn); ok && v != nil {
			m.ElevationM = f.GetFloatProperty(s.manifest.MountainsElevationColumn)
			m.HasElevation = true
		}
	}
	return m
}

// moreSpecific reports whether candidate (aArea, aName) is more specific than the
// current best (bArea, bName): a smaller known area wins; an unknown area (<= 0)
// never displaces a known one; equal areas break by name for determinism.
func moreSpecific(aArea float64, aName string, bArea float64, bName string) bool {
	aKnown, bKnown := aArea > 0, bArea > 0
	if aKnown != bKnown {
		return aKnown
	}
	if aKnown && aArea != bArea {
		return aArea < bArea
	}
	return aName < bName
}

// Elevation samples the height above sea level at the query point. It returns
// (nil, nil) when no elevation sampler is wired, so the handler omits the block
// rather than erroring. A point with no DEM coverage yields SeaLevel=true with
// Meters=0 (the ocean/no-tile convention), not an error.
func (s *Service) Elevation(ctx context.Context, p domain.Coordinate) (*domain.Elevation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	if s.elevation == nil {
		return nil, nil // feature not wired — omit from the response
	}
	r, ok, err := s.elevation.ElevationAt(ctx, p)
	if err != nil {
		// A missing source/layer (e.g. the DEM bundle mid hot-reload, or removed)
		// must not fail the whole gazetteer response — omit elevation, like
		// Locate/Bearing treat ErrNotFound. Real I/O/decode errors still propagate.
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	elev := &domain.Elevation{
		Meters:        r.Meters,
		SeaLevel:      !ok,
		HorizontalM:   s.elevMeta.HorizontalM,
		VerticalDatum: s.elevMeta.VerticalDatum,
		SurfaceModel:  s.elevMeta.SurfaceModel,
		License:       s.elevation.License(),
	}
	switch {
	case !ok:
		// Sea-level convention: 0 m is a convention, not a measurement, so it
		// carries no absolute accuracy figure.
		elev.AccuracyBasis = "sea-level convention"
	case r.HasAccuracy:
		// Per-point accuracy (e.g. HEM) when the sampler supplies it.
		elev.AccuracyM = r.AccuracyM
		elev.AccuracyBasis = s.elevMeta.PerPointAccuracyBasis
	default:
		// Fall back to the dataset accuracy constant.
		elev.AccuracyM = s.elevMeta.AccuracyM
		elev.AccuracyBasis = s.elevMeta.AccuracyBasis
	}
	return elev, nil
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
