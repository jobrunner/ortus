package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/jobrunner/ortus/internal/domain"
)

// QueryParams represents the query parameters for a point query.
type QueryParams struct {
	Lon        float64  `json:"lon"`
	Lat        float64  `json:"lat"`
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	SRID       int      `json:"srid"`
	Properties []string `json:"properties,omitempty"`
}

// handleQuery handles point queries across all sources.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	params, err := s.parseQueryParams(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req := domain.QueryRequest{
		Coordinate: s.paramsToCoordinate(params),
		SourceSRID: params.SRID,
		Properties: params.Properties,
	}

	response, err := s.queryService.QueryPoint(r.Context(), req)
	if err != nil {
		s.handleQueryError(w, err)
		return
	}

	out := s.formatQueryResponse(response)
	// Gazetteer enrichment is ON by default when the feature is wired, so a client
	// gets the admin hierarchy, bearing, name-source explanations and dataset
	// attribution in the same call. Opt out with with-gazetteer=0 (or false/no/off)
	// to skip the extra spatial work. Only attempted for WGS84 coordinates — the
	// gazetteer dataset is EPSG:4326, so a non-4326 srid query would only fail and
	// log; it is silently skipped instead. Best-effort: a gazetteer failure is
	// logged and omitted so it never breaks the core query result.
	if s.gazetteer != nil && gazetteerEnrichmentRequested(r) && isWGS84(req.Coordinate) {
		if g, gerr := s.gazetteerSections(r.Context(), req.Coordinate); gerr != nil {
			s.logger.Warn("gazetteer enrichment failed", "error", gerr)
		} else {
			out["gazetteer"] = g
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}

// handleQuerySource handles point queries for a specific source.
func (s *Server) handleQuerySource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sourceID := vars["sourceId"]

	params, err := s.parseQueryParams(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req := domain.QueryRequest{
		Coordinate: s.paramsToCoordinate(params),
		SourceSRID: params.SRID,
		Properties: params.Properties,
		SourceID:   sourceID,
	}

	response, err := s.queryService.QueryPoint(r.Context(), req)
	if err != nil {
		s.handleQueryError(w, err)
		return
	}

	s.writeJSON(w, http.StatusOK, s.formatQueryResponse(response))
}

// handleHealth returns detailed health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	details := s.health.GetHealthDetails(r.Context())

	status := http.StatusOK
	if !details.Healthy {
		status = http.StatusServiceUnavailable
	}

	s.writeJSON(w, status, map[string]interface{}{
		"status":         boolToStatus(details.Healthy),
		"ready":          details.Ready,
		"sources_loaded": details.SourcesLoaded,
		"sources_ready":  details.SourcesReady,
		"sources":        details.Sources,
		"components":     details.Components,
	})
}

// handleLiveness returns liveness status.
func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	if s.health.IsHealthy(r.Context()) {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	} else {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
	}
}

// handleReadiness returns readiness status.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if s.health.IsReady(r.Context()) {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	} else {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
	}
}

// handleListSources returns all registered sources.
func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.registry.ListSources(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list sources")
		return
	}

	response := make([]map[string]interface{}, len(sources))
	for i := range sources {
		response[i] = s.formatSource(&sources[i])
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"sources": response,
		"count":   len(sources),
	})
}

// handleGetSource returns a specific source.
func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sourceID := vars["sourceId"]

	pkg, err := s.registry.GetSource(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, domain.ErrSourceNotFound) {
			s.writeError(w, http.StatusNotFound, "Source not found")
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to get source")
		return
	}

	s.writeJSON(w, http.StatusOK, s.formatSource(pkg))
}

// handleGetLayers returns layers for a specific source.
func (s *Server) handleGetLayers(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sourceID := vars["sourceId"]

	pkg, err := s.registry.GetSource(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, domain.ErrSourceNotFound) {
			s.writeError(w, http.StatusNotFound, "Source not found")
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to get source")
		return
	}

	layers := make([]map[string]interface{}, len(pkg.Layers))
	for i, l := range pkg.Layers {
		layers[i] = map[string]interface{}{
			"name":            l.Name,
			"description":     l.Description,
			"geometry_type":   l.GeometryType,
			"geometry_column": l.GeometryColumn,
			"srid":            l.SRID,
			"has_index":       l.HasIndex,
			"feature_count":   l.FeatureCount,
		}
		if l.Extent != nil {
			layers[i]["extent"] = map[string]interface{}{
				"min_x": l.Extent.MinX,
				"min_y": l.Extent.MinY,
				"max_x": l.Extent.MaxX,
				"max_y": l.Extent.MaxY,
			}
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"source_id": sourceID,
		"layers":    layers,
		"count":     len(layers),
	})
}

// handleOpenAPI returns the OpenAPI specification.
func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	spec, err := getOpenAPIJSON()
	if err != nil {
		s.logger.Error("failed to get OpenAPI spec", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to load OpenAPI specification")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(spec)
}

// parseQueryParams parses query parameters from the request.
func (s *Server) parseQueryParams(r *http.Request) (*QueryParams, error) {
	params := &QueryParams{
		SRID: domain.SRIDWGS84, // Default
	}

	q := r.URL.Query()

	// Parse coordinates (lon/lat or x/y)
	if lon := q.Get("lon"); lon != "" {
		v, err := strconv.ParseFloat(lon, 64)
		if err != nil {
			return nil, errors.New("invalid lon parameter")
		}
		params.Lon = v
	}

	if lat := q.Get("lat"); lat != "" {
		v, err := strconv.ParseFloat(lat, 64)
		if err != nil {
			return nil, errors.New("invalid lat parameter")
		}
		params.Lat = v
	}

	if x := q.Get("x"); x != "" {
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return nil, errors.New("invalid x parameter")
		}
		params.X = v
	}

	if y := q.Get("y"); y != "" {
		v, err := strconv.ParseFloat(y, 64)
		if err != nil {
			return nil, errors.New("invalid y parameter")
		}
		params.Y = v
	}

	// Validate that we have coordinates
	if params.Lon == 0 && params.Lat == 0 && params.X == 0 && params.Y == 0 {
		return nil, errors.New("coordinates required: use lon/lat or x/y")
	}

	// Parse SRID
	if srid := q.Get("srid"); srid != "" {
		v, err := strconv.Atoi(srid)
		if err != nil {
			return nil, errors.New("invalid srid parameter")
		}
		params.SRID = v
	}

	// Parse properties filter
	if props := q.Get("properties"); props != "" {
		params.Properties = strings.Split(props, ",")
	}

	return params, nil
}

// paramsToCoordinate converts query params to a coordinate.
func (s *Server) paramsToCoordinate(params *QueryParams) domain.Coordinate {
	// Prefer lon/lat if both are set
	if params.Lon != 0 || params.Lat != 0 {
		return domain.Coordinate{
			X:    params.Lon,
			Y:    params.Lat,
			SRID: params.SRID,
		}
	}
	return domain.Coordinate{
		X:    params.X,
		Y:    params.Y,
		SRID: params.SRID,
	}
}

// formatQueryResponse formats the query response for JSON output.
func (s *Server) formatQueryResponse(resp *domain.QueryResponse) map[string]interface{} {
	results := make([]map[string]interface{}, len(resp.Results))
	for i := range resp.Results {
		r := &resp.Results[i]
		features := make([]map[string]interface{}, len(r.Features))
		for j := range r.Features {
			f := &r.Features[j]
			features[j] = map[string]interface{}{
				"id":         f.ID,
				"layer":      f.LayerName,
				"properties": f.Properties,
			}
			// Only include geometry if explicitly enabled via --with-geometry or ORTUS_RESULTS_WITH_GEOMETRY
			if s.withGeometry && f.Geometry.WKT != "" {
				features[j]["geometry"] = map[string]interface{}{
					"type": f.Geometry.Type,
					"wkt":  f.Geometry.WKT,
				}
			}
		}

		results[i] = map[string]interface{}{
			"source_id":     r.SourceID,
			"source_name":   r.SourceName,
			"features":      features,
			"feature_count": r.FeatureCount(),
			"query_time_ms": r.QueryTime.Milliseconds(),
		}

		if !r.License.IsEmpty() {
			results[i]["license"] = map[string]interface{}{
				"name":        r.License.Name,
				"url":         r.License.URL,
				"attribution": r.License.Attribution,
			}
		}
	}

	return map[string]interface{}{
		"coordinate": map[string]interface{}{
			"x":    resp.Coordinate.X,
			"y":    resp.Coordinate.Y,
			"srid": resp.Coordinate.SRID,
		},
		"results":            results,
		"total_features":     resp.TotalFeatures,
		"processing_time_ms": resp.ProcessingTime.Milliseconds(),
	}
}

// formatSource formats a source for JSON output.
func (s *Server) formatSource(pkg *domain.Source) map[string]interface{} {
	return map[string]interface{}{
		"id":           pkg.ID,
		"name":         pkg.Name,
		"path":         pkg.Path,
		"size":         pkg.Size,
		"layer_count":  pkg.LayerCount(),
		"indexed":      pkg.Indexed,
		"ready":        pkg.IsReady(),
		"loaded_at":    pkg.LoadedAt,
		"last_queried": pkg.LastQueried,
	}
}

// handleQueryError maps a domain error to the appropriate HTTP status. The
// checks use the domain's sentinel base errors (ErrInvalidInput / ErrUnsupported
// / ErrUnavailable), so the specific errors that wrap them (ErrInvalidCoordinate,
// ErrInvalidSRID, ErrUnsupportedProjection, StorageError, …) are classified
// correctly instead of all falling through to 500.
func (s *Server) handleQueryError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	var storageErr *domain.StorageError
	switch {
	case errors.As(err, &validationErr):
		s.writeError(w, http.StatusBadRequest, validationErr.Message)
	case errors.Is(err, domain.ErrSourceNotFound):
		s.writeError(w, http.StatusNotFound, "Source not found")
	case errors.Is(err, domain.ErrLayerNotFound):
		s.writeError(w, http.StatusNotFound, "Layer not found")
	case errors.Is(err, domain.ErrInvalidInput):
		// Bad coordinate / SRID / other invalid input.
		s.writeError(w, http.StatusBadRequest, "Invalid query parameters")
	case errors.Is(err, domain.ErrUnsupported):
		// Unsupported projection or source kind.
		s.writeError(w, http.StatusUnprocessableEntity, "Unsupported query")
	case errors.As(err, &storageErr), errors.Is(err, domain.ErrUnavailable):
		s.logger.Error("query unavailable", "error", err)
		s.writeError(w, http.StatusServiceUnavailable, "Service temporarily unavailable")
	default:
		// QueryError / IndexError / unexpected — a server-side failure.
		s.logger.Error("query error", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Query failed")
	}
}

// writeJSON writes a JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError writes an error response.
func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]interface{}{
		"error":   http.StatusText(status),
		"message": message,
	})
}

func boolToStatus(b bool) string {
	if b {
		return "ok"
	}
	return "unhealthy"
}

// handleSync handles the sync trigger endpoint.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if s.syncService == nil {
		s.writeError(w, http.StatusNotFound, "Sync service not available")
		return
	}

	result, err := s.syncService.TriggerSync(r.Context())
	if err != nil {
		if errors.Is(err, domain.ErrRateLimited) {
			w.Header().Set("Retry-After", "30")
			s.writeError(w, http.StatusTooManyRequests, "Rate limit exceeded. Try again in 30 seconds.")
			return
		}
		s.logger.Error("sync failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Sync failed")
		return
	}

	s.writeJSON(w, http.StatusOK, result)
}
