package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeInner is a controllable ObjectStorage for exercising the TracedStorage
// decorator's success and error branches.
type fakeInner struct {
	objs []output.StorageObject
	err  error
}

func (f *fakeInner) List(context.Context) ([]output.StorageObject, error) {
	return f.objs, f.err
}
func (f *fakeInner) Download(context.Context, string, string) error { return f.err }
func (f *fakeInner) GetReader(context.Context, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader("data")), nil
}
func (f *fakeInner) Exists(context.Context, string) (bool, error) {
	return f.err == nil, f.err
}

func TestTracedStorageDelegatesSuccess(t *testing.T) {
	inner := &fakeInner{objs: []output.StorageObject{{Key: "a.gpkg"}, {Key: "b.gpkg"}}}
	ts := NewTracedStorage(inner, nil, "local") // nil tracer => NoOp
	ctx := context.Background()

	objs, err := ts.List(ctx)
	if err != nil || len(objs) != 2 {
		t.Errorf("List = %v, %v; want 2 objects", objs, err)
	}
	if err := ts.Download(ctx, "a.gpkg", "/tmp/a"); err != nil {
		t.Errorf("Download: %v", err)
	}
	rc, err := ts.GetReader(ctx, "a.gpkg")
	if err != nil {
		t.Errorf("GetReader: %v", err)
	} else {
		_ = rc.Close()
	}
	ok, err := ts.Exists(ctx, "a.gpkg")
	if err != nil || !ok {
		t.Errorf("Exists = %v, %v; want true, nil", ok, err)
	}
}

func TestTracedStoragePropagatesErrors(t *testing.T) {
	sentinel := errors.New("boom")
	ts := NewTracedStorage(&fakeInner{err: sentinel}, nil, "s3")
	ctx := context.Background()

	if _, err := ts.List(ctx); !errors.Is(err, sentinel) {
		t.Errorf("List err = %v, want sentinel", err)
	}
	if err := ts.Download(ctx, "k", "d"); !errors.Is(err, sentinel) {
		t.Errorf("Download err = %v, want sentinel", err)
	}
	if _, err := ts.GetReader(ctx, "k"); !errors.Is(err, sentinel) {
		t.Errorf("GetReader err = %v, want sentinel", err)
	}
	if ok, err := ts.Exists(ctx, "k"); ok || !errors.Is(err, sentinel) {
		t.Errorf("Exists = %v, %v; want false, sentinel", ok, err)
	}
}
