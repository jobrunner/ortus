package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpAdapter "github.com/jobrunner/ortus/internal/adapters/mcp"
	"github.com/jobrunner/ortus/internal/domain"
)

// fakeGazetteer is a canned input.Gazetteer for the MCP tool test.
type fakeGazetteer struct {
	loc *domain.Locality
	fix *domain.Fix
}

func (f fakeGazetteer) Locate(context.Context, domain.Coordinate) (*domain.Locality, error) {
	return f.loc, nil
}
func (f fakeGazetteer) Bearing(context.Context, domain.Coordinate, domain.BearingPolicy) (*domain.Fix, error) {
	return f.fix, nil
}

func startGazetteerServer(t *testing.T) *httptest.Server {
	t.Helper()
	deps := buildDeps(t)
	deps.Gazetteer = fakeGazetteer{
		loc: &domain.Locality{CountryISO: "DE", Chain: []domain.AdminUnit{
			{Level: 8, Name: "Würzburg", NameNative: "Würzburg", Equivalent: "municipality",
				LocalTerm: "Kreisfreie Stadt", EquivalentDesc: "Municipality / commune",
				NameSource: domain.NameProvenance{Code: "latin-osm", Short: "OSM name", Long: "OSM name tag.", Standard: ""}},
		}},
		fix: &domain.Fix{
			Reference: domain.Place{Name: "Würzburg", NameNative: "Würzburg", Class: domain.ClassCity,
				NameSource: domain.NameProvenance{Code: "latin-osm", Short: "OSM name", Long: "OSM name tag.", Standard: ""}},
			DistanceKM: 4, Azimuth: 90, Compass: "E", Label: "4 km E Würzburg",
		},
	}
	srv := mcpAdapter.New(mcpAdapter.Options{Host: "127.0.0.1", Port: 0, Path: "/mcp"}, deps,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestGazetteerTool(t *testing.T) {
	ts := startGazetteerServer(t)
	client := mcp.NewClient(&mcp.Implementation{Name: "gaz-test"}, nil)
	session, err := client.Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// The tool must be registered when a gazetteer is wired.
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tl := range list.Tools {
		if tl.Name == "gazetteer" {
			found = true
		}
	}
	if !found {
		t.Fatal("gazetteer tool not registered")
	}

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "gazetteer",
		Arguments: map[string]any{"lon": 9.93, "lat": 49.79},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}

	var out struct {
		Admin *struct {
			CountryISO string `json:"country_iso"`
			Hierarchy  []struct {
				Name                  string `json:"name"`
				NameNative            string `json:"name_native"`
				NameSource            string `json:"name_source"`
				LocalTerm             string `json:"local_term"`
				EquivalentDescription string `json:"equivalent_description"`
			} `json:"hierarchy"`
		} `json:"admin"`
		Bearing *struct {
			Label      string `json:"label"`
			NameSource string `json:"name_source"`
		} `json:"bearing"`
		Sources []struct {
			Code  string `json:"code"`
			Short string `json:"short"`
		} `json:"sources"`
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if out.Admin == nil || out.Admin.CountryISO != "DE" {
		t.Errorf("admin = %+v, want country_iso DE", out.Admin)
	}
	if out.Bearing == nil || out.Bearing.Label != "4 km E Würzburg" {
		t.Errorf("bearing = %+v, want label '4 km E Würzburg'", out.Bearing)
	}
	// Provenance: per-record code + native/local terms and the response-wide
	// sources block (latin-osm appears on both records → listed once).
	if len(out.Admin.Hierarchy) != 1 || out.Admin.Hierarchy[0].NameSource != "latin-osm" ||
		out.Admin.Hierarchy[0].LocalTerm != "Kreisfreie Stadt" ||
		out.Admin.Hierarchy[0].EquivalentDescription != "Municipality / commune" {
		t.Errorf("hierarchy = %+v, want latin-osm code + local term + equivalent description", out.Admin.Hierarchy)
	}
	if out.Bearing.NameSource != "latin-osm" {
		t.Errorf("bearing name_source = %q, want latin-osm", out.Bearing.NameSource)
	}
	if len(out.Sources) != 1 || out.Sources[0].Code != "latin-osm" || out.Sources[0].Short != "OSM name" {
		t.Errorf("sources = %+v, want single latin-osm entry", out.Sources)
	}
}
