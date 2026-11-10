package geopackage

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// The gazetteer GeoPackage is a read-only dataset: production mounts it ":ro",
// and the package build owns its schema, indexes included. Opening it must never
// change a byte of the file — the attribute index used to be created at
// startup, which silently rewrote packages (and the committed test fixture).

// TestOpenGazetteerIndex_NeverWritesTheFile: the handle is read-only, so neither
// opening (even with a journal mode configured) nor querying touches the file,
// and a write through the handle is refused instead of applied.
func TestOpenGazetteerIndex_NeverWritesTheFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gazetteer.gpkg")
	buildGazetteerFixture(t, path, true)
	before := readFile(t, path)

	idx, err := OpenGazetteerIndex(ctx, path, Options{JournalMode: "WAL"})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex: %v", err)
	}
	if _, err := idx.QueryKNN(ctx, "places", domain.NewWGS84Coordinate(10.05, 50.0), 3, 60,
		&output.Filter{Column: "place", Values: []any{"city"}}); err != nil {
		t.Fatalf("QueryKNN: %v", err)
	}
	if _, err := idx.db.ExecContext(ctx, `CREATE INDEX "probe" ON "places" ("place")`); err == nil {
		t.Error("write through the gazetteer handle succeeded; want it refused (read-only)")
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !bytes.Equal(before, readFile(t, path)) {
		t.Error("opening the gazetteer changed the GeoPackage file")
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm", path + "-journal"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("opening the gazetteer created %s", filepath.Base(sidecar))
		}
	}
}

// TestOpenGazetteerIndex_ReadOnlyFile mirrors the production ":ro" mount: a file
// without write permission opens and answers queries.
func TestOpenGazetteerIndex_ReadOnlyFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gazetteer.gpkg")
	buildGazetteerFixture(t, path, true)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}

	idx, err := OpenGazetteerIndex(ctx, path, Options{})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex on a read-only file: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	got, err := idx.QueryKNN(ctx, "places", domain.NewWGS84Coordinate(10.0, 50.0), 1, 10, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("QueryKNN on a read-only file = %d hits, err %v", len(got), err)
	}
}

// TestGazetteerIndex_HasAttributeIndex: the startup check reports whether the
// package ships the rank index, without creating it.
func TestGazetteerIndex_HasAttributeIndex(t *testing.T) {
	ctx := context.Background()
	idx, path := openFixtureIndexAt(t, true)

	has, err := idx.HasAttributeIndex(ctx, "places", "place")
	if err != nil || has {
		t.Fatalf("HasAttributeIndex without the index = %v, %v; want false, nil", has, err)
	}
	addAttributeIndex(t, path, "places", "place")
	has, err = idx.HasAttributeIndex(ctx, "places", "place")
	if err != nil || !has {
		t.Fatalf("HasAttributeIndex with the index = %v, %v; want true, nil", has, err)
	}
}

// openFixtureIndexAt is openFixtureIndex that also returns the file path, for
// tests that change the package from outside the (read-only) handle.
func openFixtureIndexAt(t *testing.T, withRtree bool) (idx *GazetteerIndex, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "gazetteer.gpkg")
	buildGazetteerFixture(t, path, withRtree)
	idx, err := OpenGazetteerIndex(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx, path
}

// addAttributeIndex adds the rank index the way the package build does: through
// a separate, writable connection, never through the gazetteer handle.
func addAttributeIndex(t *testing.T, path, layer, column string) {
	t.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path)
	if err != nil {
		t.Fatalf("open %s for writing: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), AttributeIndexDDL(layer, column)); err != nil {
		t.Fatalf("create attribute index: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
