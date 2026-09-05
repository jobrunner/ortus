package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
)

// The per-request opt-out switches for /query and /query/batch live here, out of
// the handler files, so the handlers stay at their complexity baseline (the same
// split that moved the coordinate parsing to query_coordinates.go).

// queryFlagOff reports whether the named query parameter carries an explicit
// falsy value (0/false/no/off, case-insensitive). Anything else — absent or
// unrecognized included — counts as "on": the /query switches are pure opt-out.
func queryFlagOff(r *http.Request, name string) bool {
	switch strings.ToLower(r.URL.Query().Get(name)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

// gazetteerEnrichmentRequested reports whether /query should attach the gazetteer
// block. Enrichment is ON by default when the feature is wired; a client opts out
// only with an explicit falsy with-gazetteer value to skip the extra
// Locate+Bearing spatial work.
func gazetteerEnrichmentRequested(r *http.Request) bool {
	return !queryFlagOff(r, "with-gazetteer")
}

// sourcesRequested reports whether /query should run the point-in-polygon query
// over the loaded source packages. ON by default; an explicit falsy with-sources
// value skips the PiP work entirely (e.g. for gazetteer-only lookups), leaving
// results empty. Independent of with-gazetteer.
func sourcesRequested(r *http.Request) bool {
	return !queryFlagOff(r, "with-sources")
}

// resolveQueryResponse runs the PiP query for /query — or, when with-sources=0,
// skips it and synthesizes an empty response so the payload keeps its shape
// (results: [], total_features: 0). The coordinate must still be validated in
// the skip path because QueryPoint (the usual validator) never runs.
func (s *Server) resolveQueryResponse(r *http.Request, req domain.QueryRequest) (*domain.QueryResponse, error) {
	if sourcesRequested(r) {
		return s.queryService.QueryPoint(r.Context(), req)
	}
	if err := req.Coordinate.Validate(); err != nil {
		return nil, err
	}
	return &domain.QueryResponse{Coordinate: req.Coordinate}, nil
}

// resolveBatchResponses is the batch counterpart: one response per valid
// coordinate, either from the set-based PiP query or — when "with-sources":
// false — synthesized empty so every item keeps its shape. Coordinates were
// already validated per point in resolveBatchInputs.
func (s *Server) resolveBatchResponses(ctx context.Context, req *batchRequest, valid []domain.Coordinate) ([]*domain.QueryResponse, error) {
	if !batchWantsSources(req) {
		sub := make([]*domain.QueryResponse, len(valid))
		for k, c := range valid {
			sub[k] = &domain.QueryResponse{Coordinate: c}
		}
		return sub, nil
	}
	return s.queryService.QueryBatch(ctx, valid, req.Sources, req.Properties)
}
