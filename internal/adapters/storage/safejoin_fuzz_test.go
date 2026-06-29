package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSafeJoin asserts the security invariant: for any base and any key,
// safeJoin either errors or returns a path that stays within base. A key from
// a storage listing must never be able to escape the storage root.
func FuzzSafeJoin(f *testing.F) {
	for _, k := range []string{
		"", ".", "..", "../x", "../../etc/passwd", "/etc/passwd",
		"a/../../b", "regions.gpkg", "a/b/c.gpkg", "x\x00y", "./x",
		strings.Repeat("../", 64) + "x",
	} {
		f.Add("/srv/data", k)
	}
	f.Fuzz(func(t *testing.T, base, key string) {
		got, err := safeJoin(base, key)
		if err != nil {
			return // rejected — fine
		}
		clean := filepath.Clean(base)
		if got != clean && !strings.HasPrefix(got, clean+string(filepath.Separator)) {
			t.Errorf("safeJoin(%q, %q) = %q escapes base %q", base, key, got, clean)
		}
	})
}
