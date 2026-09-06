package http

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"sync"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// batchGazetteer enriches each valid point with a gazetteer block. It launches a
// goroutine per point but caps how many run concurrently with a semaphore
// (query.batch.concurrency): per-point gazetteer queries contend on SQLite, so
// unbounded parallelism is slower, not faster. Returns nil when enrichment is
// off/unavailable. Each goroutine writes only its own index, so no synchronization
// on the result slice is needed.
func (s *Server) batchGazetteer(r *http.Request, req *batchRequest, wgs []domain.Coordinate, wgsOK []bool, itemErr []string) []map[string]interface{} {
	if !batchWantsGazetteer(req) || s.gazetteer == nil {
		return nil
	}
	ctx := r.Context()
	out := make([]map[string]interface{}, len(wgs))
	// Enrich in spatial (tile-locality) order rather than input order: consecutive
	// per-point DEM/gazetteer lookups then reuse warm raster tile handles (the
	// tileset keeps a bounded open-handle LRU) and OS page cache instead of
	// thrashing across a scattered batch. Order only affects processing — results
	// are written by original index, so the caller's echo-id order is unchanged.
	order := orderedByLocality(wgs, wgsOK, itemErr)
	sem := make(chan struct{}, s.batchConcurrency)
	var wg sync.WaitGroup
	for _, i := range order {
		// Acquire a slot, but bail if the client disconnected — otherwise a
		// canceled request would keep queueing (and blocking on) work for every
		// remaining point.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return out
		}
		wg.Add(1)
		go func(idx int, w domain.Coordinate) {
			defer wg.Done()
			defer func() { <-sem }()
			out[idx] = s.enrichGazetteerPoint(ctx, w)
		}(i, wgs[i])
	}
	wg.Wait()
	return out
}

// orderedByLocality returns the indices of enrichable points (valid WGS84 coord,
// no per-item error) sorted by DEM tile locality, so enrichment processes
// spatially adjacent points consecutively (warm tile cache).
func orderedByLocality(wgs []domain.Coordinate, wgsOK []bool, itemErr []string) []int {
	order := make([]int, 0, len(wgs))
	for i := range wgs {
		if itemErr[i] == "" && wgsOK[i] {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool { return lessTileLocality(wgs[order[a]], wgs[order[b]]) })
	return order
}

// lessTileLocality orders coordinates so spatially-close points — and thus points
// sharing a DEM tile (Copernicus GLO-30 tiles are 1°×1°) — are processed
// consecutively, warming the raster tile-handle LRU and OS page cache. It sorts
// row-major over 1° tiles (latitude band, then longitude), with a stable
// sub-order within a tile so the sort is deterministic. X is longitude, Y latitude.
func lessTileLocality(a, b domain.Coordinate) bool {
	if fa, fb := math.Floor(a.Y), math.Floor(b.Y); fa != fb {
		return fa < fb
	}
	if fa, fb := math.Floor(a.X), math.Floor(b.X); fa != fb {
		return fa < fb
	}
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	return a.X < b.X
}

// enrichGazetteerPoint resolves the gazetteer block for one coordinate, returning
// nil (and logging, unless the request was canceled) on failure so a single
// point's error never fails the whole batch.
func (s *Server) enrichGazetteerPoint(ctx context.Context, w domain.Coordinate) map[string]interface{} {
	// One PiP-cache scope per point, exactly like the single endpoint
	// (handleGazetteer) and the MCP tool: Locate and Bearing both ask which admin
	// polygons contain the point — without the scope that query runs twice per
	// point. Per point (not per batch) so the cache cannot grow with batch size.
	ctx = input.WithPointInPolygonCache(ctx)
	sec, err := s.gazetteerSections(ctx, w)
	if err != nil {
		// Suppress the warning for cancellation AND deadline: once the request's
		// context is done, every in-flight point would otherwise log, turning one
		// timeout into a burst of warnings.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("batch gazetteer enrichment failed", "error", err)
		}
		return nil
	}
	return sec
}

// errBatchCountMismatch guards the invariant that the batch query returns one
// response per input coordinate, so a future divergence fails cleanly instead
// of panicking on the scatter.
var errBatchCountMismatch = errors.New("batch query returned an unexpected result count")

// handleBatchError maps buildBatchChunk's errors: the invariant guard keeps its
// specific 500 message, everything else goes through the shared classification
// (e.g. unknown source → 404).
func (s *Server) handleBatchError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBatchCountMismatch) {
		s.writeError(w, http.StatusInternalServerError, errBatchCountMismatch.Error())
		return
	}
	s.handleQueryError(w, err)
}

// buildBatchChunk runs the whole batch pipeline for the request's points —
// per-point resolution, the set-based PiP query (honoring the with-sources
// switch, see with_sources.go), the scatter back to input order, and the item
// assembly (incl. gazetteer enrichment). It is the shared unit behind the sync
// response (one call for everything) and the NDJSON stream (one call per chunk).
func (s *Server) buildBatchChunk(r *http.Request, req *batchRequest) ([]map[string]interface{}, error) {
	in := s.resolveBatchInputs(r, req)
	sub, err := s.resolveBatchResponses(r.Context(), req, in.valid)
	if err != nil {
		return nil, err
	}
	if len(sub) != len(in.valid) {
		return nil, errBatchCountMismatch
	}
	responses := make([]*domain.QueryResponse, len(req.Points))
	for k, origIdx := range in.validIdx {
		responses[origIdx] = sub[k]
	}
	return s.buildBatchItems(r, req, in.wgs, in.wgsOK, responses, in.itemErr), nil
}

// batchStreamChunkSize is how many points the NDJSON stream computes per chunk.
// Each chunk runs the full pipeline (set-based PiP + per-point gazetteer) and
// its items are emitted immediately, so the first bytes arrive after ONE chunk
// (~1-2 s on the real dataset) instead of after the whole batch (27 s for 883
// points under the old compute-everything-first delivery). Smaller chunks lower
// the time to first byte but repeat the per-layer query overhead more often;
// 100 keeps that overhead near-negligible while staying well inside proxy idle
// timeouts.
const batchStreamChunkSize = 100

// streamBatchChunks answers the NDJSON mode incrementally: the request's points
// are processed in input-order chunks, and every chunk's items are written (and
// flushed) as soon as they are computed. The response header is only sent once
// the FIRST chunk succeeded, so early failures (e.g. an unknown source id)
// still surface as proper HTTP errors; a failure after that can only abort the
// stream (logged). Lines keep the exact input order — chunking bounds the DEM
// tile-locality ordering of the gazetteer enrichment to each chunk, a deliberate
// trade-off for incremental delivery.
func (s *Server) streamBatchChunks(w http.ResponseWriter, r *http.Request, req *batchRequest) {
	// http.ResponseController reaches Flush through middleware wrappers (via
	// their Unwrap) — a plain w.(http.Flusher) assertion does not, which left
	// the per-line flush silently inert behind the metrics middleware.
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	headerSent := false
	for start := 0; start < len(req.Points); start += batchStreamChunkSize {
		end := min(start+batchStreamChunkSize, len(req.Points))
		chunk := *req
		chunk.Points = req.Points[start:end]
		items, err := s.buildBatchChunk(r, &chunk)
		if err != nil {
			if !headerSent {
				s.handleBatchError(w, err)
			} else {
				s.logger.Warn("batch stream aborted mid-stream", "error", err, "emitted_points", start)
			}
			return
		}
		if !headerSent {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			headerSent = true
		}
		for _, item := range items {
			if err := r.Context().Err(); err != nil {
				return // client disconnected
			}
			if err := enc.Encode(item); err != nil { // Encode writes the trailing newline
				s.logger.Debug("batch stream write failed", "error", err)
				return
			}
			_ = rc.Flush()
		}
	}
}
