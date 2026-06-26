package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// TestHandleQueryErrorStatusMapping pins the domain-error → HTTP-status table so
// a regression in handleQueryError (e.g. an invalid-input error silently going
// back to 500) is caught.
func TestHandleQueryErrorStatusMapping(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"validation", &domain.ValidationError{Message: "bad"}, http.StatusBadRequest},
		{"source not found", domain.ErrSourceNotFound, http.StatusNotFound},
		{"layer not found", domain.ErrLayerNotFound, http.StatusNotFound},
		{"invalid coordinate", domain.ErrInvalidCoordinate, http.StatusBadRequest},
		{"invalid srid", domain.ErrInvalidSRID, http.StatusBadRequest},
		{"unsupported projection", domain.ErrUnsupportedProjection, http.StatusUnprocessableEntity},
		{"unsupported source", domain.ErrUnsupportedSource, http.StatusUnprocessableEntity},
		{"storage error", &domain.StorageError{Operation: "download", Err: errors.New("io")}, http.StatusServiceUnavailable},
		{"storage unavailable", domain.ErrStorageUnavailable, http.StatusServiceUnavailable},
		{"query error", &domain.QueryError{SourceID: "s", Err: errors.New("boom")}, http.StatusInternalServerError},
		{"unexpected", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.handleQueryError(rr, tt.err)
			if rr.Code != tt.want {
				t.Errorf("handleQueryError(%v) status = %d, want %d", tt.err, rr.Code, tt.want)
			}
		})
	}
}
