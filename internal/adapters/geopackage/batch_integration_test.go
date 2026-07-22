package geopackage

import (
	"context"
	"fmt"
	"sort"
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
