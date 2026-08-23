package geopackage

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// TracedSpatialIndex wraps a SpatialIndex with span instrumentation. It is a
// decorator and adds no business logic, so the cgo/SpatiaLite adapter stays free
// of telemetry code — the same split as TracedStorage.
//
// Every SpatiaLite round-trip the gazetteer makes passes through this port, so
// decorating it here (rather than sprinkling spans through the query code) is
// what makes the gazetteer's SQL work visible in a trace at all. The layer name
// is always recorded: it is the attribute that separates an admin-boundary
// lookup from an island, mountain or place query, which otherwise share a span
// name.
type TracedSpatialIndex struct {
	inner  output.SpatialIndex
	tracer output.Tracer
}

// Compile-time proof that the decorator still covers the whole port. Without it,
// adding a method to SpatialIndex would leave this decorator behind silently:
// callers holding the concrete type keep compiling, and the new method's calls
// would simply never appear in a trace.
var _ output.SpatialIndex = (*TracedSpatialIndex)(nil)

// NewTracedSpatialIndex wraps inner with tracing using the given tracer. A nil
// tracer degrades to NoOpTracer, so wiring it unconditionally is safe.
func NewTracedSpatialIndex(inner output.SpatialIndex, tracer output.Tracer) *TracedSpatialIndex {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	return &TracedSpatialIndex{inner: inner, tracer: tracer}
}

// QueryKNN implements SpatialIndex.
func (t *TracedSpatialIndex) QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *output.Filter) ([]output.NearFeature, error) {
	attrs := []output.Attribute{
		output.String("spatial.layer", layer),
		output.Int("spatial.knn.k", k),
		output.Float64("spatial.knn.max_km", maxKM),
		// Whether a filter is present changes the query plan (and its cost), so
		// record the column even though the values stay out of the span.
		output.Bool("spatial.knn.filtered", f != nil),
	}
	if f != nil {
		attrs = append(attrs,
			output.String("spatial.knn.filter_column", f.Column),
			output.Int("spatial.knn.filter_values", len(f.Values)),
		)
	}
	ctx, span := t.tracer.Start(ctx, "SpatialIndex.QueryKNN",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(attrs...),
	)
	defer span.End()

	res, err := t.inner.QueryKNN(ctx, layer, p, k, maxKM, f)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "knn query failed")
		return nil, err
	}
	span.SetAttributes(output.Int("spatial.result.count", len(res)))
	span.SetStatus(output.StatusOK, "")
	return res, nil
}

// PointInPolygon implements SpatialIndex.
func (t *TracedSpatialIndex) PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error) {
	ctx, span := t.tracer.Start(ctx, "SpatialIndex.PointInPolygon",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(output.String("spatial.layer", layer)),
	)
	defer span.End()

	res, err := t.inner.PointInPolygon(ctx, layer, p)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "point-in-polygon query failed")
		return nil, err
	}
	span.SetAttributes(output.Int("spatial.result.count", len(res)))
	span.SetStatus(output.StatusOK, "")
	return res, nil
}

// ResolveChain implements SpatialIndex.
func (t *TracedSpatialIndex) ResolveChain(ctx context.Context, layer string, fromFID int64, cols output.AdminColumns) ([]output.AdminRow, error) {
	ctx, span := t.tracer.Start(ctx, "SpatialIndex.ResolveChain",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("spatial.layer", layer),
			output.Int64("spatial.chain.from_fid", fromFID),
		),
	)
	defer span.End()

	rows, err := t.inner.ResolveChain(ctx, layer, fromFID, cols)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "chain walk failed")
		return nil, err
	}
	// The chain length is the number of parent hops actually walked — one query
	// per level, so this is the span's cost driver.
	span.SetAttributes(output.Int("spatial.chain.length", len(rows)))
	span.SetStatus(output.StatusOK, "")
	return rows, nil
}

// DistanceKM implements SpatialIndex.
func (t *TracedSpatialIndex) DistanceKM(ctx context.Context, a, b domain.Coordinate) (float64, error) {
	ctx, span := t.tracer.Start(ctx, "SpatialIndex.DistanceKM",
		output.WithSpanKind(output.SpanKindClient),
	)
	defer span.End()

	km, err := t.inner.DistanceKM(ctx, a, b)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "distance failed")
		return 0, err
	}
	span.SetStatus(output.StatusOK, "")
	return km, nil
}

// Azimuth implements SpatialIndex.
func (t *TracedSpatialIndex) Azimuth(ctx context.Context, from, to domain.Coordinate) (float64, error) {
	ctx, span := t.tracer.Start(ctx, "SpatialIndex.Azimuth",
		output.WithSpanKind(output.SpanKindClient),
	)
	defer span.End()

	deg, err := t.inner.Azimuth(ctx, from, to)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "azimuth failed")
		return 0, err
	}
	span.SetStatus(output.StatusOK, "")
	return deg, nil
}
