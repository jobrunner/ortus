package domain

// PlaceClass is the OSM settlement rank used to gauge a place's findability as a
// bearing anchor. The values are ordered by salience: a City is more findable
// from afar than a Town, which is more findable than a Village.
type PlaceClass int

// Place classes, ordered by salience (higher = findable from farther away).
const (
	ClassUnknown PlaceClass = iota
	ClassVillage
	ClassTown
	ClassCity
)

// OSM `place` tokens for the settlement classes.
const (
	placeVillage = "village"
	placeTown    = "town"
	placeCity    = "city"
	placeUnknown = "unknown"
)

// String returns the OSM `place` token for the class.
func (c PlaceClass) String() string {
	switch c {
	case ClassVillage:
		return placeVillage
	case ClassTown:
		return placeTown
	case ClassCity:
		return placeCity
	default:
		return placeUnknown
	}
}

// ParsePlaceClass maps an OSM `place` token to a PlaceClass. ok is false for any
// token outside the village/town/city vocabulary this dataset uses.
func ParsePlaceClass(s string) (class PlaceClass, ok bool) {
	switch s {
	case placeVillage:
		return ClassVillage, true
	case placeTown:
		return ClassTown, true
	case placeCity:
		return ClassCity, true
	default:
		return ClassUnknown, false
	}
}

// Place is a named settlement anchor — a feature of the points layer.
type Place struct {
	Name       string         // romanized (always-Latin) display name
	NameNative string         // original-script name (empty if already Latin)
	NameSource NameProvenance // how Name was romanized/sourced
	Class      PlaceClass
	AdminID    int64  // FK → the admin unit containing the place (0 = unknown)
	CountryISO string // ISO 3166-1 alpha-2 of the place (bearing anchors must share the query's country)
	At         Coordinate
	// Prominence signals for CompositeSalience (from the enriched osm-admin-places
	// package; all zero/empty when the package predates enrichment, so selection
	// degrades gracefully to rank-only).
	Population int64  // OSM population; <= 0 means unknown (fall back to class prior)
	Capital    string // OSM `capital` rank of the seat (2=country … 8=municipality, or "yes"); "" if none
	Wikidata   string // OSM `wikidata` QID; presence is a notability proxy
}

// AdminUnit is one level of a resolved administrative hierarchy, enriched with its
// semantic meaning from the admin-level sidecar reference.
type AdminUnit struct {
	Level      int            // OSM admin_level
	Name       string         // romanized admin unit name
	NameNative string         // original-script name (empty if already Latin)
	NameSource NameProvenance // how Name was romanized/sourced
	Equivalent string         // sidecar meaning: country | state | … | municipality
	// LocalTerm is the country-specific term for this level (e.g. "Landkreis"),
	// and EquivalentDesc the generic description of Equivalent — both from the
	// sidecar, so a client learns what the level means in that country.
	LocalTerm      string
	EquivalentDesc string
}

// NameProvenance describes how a romanized name was produced, from the name-source
// manifest that ships beside the dataset (for citation/provenance transparency).
// Short/Long/Standard are empty when the code is unmapped or no manifest is wired.
type NameProvenance struct {
	Code     string // the code stored on the record (e.g. "translit-el-843")
	Short    string // short label
	Long     string // full description
	Standard string // citation standard, if any (e.g. "ELOT 743 / UN / ISO 843")
}

// Locality is the administrative hierarchy (levels 2–8) containing a coordinate —
// the result of a reverse-geocode.
type Locality struct {
	CountryISO string
	Chain      []AdminUnit // most-local first
}

// Fix is a bearing result: a reference place plus the direction and distance from
// it to the queried point, with a ready-to-render label ("4 km E Würzburg").
type Fix struct {
	Reference  Place
	DistanceKM float64
	Azimuth    float64 // degrees, 0=N, 90=E (reference→point)
	Compass    string
	Label      string
	// Inside is true when the query point lies within the reference's own
	// administrative unit — i.e. we are IN that place ("in Ochsenfurt"), not merely
	// near it ("prope Ochsenfurt"). Decided by containment, not distance, so it holds
	// even far from a large place's center node. Azimuth/Compass are unset when Inside.
	Inside bool
}

// BearingPolicy holds the tunable knobs of bearing selection. It is data, not
// branches: Reach maps each class to the radius within which it is an acceptable
// anchor, so adding a class is a map entry rather than a new code path.
type BearingPolicy struct {
	Reach           map[PlaceClass]float64 // km per class
	PreferNearestKM float64                // a town-or-larger anchor within this radius wins outright (0 = off)
	ConstraintTier  string                 // semantic admin tier anchors must share (e.g. "state")
	InsideLabelKM   float64                // below this, label as "in/prope {name}" without a bearing
	CompassPoints   int                    // 8 or 16
	// CandidateRadiusKM, when > 0, makes candidate gathering use this one flat radius
	// for every class instead of the per-class Reach. CompositeSalience sets it: it
	// wants a wide candidate pool and lets its distance decay (not a hard per-class
	// cap) shape the outcome. Zero keeps the per-class Reach behavior (RankedSalience).
	CandidateRadiusKM float64
}

// Candidate-gather limits: how many nearest places per class candidate-gathering
// fetches before the salience strategy scores them.
const (
	// rankedCandidateLimit is enough for class-then-distance: a small k > 1 leaves
	// room to skip the nearest few that fail the boundary constraint.
	rankedCandidateLimit = 10
	// compositeCandidateLimit covers the wide CandidateRadiusKM pool. A prominent
	// city that wins on score can be far beyond the nearest few, so gather enough
	// that scoring sees every city/town in range (there are only tens of those
	// within ~120 km); far villages truncated by this cap never win the score.
	compositeCandidateLimit = 250
)

// CandidateLimit is how many nearest places per class candidate-gathering fetches.
// CompositeSalience (CandidateRadiusKM > 0) scores a wide pool where the winner may
// lie well beyond the nearest few, so it fetches many; RankedSalience needs only the
// nearest few.
func (p BearingPolicy) CandidateLimit() int {
	if p.CandidateRadiusKM > 0 {
		return compositeCandidateLimit
	}
	return rankedCandidateLimit
}

// DefaultBearingPolicy returns the recommended defaults for the osm-admin-places
// dataset: a city reaches far, a village only when very close; anchors are
// constrained to the same state-tier unit; an 8-point compass rose.
func DefaultBearingPolicy() BearingPolicy {
	return BearingPolicy{
		Reach: map[PlaceClass]float64{
			ClassVillage: 5,
			ClassTown:    18,
			ClassCity:    60,
		},
		PreferNearestKM: 5.0,
		ConstraintTier:  "state",
		InsideLabelKM:   1.0,
		CompassPoints:   8,
	}
}

// ReachKM returns the reach radius for a class, or 0 when the class has no entry
// (a class with no reach never qualifies as an anchor).
func (p BearingPolicy) ReachKM(c PlaceClass) float64 {
	return p.Reach[c]
}

// GatherRadiusKM is the radius candidate gathering uses for a class: the flat
// CandidateRadiusKM when set (CompositeSalience), else the per-class Reach
// (RankedSalience). A class with neither configured yields 0 and is skipped.
func (p BearingPolicy) GatherRadiusKM(c PlaceClass) float64 {
	if p.CandidateRadiusKM > 0 {
		return p.CandidateRadiusKM
	}
	return p.Reach[c]
}

// OrDefault returns the policy when it is configured (non-nil Reach), else the
// built-in DefaultBearingPolicy. It lets adapters accept a zero-value policy and
// fall back safely without repeating the nil check.
func (p BearingPolicy) OrDefault() BearingPolicy {
	if p.Reach != nil {
		return p
	}
	return DefaultBearingPolicy()
}
