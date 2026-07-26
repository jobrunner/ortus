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

// streamBatchItems writes each item as its own JSON line (application/x-ndjson),
// flushing per line so the client can consume results incrementally and the
// server holds no large response buffer. The result set itself is computed
// set-based up front (one SQL per source), so this streams the already-resolved
// items rather than producing them lazily — a v1 trade-off (see the plan). It
// still lets a client abort mid-write via the request context.
func (s *Server) streamBatchItems(w http.ResponseWriter, r *http.Request, items []map[string]interface{}) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	for _, item := range items {
		if err := r.Context().Err(); err != nil {
			return // client disconnected
		}
		if err := enc.Encode(item); err != nil { // Encode writes the trailing newline
			s.logger.Debug("batch stream write failed", "error", err)
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
