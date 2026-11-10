package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// TestQueryBatchTransformErrorIs5xx: the batch path shares the transformer with
// /query, so a broken transformer must fail the batch as well instead of
// returning items without wgs84/gazetteer.
func TestQueryBatchTransformErrorIs5xx(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp")
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("test"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})
	srv := NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{
			Gazetteer: fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, GazetteerLicense: sampleGazetteerLicense(),
			Transformer:        fakeTransformer{supported: true, transformErr: errors.New("proj boom")},
			BatchMaxSyncPoints: 1000, BatchMaxPoints: 10000,
		},
	)

	for _, accept := range []string{"", "application/x-ndjson"} {
		rec := doBatch(t, srv, `{"points":[{"mgrs":"32UMA7652652069"},{"lon":8.68,"lat":50.1}]}`, accept)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("accept %q: status = %d, want 500 (body %s)", accept, rec.Code, rec.Body.String())
		}
	}
}

// knownSRIDTransformer supports only the listed SRIDs, like the real transformer
// does with spatial_ref_sys; every supported transform lands on Frankfurt.
type knownSRIDTransformer map[int]bool

func (k knownSRIDTransformer) Transform(_ context.Context, _ domain.Coordinate, _ int) (domain.Coordinate, error) {
	return domain.NewWGS84Coordinate(8.68, 50.1), nil
}
func (k knownSRIDTransformer) IsSupported(from, to int) bool { return k[from] && k[to] }

// TestQueryUnknownSRIDIs400: an SRID the transformer doesn't know is the
// client's mistake. It must come back as 400 naming the SRID — not as a 500
// "transformation failed", which would page someone for bad input — with or
// without the source query.
func TestQueryUnknownSRIDIs400(t *testing.T) {
	tf := knownSRIDTransformer{domain.SRIDWGS84: true, 25832: true}
	srv := newGazetteerServerT(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, tf)
	for _, q := range []string{
		"/api/v1/query?x=477114&y=5549798&srid=999999",
		"/api/v1/query?x=477114&y=5549798&srid=999999&with-sources=false",
	} {
		rec, body := doGET(t, srv, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %v)", q, rec.Code, body)
			continue
		}
		if msg, _ := body["message"].(string); !strings.Contains(msg, "unknown srid 999999") {
			t.Errorf("%s: message = %q, want it to name the unknown srid", q, msg)
		}
	}
	// A known SRID still answers.
	if rec, body := doGET(t, srv, "/api/v1/query?x=477114&y=5549798&srid=25832"); rec.Code != http.StatusOK {
		t.Errorf("srid=25832: status = %d, want 200 (body %v)", rec.Code, body)
	}
}

// TestQueryBatchUnknownSRIDIsItemError: in a batch, an unknown SRID fails only
// its own point; the valid points are still answered.
func TestQueryBatchUnknownSRIDIsItemError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp")
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	tf := knownSRIDTransformer{domain.SRIDWGS84: true, 25832: true}
	query := application.NewQueryService(reg, tf, noop.NewMeterProvider().Meter("test"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})
	srv := NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{Transformer: tf, BatchMaxSyncPoints: 1000, BatchMaxPoints: 10000},
	)

	rec := doBatch(t, srv, `{"points":[{"id":"bad","x":477114,"y":5549798,"srid":999999},{"id":"ok","lon":8.68,"lat":50.1}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct {
			ID    string `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			WGS84 map[string]any `json:"wgs84"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("results = %d, want 2 (body %s)", len(body.Results), rec.Body.String())
	}
	if bad := body.Results[0]; bad.Error == nil || !strings.Contains(bad.Error.Message, "unknown srid 999999") {
		t.Errorf("item %q error = %+v, want the unknown srid", bad.ID, bad.Error)
	}
	if ok := body.Results[1]; ok.Error != nil || ok.WGS84 == nil {
		t.Errorf("item %q = %+v, want answered with wgs84", ok.ID, ok)
	}
}
