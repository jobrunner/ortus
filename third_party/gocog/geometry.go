package gocog

import (
	"math"

	"github.com/paulmach/orb"
)

// PolygonFromBounds creates a polygon from a bounding box
func PolygonFromBounds(bound orb.Bound) orb.Polygon {
	if bound.IsEmpty() {
		return orb.Polygon{}
	}

	ring := orb.Ring{
		{bound.Min[0], bound.Min[1]}, // Bottom-left
		{bound.Max[0], bound.Min[1]}, // Bottom-right
		{bound.Max[0], bound.Max[1]}, // Top-right
		{bound.Min[0], bound.Max[1]}, // Top-left
		{bound.Min[0], bound.Min[1]}, // Close ring
	}

	return orb.Polygon{ring}
}

// PointFromPixel converts pixel coordinates to a geographic point
func (c *COG) PointFromPixel(x, y int, overview int) orb.Point {
	overviewIndex := overview // overview 0 = main image (IFD 0), overview 1+ = overviews (IFD 1+)
	if overviewIndex < 0 || overviewIndex >= len(c.geoTIFFs) {
		return orb.Point{}
	}

	gtr := c.geoTIFFs[overviewIndex]
	geoX, geoY := gtr.pixelToGeo(float64(x), float64(y))
	return orb.Point{geoX, geoY}
}

// PixelFromPoint converts a geographic point to pixel coordinates
func (c *COG) PixelFromPoint(point orb.Point, overview int) (int, int) {
	overviewIndex := overview // overview 0 = main image (IFD 0), overview 1+ = overviews (IFD 1+)
	if overviewIndex < 0 || overviewIndex >= len(c.metadata) {
		return 0, 0
	}

	meta := c.metadata[overviewIndex]
	gtr := c.geoTIFFs[overviewIndex]

	imgBounds := gtr.Bounds()

	// Calculate pixel coordinates
	geoWidth := imgBounds.Max[0] - imgBounds.Min[0]
	geoHeight := imgBounds.Max[1] - imgBounds.Min[1]

	if geoWidth == 0 || geoHeight == 0 {
		return 0, 0
	}

	// math.Floor, not a plain int() cast: a cast truncates TOWARD ZERO, so a point
	// just west of Min[0] (or just north of Max[1]) computes a small NEGATIVE pixel
	// and truncates to 0 — landing inside the image. Callers bounds-check with
	// `px < 0 || px >= Width`, which then cannot reject it, so a sub-pixel sliver
	// along the west and north edges reads as covered while the east and south
	// edges reject correctly. Flooring makes the value genuinely negative there and
	// the existing checks work as intended.
	pixelX := int(math.Floor((point[0] - imgBounds.Min[0]) / geoWidth * float64(meta.Width)))
	pixelY := int(math.Floor((imgBounds.Max[1] - point[1]) / geoHeight * float64(meta.Height))) // Y is inverted

	return pixelX, pixelY
}

// GetImagePolygon returns the image bounds as a polygon
func (c *COG) GetImagePolygon(overview int) orb.Polygon {
	overviewIndex := overview // overview 0 = main image (IFD 0), overview 1+ = overviews (IFD 1+)
	if overviewIndex < 0 || overviewIndex >= len(c.geoTIFFs) {
		return orb.Polygon{}
	}

	bounds := c.geoTIFFs[overviewIndex].Bounds()
	return PolygonFromBounds(bounds)
}

// GetCornerPoints returns the four corner points of the image
func (c *COG) GetCornerPoints(overview int) [4]orb.Point {
	overviewIndex := overview // overview 0 = main image (IFD 0), overview 1+ = overviews (IFD 1+)
	if overviewIndex < 0 || overviewIndex >= len(c.geoTIFFs) {
		return [4]orb.Point{}
	}

	gtr := c.geoTIFFs[overviewIndex]
	meta := c.metadata[overviewIndex]

	// Get corners
	topLeftX, topLeftY := gtr.pixelToGeo(0, 0)
	topRightX, topRightY := gtr.pixelToGeo(float64(meta.Width), 0)
	bottomLeftX, bottomLeftY := gtr.pixelToGeo(0, float64(meta.Height))
	bottomRightX, bottomRightY := gtr.pixelToGeo(float64(meta.Width), float64(meta.Height))

	return [4]orb.Point{
		{topLeftX, topLeftY},         // Top-left
		{topRightX, topRightY},       // Top-right
		{bottomRightX, bottomRightY}, // Bottom-right
		{bottomLeftX, bottomLeftY},   // Bottom-left
	}
}
