package domain

import (
	"path/filepath"
	"strings"
)

// Source file extensions ortus recognizes.
const (
	extGeoPackage   = ".gpkg" // vector source
	extRasterBundle = ".zip"  // raster bundle
)

// supportedSourceExtensions are the file extensions ortus recognizes as
// spatial sources: GeoPackages and raster bundles. This mirrors the adapters'
// SpatialSource.Supports(); it lives here so the storage listing and the file
// watcher share one definition instead of each hard-coding the set. (A fully
// provider-driven check is a possible later refinement.)
var supportedSourceExtensions = []string{extGeoPackage, extRasterBundle}

// DeriveSourceID derives a source id from a file path or object key — the
// filename stem (basename without extension). This is the single source of
// truth used by the registry and every adapter, so a path always maps to the
// same id regardless of who derives it.
func DeriveSourceID(path string) string {
	base := filepath.Base(path)
	if base == "" || base == "." {
		return ""
	}
	ext := filepath.Ext(base)
	// Basename that is only an extension (e.g. ".gpkg") has no stem to strip.
	if ext == "" || len(base) == len(ext) {
		return base
	}
	return strings.TrimSuffix(base, ext)
}

// IsSupportedSourceFile reports whether a filename/key looks like a spatial
// source ortus can load (by extension). Used by the storage listing and the
// file watcher to filter candidate files.
func IsSupportedSourceFile(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range supportedSourceExtensions {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}
