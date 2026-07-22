package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
)

// batchRequest is the POST /api/v1/query/batch body.
type batchRequest struct {
	SRID          int          `json:"srid"`           // default SRID for points without their own
	Sources       []string     `json:"sources"`        // optional: restrict to these source ids
	Properties    []string     `json:"properties"`     // optional: only these feature properties
	WithGazetteer bool         `json:"with-gazetteer"` // opt-in gazetteer enrichment (default off for batch)
	Points        []batchPoint `json:"points"`
}

// batchPoint is one coordinate with an optional caller-chosen echo id. Coordinate
// fields are pointers so a genuine 0 (e.g. lon 0) is distinguishable from "unset".
type batchPoint struct {
	ID   string   `json:"id"`
	Lon  *float64 `json:"lon"`
	Lat  *float64 `json:"lat"`
	X    *float64 `json:"x"`
	Y    *float64 `json:"y"`
	SRID *int     `json:"srid"`
}

// firstPositive returns v if positive, else def — for optional int knobs.
func firstPositive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// coordinate resolves the point to a domain.Coordinate, or returns a non-empty
// error message (per-item, so the batch continues). lon/lat wins over x/y.
func (p batchPoint) coordinate(defaultSRID int) (coord domain.Coordinate, errMsg string) {
	srid := defaultSRID
	if p.SRID != nil {
		srid = *p.SRID
	}
	if srid == 0 {
		srid = domain.SRIDWGS84
	}
	var c domain.Coordinate
	switch {
	case p.Lon != nil && p.Lat != nil:
		c = domain.Coordinate{X: *p.Lon, Y: *p.Lat, SRID: srid}
	case p.X != nil && p.Y != nil:
		c = domain.Coordinate{X: *p.X, Y: *p.Y, SRID: srid}
	default:
		return domain.Coordinate{}, "coordinates required: provide lon/lat or x/y"
	}
	if err := c.Validate(); err != nil {
		return domain.Coordinate{}, err.Error()
	}
	return c, ""
}

func (p batchPoint) idOr(index int) string {
	if p.ID != "" {
		return p.ID
	}
	return strconv.Itoa(index)
}

// handleQueryBatch resolves many coordinates in one request. Point-in-polygon is
// set-based (one query per source); optional gazetteer enrichment runs per point
// over a small bounded pool. Delivers a sync JSON object by default, or NDJSON
// (one result object per line) when the client sends Accept: application/x-ndjson.
func (s *Server) handleQueryBatch(w http.ResponseWriter, r *http.Request) {
	// Bound the request body so a hostile/huge payload can't force large
	// allocations before the point-count caps even apply (~512 B/point + headroom).
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.batchMaxPoints)*512+64*1024)
	req, err := parseBatchRequest(r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "request body too large — send fewer points or smaller per-point fields (e.g. shorter id values); NDJSON streaming only affects the response, not the request-body limit")
			return
		}
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Points) == 0 {
		s.writeError(w, http.StatusBadRequest, "points required: provide at least one point")
		return
	}
	if len(req.Points) > s.batchMaxPoints {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("batch of %d points exceeds the limit of %d", len(req.Points), s.batchMaxPoints))
		return
	}
	stream := prefersNDJSON(r)
	if !stream && len(req.Points) > s.batchMaxSync {
		s.writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"batch of %d points exceeds the sync limit of %d — retry with 'Accept: application/x-ndjson' to stream",
			len(req.Points), s.batchMaxSync))
		return
	}

	in := s.resolveBatchInputs(r, req)

	start := time.Now()
	sub, err := s.queryService.QueryBatch(r.Context(), in.valid, req.Sources, req.Properties)
	if err != nil {
		s.handleQueryError(w, err) // e.g. unknown source → 404
		return
	}
	if len(sub) != len(in.valid) {
		// Invariant: QueryBatch returns one response per input coordinate. Guard so a
		// future divergence fails cleanly instead of panicking on the scatter below.
		s.writeError(w, http.StatusInternalServerError, "batch query returned an unexpected result count")
		return
	}
	responses := make([]*domain.QueryResponse, len(req.Points))
	for k, origIdx := range in.validIdx {
		responses[origIdx] = sub[k]
	}

	items := s.buildBatchItems(r, req, in.wgs, in.wgsOK, responses, in.itemErr)
	if stream {
		s.streamBatchItems(w, r, items)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"results":            items,
		"total":              len(items),
		"processing_time_ms": time.Since(start).Milliseconds(),
	})
}

// batchInputs holds the per-point resolution: item error (if any), the shared
// WGS84 reprojection, and the compacted list of valid coords (+ their original
// index) that go to QueryBatch.
type batchInputs struct {
	itemErr  []string
	wgs      []domain.Coordinate
	wgsOK    []bool
	valid    []domain.Coordinate
	validIdx []int
}

// resolveBatchInputs resolves each point once: its coordinate, its WGS84
// reprojection (shared by the wgs84 block AND gazetteer enrichment, so reprojection
// isn't done twice), and any per-item error. Only valid points are collected for
// QueryBatch — invalid ones never reach the query path and surface as item errors.
func (s *Server) resolveBatchInputs(r *http.Request, req *batchRequest) batchInputs {
	n := len(req.Points)
	in := batchInputs{
		itemErr: make([]string, n),
		wgs:     make([]domain.Coordinate, n),
		wgsOK:   make([]bool, n),
	}
	for i, p := range req.Points {
		c, e := p.coordinate(req.SRID)
		in.itemErr[i] = e
		if e != "" {
			continue
		}
		in.wgs[i], in.wgsOK[i] = s.wgs84OrLog(r, c)
		in.valid = append(in.valid, c)
		in.validIdx = append(in.validIdx, i)
	}
	return in
}

// parseBatchRequest decodes and lightly validates the JSON body.
func parseBatchRequest(r *http.Request) (*batchRequest, error) {
	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return &req, nil
}

// prefersNDJSON reports whether the client asked for the streaming media type.
func prefersNDJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
}

// buildBatchItems assembles one response item per input point (in order): the
// per-source PiP result + echo id + the wgs84 block, plus the gazetteer block when
// enrichment was requested. A per-point resolution error becomes an error object.
func (s *Server) buildBatchItems(r *http.Request, req *batchRequest, wgs []domain.Coordinate, wgsOK []bool, responses []*domain.QueryResponse, itemErr []string) []map[string]interface{} {
	gaz := s.batchGazetteer(r, req, wgs, wgsOK, itemErr)
	items := make([]map[string]interface{}, len(req.Points))
	for i := range req.Points {
		id := req.Points[i].idOr(i)
		if itemErr[i] != "" {
			items[i] = map[string]interface{}{"id": id, "error": map[string]interface{}{"message": itemErr[i]}}
			continue
		}
		item := s.formatQueryResponse(responses[i])
		// The batch reports processing_time_ms once at the top level; drop the
		// per-item copy (the single-point formatter adds it) so each item matches
		// the BatchQueryResultItem schema.
		delete(item, "processing_time_ms")
		item["id"] = id
		if wgsOK[i] {
			item["wgs84"] = wgs84Block(wgs[i])
		}
		if len(gaz) > i && gaz[i] != nil {
			item["gazetteer"] = gaz[i]
		}
		items[i] = item
	}
	return items
}
