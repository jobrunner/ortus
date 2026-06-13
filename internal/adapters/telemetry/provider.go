package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/jobrunner/ortus/internal/config"
)

// otelErrors counts errors observed by OTel's internal error handler so the
// MCP server (and /metrics if wired) can see when the collector is sick.
var otelErrors atomic.Uint64

// OTelErrorCount returns the number of internal OTel errors observed since
// process start (typically OTLP exporter failures).
func OTelErrorCount() uint64 { return otelErrors.Load() }

// slogErrorHandler routes OTel SDK errors to the configured slog logger so
// failures like "OTLP collector unreachable" don't disappear into stderr.
type slogErrorHandler struct{ logger *slog.Logger }

func (h *slogErrorHandler) Handle(err error) {
	otelErrors.Add(1)
	h.logger.Warn("otel internal error", "error", err)
}

// ProviderOptions configures the trace provider.
type ProviderOptions struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	Transport      string // "http" or "grpc"
	Endpoint       string
	Insecure       bool
	Headers        map[string]string
	SampleRatio    float64
	BufferSize     int
	ExtraAttrs     map[string]string
}

// Provider bundles the OTel TracerProvider with the in-memory ring buffer so
// the App can shut down everything together.
type Provider struct {
	tp     *sdktrace.TracerProvider
	buf    *RingBuffer
	logger *slog.Logger
}

// NewProvider builds a TracerProvider with a parent-based sampler, the OTLP
// exporter (if endpoint set), and the in-memory ring buffer. It also installs
// the global tracer provider and a W3C TraceContext + Baggage propagator so
// that incoming requests' tracestate is honored.
func NewProvider(ctx context.Context, opts ProviderOptions, logger *slog.Logger) (*Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}

	res, err := buildResource(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("building resource: %w", err)
	}

	processors := make([]sdktrace.SpanProcessor, 0, 2)

	buf := NewRingBuffer(opts.BufferSize, opts.ServiceName)
	if opts.BufferSize > 0 {
		processors = append(processors, buf)
	}

	if opts.Endpoint != "" {
		exporter, err := buildOTLPExporter(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("building OTLP exporter: %w", err)
		}
		processors = append(processors, sdktrace.NewBatchSpanProcessor(exporter))
		logger.Info("OTLP trace exporter configured", "endpoint", opts.Endpoint, "transport", opts.Transport)
	} else {
		logger.Info("OTLP trace exporter disabled (no endpoint configured)")
	}

	sampler := buildSampler(opts.SampleRatio)

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}
	for _, p := range processors {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(p))
	}

	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	// Surface OTLP-exporter and other SDK errors via slog. Otherwise the
	// only signal you get for a dead collector is missing spans.
	otel.SetErrorHandler(&slogErrorHandler{logger: logger})

	return &Provider{tp: tp, buf: buf, logger: logger}, nil
}

// TracerProvider returns the underlying OTel TracerProvider, used by
// HTTP/middleware instrumentation libraries that need it directly.
func (p *Provider) TracerProvider() *sdktrace.TracerProvider { return p.tp }

// Buffer returns the in-memory ring buffer the MCP server will read from.
func (p *Provider) Buffer() *RingBuffer { return p.buf }

// Shutdown flushes spans and shuts down the provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}

func buildSampler(ratio float64) sdktrace.Sampler {
	switch {
	case ratio <= 0:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case ratio >= 1.0:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	}
}

func buildResource(ctx context.Context, opts ProviderOptions) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
	}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(opts.ServiceVersion))
	}
	if opts.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(opts.Environment))
	}
	for k, v := range opts.ExtraAttrs {
		attrs = append(attrs, attribute.String(k, v))
	}

	return resource.New(ctx,
		resource.WithHost(),
		resource.WithOSType(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithAttributes(attrs...),
	)
}

func buildOTLPExporter(ctx context.Context, opts ProviderOptions) (*otlptrace.Exporter, error) {
	transport := opts.Transport
	if transport == "" {
		transport = config.TracingTransportHTTP
	}

	switch transport {
	case config.TracingTransportGRPC:
		grpcOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(opts.Endpoint)}
		if opts.Insecure {
			grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
		}
		if len(opts.Headers) > 0 {
			grpcOpts = append(grpcOpts, otlptracegrpc.WithHeaders(opts.Headers))
		}
		return otlptracegrpc.New(ctx, grpcOpts...)
	default:
		// HTTP is the documented default; any unknown transport falls back to it.
		httpOpts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(opts.Endpoint)}
		if opts.Insecure {
			httpOpts = append(httpOpts, otlptracehttp.WithInsecure())
		}
		if len(opts.Headers) > 0 {
			httpOpts = append(httpOpts, otlptracehttp.WithHeaders(opts.Headers))
		}
		return otlptracehttp.New(ctx, httpOpts...)
	}
}
