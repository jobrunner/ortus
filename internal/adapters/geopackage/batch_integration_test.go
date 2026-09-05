package geopackage

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// featureSig is a stable, comparable signature of a feature set: sorted "name#id"
// tokens. Used to assert QueryPoints == QueryPoint without depending on row order.
func featureSig(fs []domain.Feature) string {
	toks := make([]string, len(fs))
	for i, f := range fs {
		name, _ := f.Properties["name"].(string)
		toks[i] = fmt.Sprintf("%s#%d", name, f.ID)
	}
	sort.Strings(toks)
	return fmt.Sprintf("%v", toks)
}

// batchTestPoints exercises the interesting cases against the regions fixture:
// inside-west, inside-east, gap (no hit), shared border (multi-hit), and the
// ST_Subdivide cut edge (dedup to one).
func batchTestPoints() []domain.Coordinate {
	return []domain.Coordinate{
		domain.NewCoordinate(2, 2, 4326),  // west
		domain.NewCoordinate(8, 2, 4326),  // east
		domain.NewCoordinate(5, 2, 4326),  // gap → no hit
		domain.NewCoordinate(17, 2, 4326), // borderA + borderB (shared edge)
		domain.NewCoordinate(13, 2, 4326), // tiled cut edge → dedup to one
	}
}

// assertBatchParity runs QueryPoints and asserts each element matches the
// per-point QueryPoint for the same coordinate (order + contents).
func assertBatchParity(t *testing.T, repo *Repository, pts []domain.Coordinate) {
	t.Helper()
	ctx := context.Background()
	batch, err := repo.QueryPoints(ctx, "regions", "regions", pts)
	if err != nil {
		t.Fatalf("QueryPoints: %v", err)
	}
	if len(batch) != len(pts) {
		t.Fatalf("QueryPoints returned %d slices, want %d", len(batch), len(pts))
	}
	for i, p := range pts {
		single, err := repo.QueryPoint(ctx, "regions", "regions", p)
		if err != nil {
			t.Fatalf("QueryPoint[%d]: %v", i, err)
		}
		if got, want := featureSig(batch[i]), featureSig(single); got != want {
			t.Errorf("point %d (%.0f,%.0f): QueryPoints=%s, QueryPoint=%s", i, p.X, p.Y, got, want)
		}
	}
}

// TestBatchQueryPointsIndexed: with an R-tree, the set-based path matches the
// per-point path for every point (order preserved, multi-hit, no-hit, dedup).
func TestBatchQueryPointsIndexed(t *testing.T) {
	repo, _ := newFixtureRepo(t)
	if err := repo.CreateSpatialIndex(context.Background(), "regions", "regions"); err != nil {
		t.Fatalf("CreateSpatialIndex: %v", err)
	}
	pts := batchTestPoints()
	assertBatchParity(t, repo, pts)

	// Spot-check the specific semantics the parity check rests on.
	batch, _ := repo.QueryPoints(context.Background(), "regions", "regions", pts)
	if len(batch[2]) != 0 {
		t.Errorf("gap point should have no hit, got %d features", len(batch[2]))
	}
	if len(batch[3]) != 2 {
		t.Errorf("border point should hit 2 features (borderA+borderB), got %d", len(batch[3]))
	}
	if len(batch[4]) != 1 {
		t.Errorf("tiled cut edge should dedup to 1 feature, got %d", len(batch[4]))
	}
}

// TestBatchQueryPointsFallback: with NO R-tree, QueryPoints falls back to per-point
// executePointQuery and still matches QueryPoint.
func TestBatchQueryPointsFallback(t *testing.T) {
	repo, _ := newFixtureRepo(t) // no CreateSpatialIndex → fallback path
	assertBatchParity(t, repo, batchTestPoints())
}

// TestBatchQueryPointsEmpty: an empty batch returns an empty (non-nil) result.
func TestBatchQueryPointsEmpty(t *testing.T) {
	repo, _ := newFixtureRepo(t)
	got, err := repo.QueryPoints(context.Background(), "regions", "regions", nil)
	if err != nil {
		t.Fatalf("QueryPoints(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty batch → %d slices, want 0", len(got))
	}
}

// TestBatchQueryPlanPointsDriveRTree pins the loop order of the set-based batch
// query: the points (json_each) must be the OUTER loop so SQLite probes the
// R-tree once per point. With a plain INNER JOIN the planner instead scans the
// whole R-tree and fetches every feature row of the layer — on real soil-map
// layers that turned a 2-point batch into seconds per layer (and into proxy
// 502s in production). The CROSS JOIN in buildBatchPointQuery forces the order
// (documented SQLite semantics), so this plan is stable to assert on.
func TestBatchQueryPlanPointsDriveRTree(t *testing.T) {
	repo, src := newFixtureRepo(t)
	ctx := context.Background()
	if err := repo.CreateSpatialIndex(ctx, "regions", "regions"); err != nil {
		t.Fatalf("CreateSpatialIndex: %v", err)
	}
	layer, ok := src.GetLayer("regions")
	if !ok {
		t.Fatal("regions layer missing")
	}

	repo.mu.RLock()
	db := repo.connections[src.ID]
	repo.mu.RUnlock()

	indexTable := fmt.Sprintf("rtree_%s_%s", layer.Name, layer.GeometryColumn)
	query := buildBatchPointQuery(layer, indexTable, false)
	args := []interface{}{`[{"x":2,"y":2},{"x":8,"y":2}]`}
	if layer.IsPolygonLayer() {
		args = append(args, layer.SRID)
	}

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if len(details) == 0 {
		t.Fatal("empty query plan")
	}
	// The first loop is the outer one. json_each (the points) must drive it;
	// if the R-tree comes first it is being scanned in full.
	if !strings.Contains(details[0], "json_each") {
		t.Errorf("outer loop is %q, want the json_each points scan first (R-tree full scan otherwise); full plan:\n%s",
			details[0], strings.Join(details, "\n"))
	}
}

// TestBatchQueryPointsGeometryOnlyWhenConfigured: serializing geometry (AsText)
// is the dominant cost of the batch query on large polygon layers (measured:
// 28 MB of WKT for 39 hits, 3x the query time) — and the HTTP layer throws the
// WKT away unless query.with_geometry is on. So the batch path must only ask
// SQLite for AsText when the repository is configured to deliver geometry.
func TestBatchQueryPointsGeometryOnlyWhenConfigured(t *testing.T) {
	ctx := context.Background()
	pt := []domain.Coordinate{domain.NewCoordinate(2, 2, 4326)} // inside "west"

	// Default: no geometry configured → WKT stays empty (AsText not computed).
	repo, _ := newFixtureRepo(t)
	if err := repo.CreateSpatialIndex(ctx, "regions", "regions"); err != nil {
		t.Fatalf("CreateSpatialIndex: %v", err)
	}
	batch, err := repo.QueryPoints(ctx, "regions", "regions", pt)
	if err != nil {
		t.Fatalf("QueryPoints: %v", err)
	}
	if len(batch[0]) == 0 {
		t.Fatal("expected a hit at (2,2)")
	}
	if wkt := batch[0][0].Geometry.WKT; wkt != "" {
		t.Errorf("default (no geometry): WKT should be empty, got %d bytes", len(wkt))
	}

	// WithGeometry: WKT is delivered.
	path := filepath.Join(t.TempDir(), "regions-geo.gpkg")
	buildFixtureGPKG(t, path)
	repoGeo := NewRepository(Options{WithGeometry: true})
	t.Cleanup(func() { _ = repoGeo.Close(ctx, "regions-geo") })
	if _, err := repoGeo.Open(ctx, path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := repoGeo.CreateSpatialIndex(ctx, "regions-geo", "regions"); err != nil {
		t.Fatalf("CreateSpatialIndex: %v", err)
	}
	batchGeo, err := repoGeo.QueryPoints(ctx, "regions-geo", "regions", pt)
	if err != nil {
		t.Fatalf("QueryPoints (with geometry): %v", err)
	}
	if len(batchGeo[0]) == 0 {
		t.Fatal("expected a hit at (2,2) with geometry")
	}
	if batchGeo[0][0].Geometry.WKT == "" {
		t.Errorf("WithGeometry: WKT should be delivered, got empty")
	}
}
