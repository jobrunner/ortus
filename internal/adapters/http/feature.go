package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/jobrunner/ortus/internal/domain"
)

// The feature geometry endpoint. In its own file (like with_sources.go) to keep
// handlers.go at its complexity baseline.

// contentTypeGeoJSON is the media type of a GeoJSON document (RFC 7946).
const contentTypeGeoJSON = "application/geo+json"

// featureGeometryResponse is a GeoJSON Feature carrying the complete outline of
// one feature, plus foreign members (allowed by RFC 7946) that tell a map client
// where it came from and how it may be used.
type featureGeometryResponse struct {
	Type       string                 `json:"type"`
	ID         int64                  `json:"id"`
	BBox       [4]float64             `json:"bbox"`
	Geometry   json.RawMessage        `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
	SourceID   string                 `json:"source_id"`
	Layer      string                 `json:"layer"`
	// SRID of geometry and bbox. RFC 7946 knows WGS84 only; another SRID is a
	// deliberate, labeled deviation.
	SRID    int                    `json:"srid"`
	License map[string]interface{} `json:"license,omitempty"`
}

// handleGetFeature returns one feature of a vector source as a GeoJSON Feature:
// its complete outline (MultiPolygon), bounding box and the package license, in
// the projection asked for with ?srid (default EPSG:4326).
func (s *Server) handleGetFeature(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	fid, err := strconv.ParseInt(vars["featureId"], 10, 64)
	if err != nil || fid <= 0 {
		s.writeError(w, http.StatusBadRequest, "Invalid feature id: must be a positive integer")
		return
	}
	srid := domain.SRIDWGS84
	if raw := r.URL.Query().Get("srid"); raw != "" {
		if srid, err = strconv.Atoi(raw); err != nil || srid <= 0 {
			s.writeError(w, http.StatusBadRequest, "Invalid srid parameter: must be an EPSG code")
			return
		}
	}

	fg, err := s.queryService.FeatureGeometry(r.Context(), domain.FeatureRef{
		SourceID: vars["sourceId"],
		Layer:    vars["layer"],
		ID:       fid,
		SRID:     srid,
	})
	if err != nil {
		s.handleQueryError(w, err)
		return
	}

	w.Header().Set("Content-Type", contentTypeGeoJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(formatFeatureGeometry(fg))
}

// formatFeatureGeometry maps the domain result to its GeoJSON form. The geometry
// is embedded as delivered by the projector, not re-encoded.
func formatFeatureGeometry(fg *domain.FeatureGeometry) featureGeometryResponse {
	out := featureGeometryResponse{
		Type:       "Feature",
		ID:         fg.ID,
		BBox:       [4]float64{fg.Extent.MinX, fg.Extent.MinY, fg.Extent.MaxX, fg.Extent.MaxY},
		Geometry:   json.RawMessage(fg.GeoJSON),
		Properties: fg.Properties,
		SourceID:   fg.SourceID,
		Layer:      fg.Layer,
		SRID:       fg.Extent.SRID,
	}
	if out.Properties == nil {
		// GeoJSON requires the member; an empty object is friendlier than null.
		out.Properties = map[string]interface{}{}
	}
	if !fg.License.IsEmpty() {
		out.License = map[string]interface{}{
			"name":        fg.License.Name,
			"url":         fg.License.URL,
			"attribution": fg.License.Attribution,
		}
	}
	return out
}
