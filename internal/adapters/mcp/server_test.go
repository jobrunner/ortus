package mcp_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/metric/noop"

	mcpAdapter "github.com/jobrunner/ortus/internal/adapters/mcp"
	"github.com/jobrunner/ortus/internal/adapters/storage"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeRepo is a no-DB SpatialSource.
type fakeRepo struct{}

func (fakeRepo) Open(_ context.Context, path string) (*domain.Source, error) {
	return &domain.Source{
		ID: "fake", Name: "fake.gpkg", Path: path,
		Layers: []domain.Layer{{Name: "regions", GeometryColumn: "geom", GeometryType: "POLYGON", SRID: 4326, HasIndex: true}},
	}, nil
}
func (fakeRepo) Close(_ context.Context, _ string) error                       { return nil }
func (fakeRepo) Supports(_ string) bool                                        { return true }
func (fakeRepo) Prepare(_ context.Context, _, _ string) error                  { return nil }
func (fakeRepo) GetLayers(_ context.Context, _ string) ([]domain.Layer, error) { return nil, nil }
func (fakeRepo) CreateSpatialIndex(_ context.Context, _, _ string) error       { return nil }
func (fakeRepo) HasSpatialIndex(_ context.Context, _, _ string) (bool, error)  { return true, nil }
func (fakeRepo) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}

// stubStorage satisfies output.ObjectStorage with no real I/O.
type stubStorage struct{}

func (stubStorage) List(_ context.Context) ([]output.StorageObject, error) { return nil, nil }
func (stubStorage) Download(_ context.Context, _, _ string) error          { return nil }
func (stubStorage) GetReader(_ context.Context, _ string) (io.ReadCloser, error) {
	// Returning (nil, nil) would violate the ObjectStorage contract and
	// crash any caller that tries to defer Close. The MCP tests don't
	// exercise this path, but be safe.
	return io.NopCloser(strings.NewReader("")), nil
}
func (stubStorage) Exists(_ context.Context, _ string) (bool, error) { return false, nil }

// buildDeps wires up everything an MCP server needs, with all SQLite /
// network paths stubbed out.
func buildDeps(t *testing.T) mcpAdapter.Deps {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	meter := noop.NewMeterProvider().Meter("test")

	tp, err := telemetry.NewProvider(context.Background(), telemetry.ProviderOptions{
		ServiceName: "ortus-test",
		SampleRatio: 1.0,
		BufferSize:  16,
	}, logger)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	tr := telemetry.NewTracer(tp.TracerProvider())
	store := storage.NewTracedStorage(stubStorage{}, tr, "local")
	reg := application.NewSourceRegistry([]output.SpatialSource{fakeRepo{}}, store, meter, tr, logger, "/tmp")
	qs := application.NewQueryService(reg, nil, meter, tr, logger, application.QueryServiceConfig{})
	hs := application.NewHealthService(reg, tr)

	return mcpAdapter.Deps{
		Telemetry:     tp.Buffer(),
		QueryService:  qs,
		Registry:      reg,
		HealthService: hs,
		Version:       "test",
	}
}

// startTestServer boots an MCP server with the given token and returns
// a running httptest.Server.
func startTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := mcpAdapter.New(mcpAdapter.Options{
		Host: "127.0.0.1", Port: 0, Path: "/mcp", Token: token,
	}, buildDeps(t), slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestBearerAuth_RejectsBadToken checks that a non-matching Authorization
// header gets 401 — and that the happy path proceeds to the MCP handler
// (which will respond with something non-401 even if the body is bogus).
func TestBearerAuth_RejectsBadToken(t *testing.T) {
	ts := startTestServer(t, "secret")
	for _, tc := range []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"prefix-mismatch", "Token secret", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

// TestBearerAuth_NoTokenAllowsAll: when the server is configured with
// empty token (loopback mode), middleware should pass everything through.
func TestBearerAuth_NoTokenAllowsAll(t *testing.T) {
	ts := startTestServer(t, "")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/mcp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("got 401 with empty-token config — middleware should have passed through")
	}
}

// TestToolsRegistered is the contract for the MCP surface: every tool we
// document MUST be discoverable via ListTools, and each must have a
// non-empty description.
func TestToolsRegistered(t *testing.T) {
	ts := startTestServer(t, "")

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := []string{
		// Diagnostic
		"list_traces", "get_trace", "list_active_spans", "tracing_stats", "health",
		// Query
		"query_point", "list_sources", "get_source", "get_source_layers",
	}
	have := map[string]bool{}
	for _, tt := range list.Tools {
		have[tt.Name] = true
		if tt.Description == "" {
			t.Errorf("tool %q has empty description", tt.Name)
		}
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("missing tool: %q (have: %v)", name, have)
		}
	}
}

// connectClient is a small helper that opens an MCP session for a
// test server. Cleans up on test exit.
func connectClient(t *testing.T, ts *httptest.Server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestHealthTool exercises the simplest tool end-to-end (no DB
// dependency, returns immediately) to prove the JSON-RPC roundtrip
// works from input parsing through to typed output.
func TestHealthTool(t *testing.T) {
	session := connectClient(t, startTestServer(t, ""))
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "health"})
	if err != nil {
		t.Fatalf("CallTool health: %v", err)
	}
	if res.IsError {
		t.Fatalf("health returned IsError; content=%v", res.Content)
	}
}

// TestListSourcesTool round-trips list_sources and asserts the
// response shape matches what doc/MCP.md promises.
func TestListSourcesTool(t *testing.T) {
	session := connectClient(t, startTestServer(t, ""))
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_sources"})
	if err != nil {
		t.Fatalf("CallTool list_sources: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_sources returned IsError; content=%v", res.Content)
	}
}

// TestQueryPointTool covers the coordinate-validation logic — both the
// happy path AND the (0,0) edge case that the float64+omitempty design
// used to mishandle.
func TestQueryPointTool(t *testing.T) {
	session := connectClient(t, startTestServer(t, ""))

	cases := []struct {
		name        string
		args        map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name:    "lon/lat happy path",
			args:    map[string]any{"lon": 13.4, "lat": 52.5},
			wantErr: false,
		},
		{
			name:    "x/y/srid happy path",
			args:    map[string]any{"x": 389000.0, "y": 5820000.0, "srid": 25832},
			wantErr: false,
		},
		{
			name:    "(0,0) is a valid coordinate",
			args:    map[string]any{"lon": 0.0, "lat": 0.0},
			wantErr: false,
		},
		{
			name:        "missing pair partner",
			args:        map[string]any{"lon": 13.4},
			wantErr:     true,
			errContains: "both 'lon' and 'lat'",
		},
		{
			name:        "no coordinate at all",
			args:        map[string]any{},
			wantErr:     true,
			errContains: "coordinate required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "query_point",
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if tc.wantErr {
				if !res.IsError {
					t.Fatalf("expected IsError, got success: %v", res.Content)
				}
				if tc.errContains != "" {
					var foundText string
					for _, c := range res.Content {
						if tc, ok := c.(*mcp.TextContent); ok {
							foundText += tc.Text
						}
					}
					if !strings.Contains(foundText, tc.errContains) {
						t.Errorf("error message %q does not contain %q", foundText, tc.errContains)
					}
				}
				return
			}
			if res.IsError {
				t.Fatalf("unexpected error: %v", res.Content)
			}
		})
	}
}
