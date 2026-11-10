package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/jobrunner/ortus/internal/domain"
)

// wgs84For reprojects the query coordinate to WGS84 for the response's wgs84 and
// gazetteer blocks. ok is false with a nil error when no transformer is wired —
// the caller omits both blocks. An SRID the transformer doesn't know is invalid
// input (domain.UnknownSRIDError → 400). A failed transform is returned as an
// error (wrapping domain.ErrTransformFailed) and must fail the request: omitting
// the blocks silently is how a broken transformer went unnoticed. Kept out of the
// handlers file so the /query handlers stay thin.
func (s *Server) wgs84For(r *http.Request, coord domain.Coordinate) (domain.Coordinate, bool, error) {
	wgs, err := s.toWGS84(r.Context(), coord)
	switch {
	case err == nil:
		return wgs, true, nil
	case errors.Is(err, errNotTransformable) && s.transformer != nil:
		return domain.Coordinate{}, false, domain.UnknownSRIDError(coord.SRID)
	case errors.Is(err, errNotTransformable):
		return domain.Coordinate{}, false, nil
	default:
		return domain.Coordinate{}, false, err
	}
}

// queryResponseBody renders a /query response: the query result plus the wgs84
// block (on every query response, single-source too) and, when withGazetteer,
// the gazetteer block. Shared by both /query handlers so the reprojection and
// its error handling live in one place.
func (s *Server) queryResponseBody(r *http.Request, coord domain.Coordinate, response *domain.QueryResponse, withGazetteer bool) (map[string]interface{}, error) {
	// Reproject the query point to WGS84 once: it powers the wgs84 block (a
	// geographic coordinate other services can compute with / store) and the
	// gazetteer enrichment — the gazetteer dataset is EPSG:4326, so a non-4326
	// query is reprojected here rather than skipped.
	wgs, ok, err := s.wgs84For(r, coord)
	if err != nil {
		return nil, err
	}
	out := s.formatQueryResponse(response)
	if ok {
		out["wgs84"] = wgs84Block(wgs)
		if withGazetteer {
			s.attachGazetteer(r, wgs, out)
		}
	}
	return out, nil
}

// attachGazetteer adds the best-effort gazetteer block to a /query response for the
// (already WGS84) coordinate. Enrichment is ON by default when the feature is wired;
// a client opts out with with-gazetteer=0 (false/no/off). A failure is logged and
// the block omitted so it never breaks the core query result. A client-aborted
// request (context.Canceled) is expected and logged at Debug, not Warn.
func (s *Server) attachGazetteer(r *http.Request, wgs domain.Coordinate, out map[string]interface{}) {
	if s.gazetteer == nil || !gazetteerEnrichmentRequested(r) {
		return
	}
	g, err := s.gazetteerSections(r.Context(), wgs)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("gazetteer enrichment canceled", "error", err)
		} else {
			s.logger.Warn("gazetteer enrichment failed", "error", err)
		}
		return
	}
	out["gazetteer"] = g
}
