package http

import (
	"context"
	"encoding/json"
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
	"github.com/jobrunner/ortus/internal/ports/output"
)

// serverWithDataset mirrors newTestServer but carries a dataset identity, which
// is the one input under test here.
func serverWithDataset(t *testing.T, info domain.DatasetInfo) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp",
	)
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil,
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger,
		application.QueryServiceConfig{})

	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080,
			ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{GazetteerDataset: info},
	)
}

func decodeSources(t *testing.T, info domain.DatasetInfo) map[string]any {
	t.Helper()
	srv := serverWithDataset(t, info)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestListSources_ReportsTheDatasetIdentity(t *testing.T) {
	body := decodeSources(t, domain.DatasetInfo{Version: "0.2.0", Built: "2026-08-23"})
	gz, ok := body["gazetteer"].(map[string]any)
	if !ok {
		t.Fatalf("no gazetteer block in the response: %v", body)
	}
	if gz["dataset_version"] != "0.2.0" {
		t.Errorf("dataset_version = %v, want 0.2.0", gz["dataset_version"])
	}
	if gz["built"] != "2026-08-23" {
		t.Errorf("built = %v, want 2026-08-23", gz["built"])
	}
}

func TestListSources_OmitsTheBlockForAPackageWithoutIdentity(t *testing.T) {
	// Packages built before dataset_version existed must not produce an empty
	// object: absent and "present but blank" mean different things to a consumer
	// deciding whether it can trust the value.
	body := decodeSources(t, domain.DatasetInfo{})
	if _, ok := body["gazetteer"]; ok {
		t.Errorf("block should be absent without an identity, got %v", body["gazetteer"])
	}
	if _, ok := body["sources"]; !ok {
		t.Error("the sources list itself must still be there")
	}
}

func TestListSources_OmitsOnlyTheMissingField(t *testing.T) {
	body := decodeSources(t, domain.DatasetInfo{Version: "0.3.0"})
	gz, ok := body["gazetteer"].(map[string]any)
	if !ok {
		t.Fatalf("no gazetteer block: %v", body)
	}
	if gz["dataset_version"] != "0.3.0" {
		t.Errorf("dataset_version = %v", gz["dataset_version"])
	}
	if _, ok := gz["built"]; ok {
		t.Error("an unset built date should be omitted, not reported empty")
	}
}
