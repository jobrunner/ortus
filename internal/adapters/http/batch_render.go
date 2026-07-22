package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	if !req.WithGazetteer || s.gazetteer == nil {
		return nil
	}
	ctx := r.Context()
	out := make([]map[string]interface{}, len(wgs))
	sem := make(chan struct{}, s.batchConcurrency)
	var wg sync.WaitGroup
	for i := range wgs {
		if itemErr[i] != "" || !wgsOK[i] {
			continue
		}
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
