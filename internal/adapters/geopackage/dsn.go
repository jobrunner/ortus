package geopackage

import (
	"fmt"
	"strings"
)

// The SQLite DSN shared by the vector Repository and the GazetteerIndex. Split
// out of spatialindex.go (like knn.go) to keep that file at its complexity
// baseline.

// dsnFor builds the SQLite DSN from the given options. Values are normalized
// against fixed whitelists before being concatenated, so an invalid or hostile
// config value cannot break DB open or smuggle extra DSN parameters via '&'.
// Defaults to a private cache (each connection gets its own — allows true
// concurrent reads, unlike the legacy shared cache).
func dsnFor(path string, opts Options) string {
	params := []string{"cache=" + normalizeCacheMode(opts.CacheMode)}
	if opts.BusyTimeoutMS > 0 {
		params = append(params, fmt.Sprintf("_busy_timeout=%d", opts.BusyTimeoutMS))
	}
	// A read-only open leaves the journal mode alone: switching it (to WAL)
	// rewrites the file header.
	if opts.ReadOnly {
		params = append(params, "mode=ro")
	} else if jm := normalizeJournalMode(opts.JournalMode); jm != "" {
		params = append(params, "_journal_mode="+jm)
	}
	return fmt.Sprintf("file:%s?%s", path, strings.Join(params, "&"))
}
