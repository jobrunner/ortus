package storage

import "strings"

// isSupportedSourceFile reports whether a storage object is a source ortus can
// load: a GeoPackage (.gpkg) or a raster bundle (.zip).
func isSupportedSourceFile(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".gpkg") || strings.HasSuffix(n, ".zip")
}
