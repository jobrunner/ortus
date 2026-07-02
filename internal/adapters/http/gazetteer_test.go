package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeGazetteer is a canned input.Gazetteer for handler tests.
type fakeGazetteer struct {
	loc    *domain.Locality
	locErr error
	fix    *domain.Fix
	fixErr error
}

func (f fakeGazetteer) Locate(context.Context, domain.Coordinate) (*domain.Locality, error) {
	return f.loc, f.locErr
}
func (f fakeGazetteer) Bearing(context.Context, domain.Coordinate, domain.BearingPolicy) (*domain.Fix, error) {
	return f.fix, f.fixErr
}

func newGazetteerServer(t *testing.T, gaz input.Gazetteer) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp",
	)
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("test"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})

	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{Gazetteer: gaz},
	)
}

func sampleLocality() *domain.Locality {
	return &domain.Locality{CountryISO: "DE", Chain: []domain.AdminUnit{
		{Level: 8, Name: "Würzburg", Equivalent: "municipality"},
		{Level: 4, Name: "Bayern", Equivalent: "state"},
	}}
}

func sampleFix() *domain.Fix {
	return &domain.Fix{
		Reference:  domain.Place{Name: "Würzburg", Class: domain.ClassCity},
		DistanceKM: 4, Azimuth: 90, Compass: "E", Label: "4 km E Würzburg",
	}
}

func doGET(t *testing.T, srv *Server, path string) (rec *httptest.ResponseRecorder, body map[string]any) {
	t.Helper()
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec, body
}

func TestGazetteerEndpoint(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	admin, ok := body["admin"].(map[string]any)
	if !ok || admin["country_iso"] != "DE" {
		t.Errorf("admin = %v, want country_iso DE", body["admin"])
	}
	bearing, ok := body["bearing"].(map[string]any)
	if !ok || bearing["label"] != "4 km E Würzburg" {
		t.Errorf("bearing = %v, want label '4 km E Würzburg'", body["bearing"])
	}
}

func TestGazetteerEndpointPartial(t *testing.T) {
	// No admin coverage (ErrNotFound) but a bearing exists → admin null, bearing set.
	srv := newGazetteerServer(t, fakeGazetteer{locErr: domain.ErrNotFound, fix: sampleFix()})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["admin"] != nil {
		t.Errorf("admin = %v, want null", body["admin"])
	}
	if _, ok := body["bearing"].(map[string]any); !ok {
		t.Errorf("bearing missing, want set")
	}
}

func TestGazetteerEndpointError(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{locErr: errors.New("db down")})
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestGazetteerRouteAbsentWhenDisabled(t *testing.T) {
	srv := newGazetteerServer(t, nil) // no gazetteer wired
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not registered)", rec.Code)
	}
}

func TestQueryWithGazetteerFlag(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})

	// Opt-in flag → gazetteer section present.
	_, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79&with-gazetteer=1")
	if _, ok := body["gazetteer"].(map[string]any); !ok {
		t.Errorf("with-gazetteer=1: gazetteer section missing")
	}

	// Default (no flag) → no gazetteer section.
	_, body = doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79")
	if _, present := body["gazetteer"]; present {
		t.Errorf("without flag: gazetteer section should be absent")
	}
}

func TestIsTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on"} {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "2"} {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q) = true, want false", v)
		}
	}
}
