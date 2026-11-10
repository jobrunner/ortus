package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/jobrunner/ortus/internal/domain"
)

// The query error → HTTP status mapping, shared by every query endpoint. Split
// out of handlers.go (like query_coordinates.go) to keep that file at its
// complexity baseline.

// clientQueryErrors maps the client-side query errors to their status, checked
// in order (a more specific sentinel before the one it wraps).
var clientQueryErrors = []struct {
	target error
	status int
	msg    string
}{
	{domain.ErrSourceNotFound, http.StatusNotFound, "Source not found"},
	{domain.ErrLayerNotFound, http.StatusNotFound, "Layer not found"},
	// Bad coordinate / SRID / other invalid input.
	{domain.ErrInvalidInput, http.StatusBadRequest, "Invalid query parameters"},
	// Unsupported projection or source kind.
	{domain.ErrUnsupported, http.StatusUnprocessableEntity, "Unsupported query"},
}

// clientQueryError returns the status and message for a client-side query
// error; ok is false for server-side failures.
func clientQueryError(err error) (status int, msg string, ok bool) {
	for _, c := range clientQueryErrors {
		if errors.Is(err, c.target) {
			return c.status, c.msg, true
		}
	}
	return 0, "", false
}

// handleQueryError maps a domain error to the appropriate HTTP status. The
// checks use the domain's sentinel base errors (ErrInvalidInput / ErrUnsupported
// / ErrUnavailable), so the specific errors that wrap them (ErrInvalidCoordinate,
// ErrInvalidSRID, ErrUnsupportedProjection, StorageError, …) are classified
// correctly instead of all falling through to 500.
func (s *Server) handleQueryError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	var storageErr *domain.StorageError
	if errors.As(err, &validationErr) {
		s.writeError(w, http.StatusBadRequest, validationErr.Message)
		return
	}
	if status, msg, ok := clientQueryError(err); ok {
		s.writeError(w, status, msg)
		return
	}
	switch {
	case errors.As(err, &storageErr), errors.Is(err, domain.ErrUnavailable):
		s.logger.Error("query unavailable", "error", err)
		s.writeError(w, http.StatusServiceUnavailable, "Service temporarily unavailable")
	case errors.Is(err, context.DeadlineExceeded):
		// The server's query.timeout elapsed — a real "too slow" failure, not empty success.
		s.logger.Warn("query timed out", "error", err)
		s.writeError(w, http.StatusGatewayTimeout, "Query timed out")
	case errors.Is(err, context.Canceled):
		// Client went away mid-query; nothing useful to send and not a server fault.
		// net/http has no reason phrase for the non-standard 499, so set the error
		// label explicitly (writeError would leave "error" empty via StatusText).
		s.logger.Debug("query canceled by client", "error", err)
		s.writeJSON(w, StatusClientClosedRequest, map[string]interface{}{
			"error":   "Client Closed Request",
			"message": "The request was canceled by the client",
		})
	case errors.Is(err, domain.ErrTransformFailed):
		// The coordinate transformer is shared by every query: if it fails here it
		// fails for all foreign-SRID sources, so no partial answer is possible.
		s.logger.Error("coordinate transformation failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Coordinate transformation failed")
	default:
		// QueryError / IndexError / unexpected — a server-side failure.
		s.logger.Error("query error", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Query failed")
	}
}
