package output

import "context"

// SpanKind classifies a span by its role in the system. It mirrors the OTel
// SpanKind so adapters can map values 1:1 without coupling the domain to OTel.
type SpanKind int

// Span kind constants. Internal is the default.
const (
	SpanKindInternal SpanKind = iota
	SpanKindServer
	SpanKindClient
	SpanKindProducer
	SpanKindConsumer
)

// StatusCode reflects the OTel status code without importing OTel.
type StatusCode int

// Status code constants.
const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

// Attribute is a typed key/value pair attached to a span. Value must be one of
// string, bool, int, int64, float64, []string, []int64, []float64.
type Attribute struct {
	Key   string
	Value any
}

// String builds a string attribute.
func String(key, value string) Attribute { return Attribute{Key: key, Value: value} }

// Int builds an int attribute.
func Int(key string, value int) Attribute { return Attribute{Key: key, Value: value} }

// Int64 builds an int64 attribute.
func Int64(key string, value int64) Attribute { return Attribute{Key: key, Value: value} }

// Float64 builds a float64 attribute.
func Float64(key string, value float64) Attribute { return Attribute{Key: key, Value: value} }

// Bool builds a bool attribute.
func Bool(key string, value bool) Attribute { return Attribute{Key: key, Value: value} }

// StartSpanOptions controls span creation.
type StartSpanOptions struct {
	Kind       SpanKind
	Attributes []Attribute
}

// StartSpanOption applies a setting to StartSpanOptions.
type StartSpanOption func(*StartSpanOptions)

// WithSpanKind sets the span kind.
func WithSpanKind(kind SpanKind) StartSpanOption {
	return func(o *StartSpanOptions) { o.Kind = kind }
}

// WithAttributes pre-populates the span with attributes.
func WithAttributes(attrs ...Attribute) StartSpanOption {
	return func(o *StartSpanOptions) { o.Attributes = append(o.Attributes, attrs...) }
}

// Tracer is the secondary port for distributed tracing. Adapters live in
// internal/adapters/telemetry. The default implementation is NoOpTracer.
type Tracer interface {
	// Start opens a new span and returns a context carrying it. The caller
	// must End() the span exactly once. If ctx already carries a span, the
	// new span becomes its child.
	Start(ctx context.Context, name string, opts ...StartSpanOption) (context.Context, Span)
}

// Span is an in-progress operation being traced.
type Span interface {
	SetAttributes(attrs ...Attribute)
	AddEvent(name string, attrs ...Attribute)
	RecordError(err error)
	SetStatus(code StatusCode, description string)
	End()
}

// NoOpTracer is the zero-value Tracer; it discards everything.
type NoOpTracer struct{}

// Start implements Tracer.
func (NoOpTracer) Start(ctx context.Context, _ string, _ ...StartSpanOption) (context.Context, Span) {
	return ctx, noOpSpan{}
}

type noOpSpan struct{}

func (noOpSpan) SetAttributes(_ ...Attribute)      {}
func (noOpSpan) AddEvent(_ string, _ ...Attribute) {}
func (noOpSpan) RecordError(_ error)               {}
func (noOpSpan) SetStatus(_ StatusCode, _ string)  {}
func (noOpSpan) End()                              {}
