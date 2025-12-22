package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// mockQueryService implements application.QueryService for testing.
type mockQueryService struct {
	queryResponse *domain.QueryResponse
	queryErr      error
}

func (m *mockQueryService) QueryPoint(_ context.Context, _ domain.QueryRequest) (*domain.QueryResponse, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.queryResponse, nil
}

func (m *mockQueryService) QueryPointInPackage(_ context.Context, _ string, _ domain.QueryRequest) (*domain.QueryResult, error) {
	return nil, nil
}

// mockPackageRegistry implements application.PackageRegistry for testing.
type mockPackageRegistry struct {
	packages   []domain.GeoPackage
	getPackage *domain.GeoPackage
	status     domain.GeoPackageStatus
	getErr     error
}

func (m *mockPackageRegistry) ListPackages(_ context.Context) ([]domain.GeoPackage, error) {
	return m.packages, nil
}

func (m *mockPackageRegistry) GetPackage(_ context.Context, _ string) (*domain.GeoPackage, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getPackage, nil
}

func (m *mockPackageRegistry) GetPackageStatus(_ context.Context, _ string) (domain.GeoPackageStatus, error) {
	return m.status, nil
}

func (m *mockPackageRegistry) ReadyPackageIDs() []string {
	var ids []string
	for _, pkg := range m.packages {
		if pkg.IsReady() {
			ids = append(ids, pkg.ID)
		}
	}
	return ids
}

func (m *mockPackageRegistry) IsReady(packageID string) bool {
	for _, pkg := range m.packages {
		if pkg.ID == packageID && pkg.IsReady() {
			return true
		}
	}
	return false
}

// mockHealthService implements application.HealthService for testing.
type mockHealthService struct {
	healthy bool
	ready   bool
}

func (m *mockHealthService) IsHealthy(_ context.Context) bool {
	return m.healthy
}

func (m *mockHealthService) IsReady(_ context.Context) bool {
	return m.ready
}

func (m *mockHealthService) GetHealthDetails(_ context.Context) *mockHealthDetails {
	return &mockHealthDetails{
		healthy: m.healthy,
		ready:   m.ready,
	}
}

type mockHealthDetails struct {
	healthy bool
	ready   bool
}

func newTestServer(_ *mockQueryService, _ *mockPackageRegistry, _ *mockHealthService) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create real services using mocks
	realRegistry := application.NewPackageRegistry(
		&mockRepository{},
		&mockStorage{},
		&output.NoOpMetrics{},
		logger,
		"/tmp",
	)

	realHealth := application.NewHealthService(realRegistry)
	realQuery := application.NewQueryService(
		realRegistry,
		&mockRepository{},
		nil,
		&output.NoOpMetrics{},
		logger,
		application.QueryServiceConfig{},
	)

	// Create server
	srv := NewServer(
		config.ServerConfig{
			Host:         "localhost",
			Port:         8080,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		realQuery,
		realRegistry,
		realHealth,
		nil, // No sync service for tests
		logger,
		false,
	)

	return srv
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want %q", resp["status"], "ok")
	}
}

func TestHandleLiveness(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want %q", resp["status"], "ok")
	}
}

func TestHandleReadiness(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	// Empty registry is ready
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleListPackages(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/packages", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["count"] != float64(0) {
		t.Errorf("count = %v, want 0", resp["count"])
	}
}

func TestHandleQueryMissingCoordinates(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleQueryInvalidCoordinates(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	tests := []struct {
		name string
		url  string
	}{
		{"invalid lon", "/api/v1/query?lon=abc&lat=50"},
		{"invalid lat", "/api/v1/query?lon=10&lat=abc"},
		{"invalid x", "/api/v1/query?x=abc&y=50"},
		{"invalid y", "/api/v1/query?x=10&y=abc"},
		{"invalid srid", "/api/v1/query?lon=10&lat=50&srid=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()

			srv.router.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleQueryValidCoordinates(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	tests := []struct {
		name string
		url  string
	}{
		{"lon/lat", "/api/v1/query?lon=10&lat=50"},
		{"x/y", "/api/v1/query?x=500000&y=5700000&srid=25832"},
		{"with srid", "/api/v1/query?lon=10&lat=50&srid=4326"},
		{"with properties filter", "/api/v1/query?lon=10&lat=50&properties=name,type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()

			srv.router.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if _, ok := resp["coordinate"]; !ok {
				t.Error("response should contain coordinate")
			}
			if _, ok := resp["results"]; !ok {
				t.Error("response should contain results")
			}
		})
	}
}

func TestHandleGetPackageNotFound(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/packages/nonexistent", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleGetLayersNotFound(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/packages/nonexistent/layers", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleQueryPackageNotFound(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query/nonexistent?lon=10&lat=50", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleOpenAPI(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rr := httptest.NewRecorder()

	srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestParseQueryParams(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	tests := []struct {
		name    string
		url     string
		wantErr bool
		check   func(*QueryParams) error
	}{
		{
			name: "lon/lat coordinates",
			url:  "/query?lon=9.9&lat=52.5",
			check: func(p *QueryParams) error {
				if p.Lon != 9.9 {
					return domain.ErrInvalidCoordinate
				}
				if p.Lat != 52.5 {
					return domain.ErrInvalidCoordinate
				}
				return nil
			},
		},
		{
			name: "x/y coordinates",
			url:  "/query?x=500000&y=5700000",
			check: func(p *QueryParams) error {
				if p.X != 500000 {
					return domain.ErrInvalidCoordinate
				}
				if p.Y != 5700000 {
					return domain.ErrInvalidCoordinate
				}
				return nil
			},
		},
		{
			name: "custom SRID",
			url:  "/query?lon=10&lat=50&srid=25832",
			check: func(p *QueryParams) error {
				if p.SRID != 25832 {
					return domain.ErrInvalidSRID
				}
				return nil
			},
		},
		{
			name: "default SRID",
			url:  "/query?lon=10&lat=50",
			check: func(p *QueryParams) error {
				if p.SRID != domain.SRIDWGS84 {
					return domain.ErrInvalidSRID
				}
				return nil
			},
		},
		{
			name: "properties filter",
			url:  "/query?lon=10&lat=50&properties=name,type,area",
			check: func(p *QueryParams) error {
				if len(p.Properties) != 3 {
					return domain.ErrInvalidInput
				}
				return nil
			},
		},
		{
			name:    "missing coordinates",
			url:     "/query",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			params, err := srv.parseQueryParams(req)

			if (err != nil) != tt.wantErr {
				t.Errorf("parseQueryParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.check != nil {
				if err := tt.check(params); err != nil {
					t.Errorf("check failed: %v", err)
				}
			}
		})
	}
}

func TestBoolToStatus(t *testing.T) {
	if boolToStatus(true) != "ok" {
		t.Error("boolToStatus(true) should return 'ok'")
	}
	if boolToStatus(false) != "unhealthy" {
		t.Error("boolToStatus(false) should return 'unhealthy'")
	}
}

// Mock implementations for testing

type mockRepository struct{}

func (m *mockRepository) Open(_ context.Context, path string) (*domain.GeoPackage, error) {
	return &domain.GeoPackage{ID: path, Name: path, Path: path}, nil
}

func (m *mockRepository) Close(_ context.Context, _ string) error {
	return nil
}

func (m *mockRepository) GetLayers(_ context.Context, _ string) ([]domain.Layer, error) {
	return nil, nil
}

func (m *mockRepository) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}

func (m *mockRepository) CreateSpatialIndex(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockRepository) HasSpatialIndex(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

type mockStorage struct{}

func (m *mockStorage) List(_ context.Context) ([]output.StorageObject, error) {
	return nil, nil
}

func (m *mockStorage) Download(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockStorage) GetReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockStorage) Exists(_ context.Context, _ string) (bool, error) {
	return true, nil
}
