package geopackage

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// The GazetteerIndex's nearest-neighbor search lives in this file: the staged
// (growing-radius) QueryKNN, the two SQL forms it alternates between, and the
// attribute index that makes the wide filtered form affordable. Split out of
// spatialindex.go to keep that file at its complexity baseline — the same
// pattern as query_coordinates.go for the HTTP handlers.
//
// The attribute Filter is why this uses an R-tree bbox pre-filter rather than
// SpatiaLite's VirtualKNN2: KNN2 cannot push an attribute predicate (place-class
// or admin membership) into the nearest search, and every gazetteer query is
// class- and boundary-constrained, so a filtered radius search is the right tool.

// knnDistColumn is the alias under which the KNN queries project the ellipsoidal
// distance (meters) so QueryKNN returns it without a per-row DistanceKM call.
const knnDistColumn = "__ortus_dist_m"

// knnStageStartKM is the first stage of the growing-radius KNN search: in
// settled regions the k nearest places of a class all lie within a few km, so a
// small first bbox answers most queries at a fraction of the wide-box cost
// (the 120 km candidate gather dominated batch gazetteer requests). A query
// whose first stage holds fewer than k hits escalates — see knnStages.
const knnStageStartKM = 15.0

// knnFilterIndexMinKM is the stage radius from which a filtered KNN switches
// from the R-tree bbox plan to scanning the filter column's attribute index.
// A wide radius makes the bbox cover a huge share of the layer, and SQLite
// fetches every row in the box BEFORE the attribute filter runs — measured on
// the real dataset (422k places, 1.9k cities): a 120 km city query costs 573 ms
// via the R-tree plan and well under 10 ms via the place index. Narrow radii
// stay on the R-tree, where the small bbox wins (~2 ms). The planner never
// picks the attribute index on its own — not even with ANALYZE — so the choice
// is forced in both directions (see buildKNNQuery).
const knnFilterIndexMinKM = 30.0

// knnStages returns the radii of the growing KNN search, ending exactly at
// maxKM. Escalation is geometric (×3) so the worst case adds a small constant
// number of cheaper probes before the full-radius query — cheaper in BOTH query
// forms: the R-tree form fetches fewer bbox rows, and the attribute-index form
// materializes a smaller R-tree id set for its IN-subquery (measured on the
// 51-point batch: staged 0.9 s vs. 3.4 s when wide filtered stages jumped
// straight to maxKM).
func knnStages(maxKM float64) []float64 {
	if maxKM <= knnStageStartKM {
		return []float64{maxKM}
	}
	stages := []float64{knnStageStartKM}
	for r := knnStageStartKM * 3; r < maxKM; r *= 3 {
		stages = append(stages, r)
	}
	return append(stages, maxKM)
}

// EnsureAttributeIndex creates the attribute index a filtered KNN needs to stay
// fast on wide radii (see knnFilterIndexMinKM), idempotently, named so
// filterIndexFor finds it. Call it at startup for the manifest's rank column;
// on a read-only file the caller may treat the error as a performance warning
// (queries stay correct on the R-tree plan without it).
func (g *GazetteerIndex) EnsureAttributeIndex(ctx context.Context, layer, column string) error {
	stmt := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %q ON %q (%q)`,
		attributeIndexName(layer, column), layer, column) //#nosec G201 -- identifiers from the verified manifest, double-quoted; SQLite can't parameterize identifiers
	_, err := g.db.ExecContext(ctx, stmt)
	return err
}

// attributeIndexName is the naming contract between EnsureAttributeIndex and
// filterIndexFor.
func attributeIndexName(layer, column string) string {
	return fmt.Sprintf("idx_ortus_%s_%s", layer, column)
}

// filterIndexFor returns the name of the ortus-managed attribute index for the
// filter's column when it exists, else "". A missing index (read-only file,
// pre-existing deployment) just keeps the R-tree plan.
func (g *GazetteerIndex) filterIndexFor(ctx context.Context, layer string, f *output.Filter) string {
	if f == nil || len(f.Values) == 0 {
		return ""
	}
	name := attributeIndexName(layer, f.Column)
	var n int
	if err := g.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil || n == 0 {
		return ""
	}
	return name
}

// QueryKNN returns up to k nearest features within maxKM of p (optionally
// attribute-filtered), each paired with its ellipsoidal distance projected by the
// same query — so callers need no per-candidate DistanceKM round-trip.
//
// The search grows its radius in stages: a stage with at least k hits already
// holds the true k nearest (every feature outside a bbox of half-side s is more
// than s away, and hits are radius-filtered to s), so wider stages only run
// when needed. Results are identical to a single maxKM query — measured, the
// staged search is what keeps the wide composite candidate gather (120 km,
// k=250) affordable in dense regions.
func (g *GazetteerIndex) QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *output.Filter) ([]output.NearFeature, error) {
	geom, err := geomColumn(ctx, g.db, layer)
	if err != nil {
		return nil, err
	}
	if k < 1 {
		k = 1
	}
	rtree := rtreeName(layer, geom)
	hasRtree := tableExists(ctx, g.db, rtree)
	filterIndex := g.filterIndexFor(ctx, layer, f)
	// Without an R-tree every stage is a full scan, so staging only adds cost.
	stages := []float64{maxKM}
	if hasRtree {
		stages = knnStages(maxKM)
	}
	var feats []domain.Feature
	for _, stage := range stages {
		query, args := buildKNNQuery(layer, geom, hasRtree, p, k, stage, f, filterIndex)
		// KNN keeps the geometry: the places layer is points (tiny WKT) and the
		// caller parses the coordinate out of it.
		feats, err = g.runFeatureQuery(ctx, layer, geom, true, query, args...)
		if err != nil {
			return nil, err
		}
		if len(feats) >= k {
			break
		}
	}
	out := make([]output.NearFeature, 0, len(feats))
	for i := range feats {
		// The projected distance rides back as a synthetic property; read it with the
		// shared numeric coercion, then strip it so it never leaks to callers. A miss
		// yields 0 (GetFloatProperty) and delete is a no-op, so this stays safe.
		km := feats[i].GetFloatProperty(knnDistColumn) / 1000 // Distance() returns meters
		delete(feats[i].Properties, knnDistColumn)
		out = append(out, output.NearFeature{Feature: feats[i], DistanceKM: km})
	}
	return out, nil
}

// buildKNNQuery assembles the radius-search SQL and its ordered args for one
// stage. The plan choice is forced in BOTH directions once the ortus attribute
// index exists (see knnFilterIndexMinKM) — the planner gets a wide radius wrong
// on its own (full bbox row-fetch before the filter), and on small fixtures it
// picks the attribute index for narrow radii where the R-tree's tiny bbox wins.
// Without the ortus index (nil filter, read-only file, foreign indexes) the
// query text is unchanged from the classic R-tree form.
func buildKNNQuery(layer, geom string, hasRtree bool, p domain.Coordinate, k int, maxKM float64, f *output.Filter, filterIndex string) (query string, args []any) {
	var inner string
	if filterIndex != "" && maxKM >= knnFilterIndexMinKM {
		inner, args = knnIndexedForm(layer, geom, hasRtree, p, maxKM, f, filterIndex)
	} else {
		inner, args = knnRtreeForm(layer, geom, hasRtree, p, maxKM, f, filterIndex != "")
	}
	// The radius filter and the ordering both run on the OUTER query, against the
	// already-projected distance. Spelling the distance expression out in WHERE
	// (SQLite does not allow an output alias there) made SpatiaLite evaluate the
	// ellipsoidal Distance twice per candidate row inside the bounding box —
	// measured, that doubled the query: 20 ms against 10 ms on a dense point.
	query = fmt.Sprintf(`SELECT * FROM (%s) WHERE %q <= ? ORDER BY %q ASC LIMIT ?`,
		inner, knnDistColumn, knnDistColumn)
	args = append(args, maxKM*1000, k)
	return query, args
}

// knnSelect is the shared projection of both query forms: the row, the exact
// distance (its two placeholders lead the args), and the point geometry as WKT.
func knnSelect(layer, geom, tableSuffix string) string {
	distExpr := fmt.Sprintf(`Distance(CastAutomagic(t.%q), MakePoint(?, ?, 4326), 1)`, geom)
	return fmt.Sprintf(`SELECT t.*, %s AS %q, AsText(CastAutomagic(t.%q)) FROM %q t%s`,
		distExpr, knnDistColumn, geom, layer, tableSuffix)
}

// knnIndexedForm is the wide-radius filtered form: scan the filter column's
// attribute index (forced via INDEXED BY) with the R-tree as an UNCORRELATED
// IN-subquery, so its box ids are materialized ONCE. An INDEXED BY + JOIN
// spelling instead made the planner rescan the whole R-tree per candidate row
// (90 s batches); this form plans `SEARCH t USING INDEX (col=? AND rowid=?)`.
func knnIndexedForm(layer, geom string, hasRtree bool, p domain.Coordinate, maxKM float64, f *output.Filter, filterIndex string) (inner string, args []any) {
	var b strings.Builder
	b.WriteString(knnSelect(layer, geom, fmt.Sprintf(` INDEXED BY %q`, filterIndex)))
	args = []any{p.X, p.Y}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(f.Values)), ",")
	fmt.Fprintf(&b, ` WHERE t."%s" IN (%s)`, f.Column, placeholders)
	args = append(args, f.Values...)
	if hasRtree {
		minX, maxX, minY, maxY := knnBBox(p, maxKM)
		fmt.Fprintf(&b, ` AND t.rowid IN (SELECT id FROM %q WHERE maxx >= ? AND minx <= ? AND maxy >= ? AND miny <= ?)`,
			rtreeName(layer, geom))
		args = append(args, minX, maxX, minY, maxY)
	}
	return b.String(), args
}

// knnRtreeForm is the classic narrow-radius form: R-tree bbox join, then the
// attribute filter. pinRtree adds NOT INDEXED so the planner cannot drift to the
// attribute index where the tiny bbox wins (it does on small fixtures).
func knnRtreeForm(layer, geom string, hasRtree bool, p domain.Coordinate, maxKM float64, f *output.Filter, pinRtree bool) (inner string, args []any) {
	suffix := ""
	if pinRtree {
		suffix = ` NOT INDEXED`
	}
	var b strings.Builder
	b.WriteString(knnSelect(layer, geom, suffix))
	args = []any{p.X, p.Y}
	if hasRtree {
		minX, maxX, minY, maxY := knnBBox(p, maxKM)
		fmt.Fprintf(&b, ` JOIN %q r ON t.rowid = r.id`, rtreeName(layer, geom))
		b.WriteString(` WHERE r.maxx >= ? AND r.minx <= ? AND r.maxy >= ? AND r.miny <= ?`)
		args = append(args, minX, maxX, minY, maxY)
	} else {
		b.WriteString(` WHERE 1 = 1`)
	}
	if f != nil && len(f.Values) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(f.Values)), ",")
		fmt.Fprintf(&b, ` AND t."%s" IN (%s)`, f.Column, placeholders)
		args = append(args, f.Values...)
	}
	return b.String(), args
}

// knnBBox returns a lon/lat bounding box of half-side maxKM around p, used as the
// R-tree pre-filter. Longitude degrees shrink with latitude; the cosine is
// floored so the box stays finite near the poles.
func knnBBox(p domain.Coordinate, maxKM float64) (minX, maxX, minY, maxY float64) {
	const kmPerDegree = 111.32
	dLat := maxKM / kmPerDegree
	cos := math.Cos(p.Y * math.Pi / 180)
	if cos < 0.01 {
		cos = 0.01
	}
	dLon := maxKM / (kmPerDegree * cos)
	return p.X - dLon, p.X + dLon, p.Y - dLat, p.Y + dLat
}
