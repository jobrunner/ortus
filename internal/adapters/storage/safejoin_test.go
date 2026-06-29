package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	base := filepath.Clean("/srv/data")
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"simple file", "regions.gpkg", false},
		{"nested", "eu/de/regions.gpkg", false},
		{"dot-prefixed", "./regions.gpkg", false},
		{"empty key", "", true},
		{"parent escape", "../secret", true},
		{"absolute", "/etc/passwd", true},
		{"sneaky traversal", "a/../../etc/passwd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeJoin(base, tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("safeJoin(%q) = %q, want error", tt.key, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("safeJoin(%q) unexpected error: %v", tt.key, err)
			}
			if got != base && !strings.HasPrefix(got, base+string(filepath.Separator)) {
				t.Errorf("safeJoin(%q) = %q, escapes base %q", tt.key, got, base)
			}
		})
	}
}
