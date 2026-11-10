package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
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
