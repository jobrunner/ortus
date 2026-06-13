// Package metrics provides the OpenTelemetry meter provider that exports
// metrics both via Prometheus scrape (/metrics) and, optionally, via OTLP
// push to an external collector. The provider is constructed once at
// startup; services pull the *otel-go* meter from it and define their own
// instruments — there is no per-instrument port abstraction here. The
// per-signal config lives in config.MetricsConfig.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/jobrunner/ortus/internal/config"
)

// Options configures the meter provider built by New.
type Options struct {
	Namespace string // Prefix for instrument names (e.g. "ortus")

	// OTLP push configuration. When OTLPEnabled is true, the meter provider
	// gets a PeriodicReader exporting via the chosen transport in addition
	// to the Prometheus scrape Reader.
	OTLPEnabled   bool
	OTLPEndpoint  string
	OTLPTransport string
	OTLPInsecure  bool
	OTLPHeaders   map[string]string
	OTLPInterval  time.Duration
}

// Collector bundles the MeterProvider lifecycle. Services obtain instruments
// by calling MeterProvider().Meter(name) on it directly.
type Collector struct {
	provider *sdkmetric.MeterProvider
}

// New constructs the meter provider with a Prometheus reader and optionally
// an OTLP PeriodicReader. The Prometheus exporter registers with the
// default prometheus registerer, so promhttp.Handler() picks it up.
func New(ctx context.Context, opts Options, logger *slog.Logger) (*Collector, error) {
	if logger == nil {
		logger = slog.Default()
	}

	readers := make([]sdkmetric.Option, 0, 2)

	promExporter, err := otelprom.New()
	if err != nil {
		return nil, fmt.Errorf("creating prometheus exporter: %w", err)
	}
	readers = append(readers, sdkmetric.WithReader(promExporter))

	if opts.OTLPEnabled {
		otlpReader, err := buildOTLPMetricReader(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("creating OTLP metric exporter: %w", err)
		}
		readers = append(readers, sdkmetric.WithReader(otlpReader))
		logger.Info("OTLP metric exporter configured",
			"endpoint", opts.OTLPEndpoint,
			"transport", opts.OTLPTransport,
			"interval", opts.OTLPInterval,
		)
	}

	provider := sdkmetric.NewMeterProvider(readers...)
	return &Collector{provider: provider}, nil
}

// MeterProvider returns the underlying OTel MeterProvider. Services call
// .Meter(name) on it to obtain instruments.
func (c *Collector) MeterProvider() metric.MeterProvider {
	if c == nil || c.provider == nil {
		return nil
	}
	return c.provider
}

// Shutdown flushes pending metrics and closes the provider. Safe on nil.
func (c *Collector) Shutdown(ctx context.Context) error {
	if c == nil || c.provider == nil {
		return nil
	}
	return c.provider.Shutdown(ctx)
}

func buildOTLPMetricReader(ctx context.Context, opts Options) (sdkmetric.Reader, error) {
	transport := opts.OTLPTransport
	if transport == "" {
		transport = config.TracingTransportHTTP
	}
	interval := opts.OTLPInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}

	var exporter sdkmetric.Exporter
	var err error
	switch transport {
	case config.TracingTransportGRPC:
		grpcOpts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(opts.OTLPEndpoint)}
		if opts.OTLPInsecure {
			grpcOpts = append(grpcOpts, otlpmetricgrpc.WithInsecure())
		}
		if len(opts.OTLPHeaders) > 0 {
			grpcOpts = append(grpcOpts, otlpmetricgrpc.WithHeaders(opts.OTLPHeaders))
		}
		exporter, err = otlpmetricgrpc.New(ctx, grpcOpts...)
	default:
		httpOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(opts.OTLPEndpoint)}
		if opts.OTLPInsecure {
			httpOpts = append(httpOpts, otlpmetrichttp.WithInsecure())
		}
		if len(opts.OTLPHeaders) > 0 {
			httpOpts = append(httpOpts, otlpmetrichttp.WithHeaders(opts.OTLPHeaders))
		}
		exporter, err = otlpmetrichttp.New(ctx, httpOpts...)
	}
	if err != nil {
		return nil, err
	}

	return sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval)), nil
}

// Handler returns the Prometheus HTTP handler. The OTel prometheus exporter
// registers with the default registerer, which this handler serves.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Server provides a dedicated HTTP server for Prometheus metrics.
type Server struct {
	server *http.Server
	logger *slog.Logger
}

// NewServer creates a new metrics server.
func NewServer(port int, path string, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle(path, promhttp.Handler())

	return &Server{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// Start starts the metrics server.
func (s *Server) Start() error {
	s.logger.Info("starting metrics server", "address", s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the metrics server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down metrics server")
	return s.server.Shutdown(ctx)
}
