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
	Name    string
	Class   PlaceClass
	AdminID int64 // FK → the admin unit containing the place (0 = unknown)
	At      Coordinate
}

// AdminUnit is one level of a resolved administrative hierarchy, already enriched
// with its semantic meaning (Equivalent) from the admin-level sidecar reference.
type AdminUnit struct {
	Level      int    // OSM admin_level
	Name       string // native admin unit name
	Equivalent string // sidecar meaning: country | state | … | municipality
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
}

// BearingPolicy holds the tunable knobs of bearing selection. It is data, not
// branches: Reach maps each class to the radius within which it is an acceptable
// anchor, so adding a class is a map entry rather than a new code path.
type BearingPolicy struct {
	Reach           map[PlaceClass]float64 // km per class
	PreferNearestKM float64                // a town-or-larger anchor within this radius wins outright (0 = off)
	ConstraintTier  string                 // semantic admin tier anchors must share (e.g. "state")
	InsideLabelKM   float64                // below this, label as "in/bei {name}" without a bearing
	CompassPoints   int                    // 8 or 16
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
