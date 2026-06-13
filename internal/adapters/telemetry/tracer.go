package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// OTelTracer adapts an OTel trace.Tracer to the output.Tracer port.
type OTelTracer struct {
	tracer trace.Tracer
}

// NewTracer builds an output.Tracer backed by the given OTel TracerProvider.
// The instrumentation name groups all spans emitted via this tracer in OTel
// backends; we use the Go module path.
func NewTracer(tp trace.TracerProvider) *OTelTracer {
	return &OTelTracer{tracer: tp.Tracer("github.com/jobrunner/ortus")}
}

// Start implements output.Tracer.
func (t *OTelTracer) Start(ctx context.Context, name string, opts ...output.StartSpanOption) (context.Context, output.Span) {
	cfg := output.StartSpanOptions{}
	for _, o := range opts {
		o(&cfg)
	}

	otelOpts := []trace.SpanStartOption{
		trace.WithSpanKind(mapKind(cfg.Kind)),
	}
	if len(cfg.Attributes) > 0 {
		otelOpts = append(otelOpts, trace.WithAttributes(toOTelAttrs(cfg.Attributes)...))
	}

	ctx, span := t.tracer.Start(ctx, name, otelOpts...)
	return ctx, &otelSpan{span: span}
}

type otelSpan struct {
	span trace.Span
}

func (s *otelSpan) SetAttributes(attrs ...output.Attribute) {
	if len(attrs) == 0 {
		return
	}
	s.span.SetAttributes(toOTelAttrs(attrs)...)
}

func (s *otelSpan) AddEvent(name string, attrs ...output.Attribute) {
	if len(attrs) == 0 {
		s.span.AddEvent(name)
		return
	}
	s.span.AddEvent(name, trace.WithAttributes(toOTelAttrs(attrs)...))
}

func (s *otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	// Always include the stack trace. Errors are rare relative to total
	// span volume, and the stack is exactly the diagnostic info that makes
	// "why did this fail" answerable from the ring buffer alone — without
	// it, the MCP server gets a single error string and no caller context.
	s.span.RecordError(err, trace.WithStackTrace(true))
}

func (s *otelSpan) SetStatus(code output.StatusCode, description string) {
	s.span.SetStatus(mapStatus(code), description)
}

func (s *otelSpan) End() { s.span.End() }

func mapKind(k output.SpanKind) trace.SpanKind {
	switch k {
	case output.SpanKindServer:
		return trace.SpanKindServer
	case output.SpanKindClient:
		return trace.SpanKindClient
	case output.SpanKindProducer:
		return trace.SpanKindProducer
	case output.SpanKindConsumer:
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindInternal
	}
}

func mapStatus(c output.StatusCode) codes.Code {
	switch c {
	case output.StatusOK:
		return codes.Ok
	case output.StatusError:
		return codes.Error
	default:
		return codes.Unset
	}
}

func toOTelAttrs(in []output.Attribute) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(in))
	for _, a := range in {
		switch v := a.Value.(type) {
		case string:
			out = append(out, attribute.String(a.Key, v))
		case bool:
			out = append(out, attribute.Bool(a.Key, v))
		case int:
			out = append(out, attribute.Int(a.Key, v))
		case int64:
			out = append(out, attribute.Int64(a.Key, v))
		case float64:
			out = append(out, attribute.Float64(a.Key, v))
		case []string:
			out = append(out, attribute.StringSlice(a.Key, v))
		case []int64:
			out = append(out, attribute.Int64Slice(a.Key, v))
		case []float64:
			out = append(out, attribute.Float64Slice(a.Key, v))
		default:
			// Best effort: stringify unknown types so we don't silently drop
			// the value. Handle nil, error, fmt.Stringer, and finally fall
			// back to fmt.Sprintf("%v", v) — better than an empty string.
			out = append(out, attribute.String(a.Key, sprint(v)))
		}
	}
	return out
}

func sprint(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
