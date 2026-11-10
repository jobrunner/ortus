package app

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

// TestGazetteerPackageStaysUntouched: production mounts the gazetteer ":ro", and
// the package build owns its schema. Starting ortus must leave every byte of the
// package as it was — also on a writable file, where the rank index used to be
// created at startup (which rewrote packages and the committed fixture). A
// package that ships the index starts without the slow-query warning; one
// without it starts too, but says what to add to the build.
func TestGazetteerPackageStaysUntouched(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dropIndex bool
		readOnly  bool
		wantWarn  bool
	}{
		{name: "package ships the rank index"},
		{name: "package lacks the rank index", dropIndex: true, wantWarn: true},
		{name: "read-only file like the :ro mount", dropIndex: true, readOnly: true, wantWarn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gazetteer.gpkg")
			if err := os.WriteFile(path, mustRead(t, "testdata/gazetteer-fixture.gpkg"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.dropIndex {
				dropRankIndex(t, path)
			}
			if tc.readOnly {
				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
			}
			before := mustRead(t, path)

			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			a, err := New(context.Background(), gazetteerOnlyConfig(t, path), logger)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := a.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}

			if !bytes.Equal(before, mustRead(t, path)) {
				t.Error("starting ortus changed the gazetteer GeoPackage")
			}
			warned := strings.Contains(logs.String(), "lacks the attribute index")
			if warned != tc.wantWarn {
				t.Errorf("rank-index warning = %v, want %v; logs:\n%s", warned, tc.wantWarn, logs.String())
			}
		})
	}
}

func gazetteerOnlyConfig(t *testing.T, gpkg string) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Storage.Type = config.StorageTypeLocal
	cfg.Storage.LocalPath = t.TempDir()
	cfg.Gazetteer.Enabled = true
	cfg.Gazetteer.GeoPackagePath = gpkg
	cfg.Gazetteer.ManifestPath = "testdata/gazetteer-manifest.yaml"
	cfg.Gazetteer.LevelReferencePath = "testdata/gazetteer-sidecar.yaml"
	cfg.Gazetteer.NameSourceManifestPath = "testdata/gazetteer-name-sources.yaml"
	return cfg
}

func dropRankIndex(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), `DROP INDEX "idx_ortus_places_place"`); err != nil {
		t.Fatalf("drop rank index: %v", err)
	}
}
