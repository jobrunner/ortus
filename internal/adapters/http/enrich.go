package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/jobrunner/ortus/internal/domain"
)

// wgs84OrLog reprojects the query coordinate to WGS84 for the response's wgs84 and
// gazetteer blocks. ok is false when the coordinate can't be used: either the SRID
// isn't transformable (a client concern — the caller silently omits the blocks) or
// the transform itself failed (an internal concern — logged here at WARN so it is
// diagnosable). Only a successful reprojection returns ok=true. Kept out of the
// handlers file so the /query handlers stay thin.
func (s *Server) wgs84OrLog(r *http.Request, coord domain.Coordinate) (domain.Coordinate, bool) {
	wgs, err := s.toWGS84(r.Context(), coord)
	switch {
	case err == nil:
		return wgs, true
	case errors.Is(err, errNotTransformable):
		return domain.Coordinate{}, false
	default:
		s.logger.Warn("wgs84 reprojection failed", "srid", coord.SRID, "error", err)
		return domain.Coordinate{}, false
	}
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
