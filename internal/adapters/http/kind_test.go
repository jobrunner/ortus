package http

import (
	"testing"

	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
)

// A client decides from `kind` whether a source has feature geometries to
// fetch, so both the source listing and each query result carry it.
func TestKindInSourceAndQueryResult(t *testing.T) {
	srv := newTestServerWithConfig(config.ServerConfig{Host: "localhost", Port: 8080})

	for _, kind := range []domain.SourceKind{domain.SourceKindVector, domain.SourceKindRaster} {
		if got := srv.formatSource(&domain.Source{ID: "s", Kind: kind})["kind"]; got != string(kind) {
			t.Errorf("source kind = %v, want %q", got, kind)
		}
		resp := srv.formatQueryResponse(&domain.QueryResponse{
			Results: []domain.QueryResult{{SourceID: "s", Kind: kind}},
		})
		results, _ := resp["results"].([]map[string]interface{})
		if len(results) != 1 || results[0]["kind"] != string(kind) {
			t.Errorf("query result = %v, want kind %q", results, kind)
		}
	}

	// A source without a kind (never produced by the adapters) omits the field
	// rather than reporting an empty one.
	if _, has := srv.formatSource(&domain.Source{ID: "s"})["kind"]; has {
		t.Error("kind present for a source without one")
	}
}
