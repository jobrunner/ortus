package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// mgrsPattern matches an MGRS reference with all whitespace already
// stripped: a UTM zone (1-2 digits), a latitude band letter (C-X, I/O
// excluded — the polar bands A/B/Y/Z are out of scope), a 100 km grid
// square (2 letters, I/O excluded — the row letter's actual range, A-V,
// is checked separately for a clearer error message), and a digit run
// that must split evenly between easting and northing.
var mgrsPattern = regexp.MustCompile(`^(\d{1,2})([C-HJ-NP-X])([A-HJ-NP-Z]{2})(\d+)$`)

// mgrsColumnAlphabet and mgrsRowAlphabet are the MGRS 100 km grid square
// letter sequences with I and O excluded (visual ambiguity with 1 and 0).
const (
	mgrsColumnAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ" // 24 letters
	mgrsRowAlphabet    = "ABCDEFGHJKLMNPQRSTUV"     // 20 letters
)

// mgrsMinNorthing gives each latitude band's minimum northing, in its own
// hemisphere's false-northing convention (south: equator = 10,000,000;
// north: equator = 0). It resolves which 2,000,000 m northing cycle a
// 100 km grid square row belongs to — the row letter alone repeats every
// 2,000 km, so the band tells us which repetition applies.
var mgrsMinNorthing = map[byte]float64{
	'C': 1100000, 'D': 2000000, 'E': 2800000, 'F': 3700000, 'G': 4600000,
	'H': 5500000, 'J': 6400000, 'K': 7300000, 'L': 8200000, 'M': 9100000,
	'N': 0, 'P': 800000, 'Q': 1700000, 'R': 2600000, 'S': 3500000,
	'T': 4400000, 'U': 5300000, 'V': 6200000, 'W': 7000000, 'X': 7900000,
}

// Hemisphere identifies which hemisphere a UTM zone/coordinate belongs to.
type Hemisphere int

// Hemisphere values.
const (
	HemisphereNorth Hemisphere = iota
	HemisphereSouth
)

// MGRSCoordinate is an MGRS reference resolved to its UTM position.
type MGRSCoordinate struct {
	Zone       int
	Hemisphere Hemisphere
	Easting    float64
	Northing   float64
	Precision  int // digits per axis in the source string, 1-5 (10 km down to 1 m)
}

// ParseMGRS parses an MGRS string of variable precision (1-5 digits per
// axis) into its UTM zone, hemisphere, and easting/northing. Whitespace
// between the zone/band, the 100 km grid square, and the digit groups is
// optional and ignored — "32U NA 01234 56789", "32UNA0123456789", and
// "32U NA0123456789" all parse identically. Low-precision input resolves
// to the south-west origin of the corresponding grid cell, not its
// center — callers relying on exact placement should account for that
// cell size.
//
// Only the UTM latitude bands C-X (+-80 deg) are supported; the polar
// bands A, B, Y, Z (UPS projection) are out of scope and rejected.
func ParseMGRS(s string) (MGRSCoordinate, error) {
	stripped := strings.ToUpper(strings.Join(strings.Fields(s), ""))
	m := mgrsPattern.FindStringSubmatch(stripped)
	if m == nil {
		return MGRSCoordinate{}, fmt.Errorf("%w: %q is not a valid MGRS reference", ErrInvalidMGRS, s)
	}

	zone, err := strconv.Atoi(m[1])
	if err != nil || zone < 1 || zone > 60 {
		return MGRSCoordinate{}, fmt.Errorf("%w: zone %q out of range 1-60", ErrInvalidMGRS, m[1])
	}
	band := m[2][0]
	square := m[3]
	digits := m[4]

	if len(digits)%2 != 0 {
		return MGRSCoordinate{}, fmt.Errorf("%w: easting and northing must have the same number of digits, got %q", ErrInvalidMGRS, digits)
	}
	precision := len(digits) / 2
	if precision < 1 || precision > 5 {
		return MGRSCoordinate{}, fmt.Errorf("%w: precision must be 1-5 digits per axis, got %d", ErrInvalidMGRS, precision)
	}

	eastingBase, northingBase, err := mgrs100kOrigin(zone, square)
	if err != nil {
		return MGRSCoordinate{}, err
	}

	scale := math.Pow(10, float64(5-precision))
	eastingDigits, _ := strconv.Atoi(digits[:precision])  // digits are \d+, Atoi cannot fail
	northingDigits, _ := strconv.Atoi(digits[precision:]) // same

	northing := northingBase + float64(northingDigits)*scale
	minNorthing := mgrsMinNorthing[band] // band is constrained to C-X by mgrsPattern
	for northing < minNorthing {
		northing += 2000000
	}

	hemisphere := HemisphereNorth
	if band < 'N' {
		hemisphere = HemisphereSouth
	}

	return MGRSCoordinate{
		Zone:       zone,
		Hemisphere: hemisphere,
		Easting:    eastingBase + float64(eastingDigits)*scale,
		Northing:   northing,
		Precision:  precision,
	}, nil
}

// mgrs100kOrigin resolves a 100 km grid square designator to the
// south-west corner of that square, in UTM meters for the given zone.
func mgrs100kOrigin(zone int, square string) (easting, northing float64, err error) {
	colOffset, err := mgrsLetterOffset(mgrsColumnAlphabet, mgrsColumnOrigin(zone), square[0])
	if err != nil {
		return 0, 0, fmt.Errorf("%w: grid square column %q: %v", ErrInvalidMGRS, string(square[0]), err)
	}
	rowOffset, err := mgrsLetterOffset(mgrsRowAlphabet, mgrsRowOrigin(zone), square[1])
	if err != nil {
		return 0, 0, fmt.Errorf("%w: grid square row %q: %v", ErrInvalidMGRS, string(square[1]), err)
	}
	return float64(colOffset+1) * 100000, float64(rowOffset) * 100000, nil
}

// mgrsColumnOrigin returns the 100 km column letter that starts the
// letter cycle for a zone. The cycle is A, J, S and repeats every 3 zones.
func mgrsColumnOrigin(zone int) byte {
	return []byte{'A', 'J', 'S'}[(zone-1)%3]
}

// mgrsRowOrigin returns the 100 km row letter that starts the letter
// cycle for a zone. It alternates between A (odd zones) and F (even
// zones) so the 2,000 km row cycle lines up correctly across zone
// boundaries.
func mgrsRowOrigin(zone int) byte {
	if zone%2 == 1 {
		return 'A'
	}
	return 'F'
}

// mgrsLetterOffset returns how many positions letter is past origin in
// alphabet, wrapping around the end of the alphabet.
func mgrsLetterOffset(alphabet string, origin, letter byte) (int, error) {
	li := strings.IndexByte(alphabet, letter)
	if li < 0 {
		return 0, fmt.Errorf("letter %q not valid in this position", string(letter))
	}
	oi := strings.IndexByte(alphabet, origin)
	return (li - oi + len(alphabet)) % len(alphabet), nil
}

// UTMSRIDForZone returns the EPSG code for the WGS84 UTM zone/hemisphere
// pair an MGRS coordinate decodes into (32601-32660 north, 32701-32760
// south).
func UTMSRIDForZone(zone int, hemisphere Hemisphere) (int, error) {
	if zone < 1 || zone > 60 {
		return 0, fmt.Errorf("%w: UTM zone %d out of range 1-60", ErrInvalidMGRS, zone)
	}
	if hemisphere == HemisphereSouth {
		return 32700 + zone, nil
	}
	return 32600 + zone, nil
}
