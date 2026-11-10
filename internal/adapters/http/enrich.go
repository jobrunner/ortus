package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/jobrunner/ortus/internal/domain"
)

// wgs84For reprojects the query coordinate to WGS84 for the response's wgs84 and
// gazetteer blocks. ok is false with a nil error when the SRID isn't
// transformable (no transformer wired) — the caller omits both blocks. A failed
// transform is returned as an error (wrapping domain.ErrTransformFailed) and must
// fail the request: omitting the blocks silently is how a broken transformer
// went unnoticed. Kept out of the handlers file so the /query handlers stay thin.
func (s *Server) wgs84For(r *http.Request, coord domain.Coordinate) (domain.Coordinate, bool, error) {
	wgs, err := s.toWGS84(r.Context(), coord)
	switch {
	case err == nil:
		return wgs, true, nil
	case errors.Is(err, errNotTransformable):
		return domain.Coordinate{}, false, nil
	default:
		return domain.Coordinate{}, false, err
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
