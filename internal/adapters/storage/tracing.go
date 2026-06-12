package storage

import (
	"context"
	"io"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// TracedStorage wraps an ObjectStorage with span instrumentation. It is a
// decorator and adds no business logic, so the underlying adapter (local, s3,
// azure, http) stays free of telemetry code.
type TracedStorage struct {
	inner      output.ObjectStorage
	tracer     output.Tracer
	systemAttr output.Attribute // e.g. {Key: "storage.system", Value: "s3"}
}

// NewTracedStorage wraps inner with tracing using the given tracer. The
// storageSystem string is recorded as the "storage.system" attribute (e.g.
// "local", "s3", "azure", "http") so spans group cleanly in backends.
func NewTracedStorage(inner output.ObjectStorage, tracer output.Tracer, storageSystem string) *TracedStorage {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	return &TracedStorage{
		inner:      inner,
		tracer:     tracer,
		systemAttr: output.String("storage.system", storageSystem),
	}
}

// List implements ObjectStorage.
func (t *TracedStorage) List(ctx context.Context) ([]output.StorageObject, error) {
	ctx, span := t.tracer.Start(ctx, "ObjectStorage.List",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(t.systemAttr),
	)
	defer span.End()

	objs, err := t.inner.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "list failed")
		return nil, err
	}
	span.SetAttributes(output.Int("storage.objects.count", len(objs)))
	span.SetStatus(output.StatusOK, "")
	return objs, nil
}

// Download implements ObjectStorage.
func (t *TracedStorage) Download(ctx context.Context, key string, dest string) error {
	ctx, span := t.tracer.Start(ctx, "ObjectStorage.Download",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			t.systemAttr,
			output.String("storage.key", key),
			output.String("storage.dest", dest),
		),
	)
	defer span.End()

	if err := t.inner.Download(ctx, key, dest); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "download failed")
		return err
	}
	span.SetStatus(output.StatusOK, "")
	return nil
}

// GetReader implements ObjectStorage.
func (t *TracedStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, span := t.tracer.Start(ctx, "ObjectStorage.GetReader",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(t.systemAttr, output.String("storage.key", key)),
	)
	defer span.End()

	rc, err := t.inner.GetReader(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "get reader failed")
		return nil, err
	}
	span.SetStatus(output.StatusOK, "")
	return rc, nil
}

// Exists implements ObjectStorage.
func (t *TracedStorage) Exists(ctx context.Context, key string) (bool, error) {
	ctx, span := t.tracer.Start(ctx, "ObjectStorage.Exists",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(t.systemAttr, output.String("storage.key", key)),
	)
	defer span.End()

	ok, err := t.inner.Exists(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "exists failed")
		return false, err
	}
	span.SetAttributes(output.Bool("storage.exists", ok))
	span.SetStatus(output.StatusOK, "")
	return ok, nil
}
