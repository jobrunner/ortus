package http

import (
	"context"
	"encoding/json"
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
		{"deadline exceeded", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"canceled", context.Canceled, StatusClientClosedRequest},
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

// TestHandleQueryErrorCanceledBodyLabel guards the non-standard 499 response: it
// must carry a non-empty "error" label (http.StatusText(499) is "", so a naive
// writeError would emit "error":"" and make client-side handling ambiguous).
func TestHandleQueryErrorCanceledBodyLabel(t *testing.T) {
	srv := newTestServer(nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.handleQueryError(rr, context.Canceled)

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Error == "" {
		t.Errorf("canceled response has empty \"error\" label; body = %s", rr.Body.String())
	}
	if body.Message == "" {
		t.Error("canceled response should include a message")
	}
}
