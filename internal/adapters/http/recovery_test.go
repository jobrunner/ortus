package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRecoveryMiddleware ensures a panic in a downstream handler is converted
// into a 500 response instead of crashing the connection — the safety net is
// only useful if it actually fires (tech-debt D5).
func TestRecoveryMiddleware(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	h := s.recoveryMiddleware(panicking)

	rr := httptest.NewRecorder()
	// Must not propagate the panic out of ServeHTTP.
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

// TestRecoveryMiddleware_NoPanicPassesThrough confirms the middleware is
// transparent on the happy path.
func TestRecoveryMiddleware_NoPanicPassesThrough(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (middleware altered a non-panicking response)", rr.Code)
	}
}
