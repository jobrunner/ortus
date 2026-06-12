package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// SpanContextHandler is a slog.Handler decorator that injects the current
// span's trace_id and span_id into every log record carrying a context. It
// is the link that lets you jump from a stray Warn-level log to the trace
// it happened in.
//
// Wrap an existing handler with NewSpanContextHandler(inner) in main.go:
//
//	logger := slog.New(telemetry.NewSpanContextHandler(jsonHandler))
type SpanContextHandler struct {
	inner slog.Handler
}

// NewSpanContextHandler wraps the inner handler. Passing nil falls back to
// slog.Default()'s handler so callers don't have to nil-check.
func NewSpanContextHandler(inner slog.Handler) *SpanContextHandler {
	if inner == nil {
		inner = slog.Default().Handler()
	}
	return &SpanContextHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *SpanContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle adds trace_id / span_id attributes when ctx carries a valid span.
func (h *SpanContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs delegates.
func (h *SpanContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SpanContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup delegates.
func (h *SpanContextHandler) WithGroup(name string) slog.Handler {
	return &SpanContextHandler{inner: h.inner.WithGroup(name)}
}
