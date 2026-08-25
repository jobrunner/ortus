package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

// TestNewReportsInitFailuresInsteadOfPanicking pins the contract that a failed
// New returns its error. It exists because it did not: New has a named `app`
// return value and a deferred cleanup that dereferences it, while every error
// path after that defer does `return nil, err` — which sets the named value to
// nil *before* defers run. The result was a segfault at startup that replaced
// the real, actionable error message.
func TestNewReportsInitFailuresInsteadOfPanicking(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	// Both error paths that sit after the cleanup defer, so the contract is shown
	// to hold for the mechanism rather than for one subsystem.
	cases := []struct {
		name    string
		mangle  func(cfg *config.Config, dir string)
		wantSub string
	}{
		{
			name: "missing gazetteer manifest",
			mangle: func(cfg *config.Config, dir string) {
				cfg.Gazetteer.Enabled = true
				cfg.Gazetteer.ManifestPath = filepath.Join(dir, "absent-manifest.yaml")
				cfg.Gazetteer.GeoPackagePath = filepath.Join(dir, "absent.gpkg")
			},
			wantSub: "gazetteer",
		},
		{
			name: "unusable TLS configuration",
			mangle: func(cfg *config.Config, dir string) {
				cfg.TLS.Enabled = true
				cfg.TLS.Domains = []string{"example.invalid"}
				// A cache dir under a regular file cannot be created.
				blocker := filepath.Join(dir, "blocker")
				_ = os.WriteFile(blocker, []byte("x"), 0o600)
				cfg.TLS.CacheDir = filepath.Join(blocker, "acme")
			},
			wantSub: "TLS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.Config{}
			cfg.Storage.Type = config.StorageTypeLocal
			cfg.Storage.LocalPath = dir
			cfg.Metrics.Enabled = false
			tc.mangle(cfg, dir)

			app, err := New(context.Background(), cfg, logger)
			if err == nil {
				// Not a skip: if this configuration stops failing, the case no
				// longer exercises the error path and the test would silently stop
				// guarding anything.
				t.Fatalf("expected this configuration to fail; it did not, so the path is untested: %+v", app)
			}
			if app != nil {
				t.Errorf("a failed New must not return an App, got %+v", app)
			}
			// The operator has to be able to act on this: the message must name
			// what was actually wrong, not merely that something was.
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error should name the failing subsystem %q, got: %v", tc.wantSub, err)
			}
		})
	}
}
