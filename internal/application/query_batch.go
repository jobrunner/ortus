package application

import (
	"context"
	"errors"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// QueryBatch resolves many coordinates in one pass, returning one QueryResponse
// per input coordinate in input order. Point-in-polygon is resolved SET-BASED
// (one query per source/layer for ALL points, via the registry batch seam), which
// is far cheaper than N per-point queries and avoids the reader contention that
// naive per-point fan-out causes. A per-source failure is isolated (logged, that
// source contributes nothing) so one bad source never drops the whole batch.
func (s *QueryService) QueryBatch(ctx context.Context, coords []domain.Coordinate, sources, properties []string) ([]*domain.QueryResponse, error) {
	start := time.Now()
	if s.queryTimeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.queryTimeout)
			defer cancel()
		}
	}
	ctx, span := s.tracer.Start(ctx, "QueryService.QueryBatch",
		output.WithAttributes(
			output.Int("ortus.batch.points", len(coords)),
			output.Int("ortus.batch.sources_requested", len(sources)),
		),
	)
	defer span.End()

	sourceIDs, err := s.resolveBatchSources(sources)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "resolve sources")
		return nil, err
	}

	out := make([]*domain.QueryResponse, len(coords))
	for i := range coords {
		out[i] = &domain.QueryResponse{Coordinate: coords[i]}
	}

	for _, sid := range sourceIDs {
		if err := s.queryBatchSource(ctx, sid, coords, properties, out); err != nil {
			if isContextErr(err) {
				// Cancellation/deadline is global to the shared context — abort and
				// surface it so a timed-out batch is a real failure, not empty success.
				span.RecordError(err)
				span.SetStatus(output.StatusError, "batch canceled or timed out")
				return nil, err
			}
			s.logger.Warn("batch query failed for source", "source", sid, "error", err)
		}
	}

	elapsed := time.Since(start)
	for i := range out {
		out[i].ProcessingTime = elapsed
	}
	span.SetAttributes(output.Int("ortus.batch.sources_queried", len(sourceIDs)))
	span.SetStatus(output.StatusOK, "")
	return out, nil
}

// resolveBatchSources returns the ready source ids, optionally restricted to the
// requested set. An unknown requested id is a client error (ErrSourceNotFound),
// matching QueryPoint's single-source behavior.
func (s *QueryService) resolveBatchSources(sources []string) ([]string, error) {
	all := s.registry.ReadySourceIDs()
	if len(sources) == 0 {
		return all, nil
	}
	ready := make(map[string]bool, len(all))
	for _, id := range all {
		ready[id] = true
	}
	// Validate and de-duplicate: a duplicate id (e.g. ["pkg1","pkg1"]) must not
	// query the same source twice and double its results.
	seen := make(map[string]bool, len(sources))
	deduped := make([]string, 0, len(sources))
	for _, id := range sources {
		if !ready[id] {
			return nil, domain.ErrSourceNotFound
		}
		if !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}
	return deduped, nil
}

// queryBatchSource resolves one source's layers for all points and adds a
// per-point QueryResult (when it has features) to the matching response.
func (s *QueryService) queryBatchSource(ctx context.Context, sid string, coords []domain.Coordinate, properties []string, out []*domain.QueryResponse) error {
	pkg, err := s.registry.GetSource(ctx, sid)
	if err != nil {
		return err
	}
	start := time.Now()
	results := make([]domain.QueryResult, len(coords))
	for i := range results {
		results[i] = domain.QueryResult{SourceID: pkg.ID, SourceName: pkg.Name, License: pkg.License}
	}
	for li := range pkg.Layers {
		if err := s.batchLayer(ctx, sid, &pkg.Layers[li], coords, properties, results); err != nil {
			return err // context error → abort this source (and the batch)
		}
	}
	// Attribute the source's batch query time amortized per point, so summing the
	// items' query_time_ms approximates the source's total (rather than each point
	// reporting the whole-batch time, which would inflate the sum ~N×).
	per := time.Since(start)
	if n := len(coords); n > 0 {
		per /= time.Duration(n)
	}
	for i := range results {
		if results[i].HasFeatures() {
			results[i].QueryTime = per
			out[i].AddResult(results[i])
		}
	}
	return nil
}

// batchLayer queries one layer set-based for all transformable points and folds
// the per-point features into results. Points whose SRID can't be transformed to
// the layer are skipped for this layer (same as queryLayer's ok=false path). A
// context error (cancellation or the server's query.timeout deadline) is returned
// so the batch aborts instead of reporting a misleading empty result; any other
// adapter error is isolated (logged, this layer contributes nothing).
func (s *QueryService) batchLayer(ctx context.Context, sid string, layer *domain.Layer, coords []domain.Coordinate, properties []string, results []domain.QueryResult) error {
	tc := make([]domain.Coordinate, 0, len(coords))
	idxs := make([]int, 0, len(coords))
	for i, c := range coords {
		// Skip points already at the per-point feature cap — the single-point path
		// stops querying further layers once max_features is reached (query.go), so
		// mirror that here and keep already-full points out of later layers' set query.
		// Also skip coordinates that fail validation (the batch isolates a bad point
		// as an empty result rather than failing the whole request), so QueryBatch
		// applies the same coordinate validation as QueryPoint even when a caller
		// bypasses the HTTP handler's pre-validation.
		if len(results[i].Features) >= s.maxFeatures || c.Validate() != nil {
			continue
		}
		if qc, ok := s.transformCoordinate(ctx, c, layer); ok {
			tc = append(tc, qc)
			idxs = append(idxs, i)
		}
	}
	if len(tc) == 0 {
		return nil
	}
	feats, err := s.registry.QueryPoints(ctx, sid, layer.Name, tc)
	if err != nil {
		if isContextErr(err) {
			return err // cancellation/deadline → abort the batch, don't fake an empty result
		}
		s.logger.Warn("batch layer query failed", "source", sid, "layer", layer.Name, "error", err)
		return nil
	}
	for k, origIdx := range idxs {
		f := feats[k]
		if len(properties) > 0 {
			f = s.filterProperties(f, properties)
		}
		limited, _ := s.applyMaxFeaturesLimit(f, &results[origIdx])
		results[origIdx].Features = append(results[origIdx].Features, limited...)
	}
	return nil
}

// isContextErr reports whether err is a context cancellation OR deadline. Unlike
// isCanceled (which excludes DeadlineExceeded so a slow single-point query still
// warns), the batch path treats BOTH as reasons to abort and propagate: a
// timed-out or aborted batch must surface as a real failure to the caller, not a
// misleading empty "no hits" response.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
