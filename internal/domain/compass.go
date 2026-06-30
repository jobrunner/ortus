package domain

import (
	"fmt"
	"math"
)

// compass8 and compass16 are the clockwise point labels starting at North.
var (
	compass8  = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	compass16 = []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
)

// Compass quantizes an azimuth in degrees (0=N, 90=E, clockwise) to a compass
// point label. points selects the rose resolution: 16 for the finer rose, any
// other value (typically 8) for the 8-point rose. Azimuths outside [0,360) are
// normalized first, so negative and over-360 inputs work.
func Compass(azimuthDeg float64, points int) string {
	rose := compass8
	if points == 16 {
		rose = compass16
	}
	n := len(rose)
	step := 360.0 / float64(n)
	az := math.Mod(azimuthDeg, 360)
	if az < 0 {
		az += 360
	}
	idx := int(math.Round(az/step)) % n
	return rose[idx]
}

// RoundDistanceKM rounds a distance for display: to 0.5 km under 10 km (where the
// extra precision reads as meaningful), to whole km beyond.
func RoundDistanceKM(km float64) float64 {
	if km < 10 {
		return math.Round(km*2) / 2
	}
	return math.Round(km)
}

// FormatBearingLabel renders a bearing label, e.g. "4 km E Würzburg". The distance
// is expected pre-rounded (see RoundDistanceKM): a whole number prints without a
// decimal, a half with one.
func FormatBearingLabel(km float64, compass, name string) string {
	return fmt.Sprintf("%s km %s %s", formatKM(km), compass, name)
}

func formatKM(km float64) string {
	if km == math.Trunc(km) {
		return fmt.Sprintf("%.0f", km)
	}
	return fmt.Sprintf("%.1f", km)
}
