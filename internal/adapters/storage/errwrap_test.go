package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

func TestErrorWrappingStorage_WrapsErrors(t *testing.T) {
	sentinel := errors.New("boom")
	s := NewErrorWrappingStorage(&fakeInner{err: sentinel})
	ctx := context.Background()

	check := func(name string, err error, wantOp string) {
		t.Helper()
		var se *domain.StorageError
		if !errors.As(err, &se) {
			t.Fatalf("%s: error %v is not a *domain.StorageError", name, err)
		}
		if se.Operation != wantOp {
			t.Errorf("%s: Operation = %q, want %q", name, se.Operation, wantOp)
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("%s: underlying error not preserved (errors.Is failed)", name)
		}
	}

	_, err := s.List(ctx)
	check("List", err, "list")
	check("Download", s.Download(ctx, "k", "/tmp/d"), "download")
	_, err = s.GetReader(ctx, "k")
	check("GetReader", err, "get_reader")
	_, err = s.Exists(ctx, "k")
	check("Exists", err, "exists")
}

func TestErrorWrappingStorage_PassesThroughSuccessAndTypedErrors(t *testing.T) {
	ctx := context.Background()

	// Success: no error, objects forwarded.
	ok := NewErrorWrappingStorage(&fakeInner{})
	if objs, err := ok.List(ctx); err != nil || objs != nil {
		t.Errorf("List(success) = %v, %v; want nil, nil", objs, err)
	}

	// An already-typed StorageError must pass through unchanged (operation/key
	// set closest to the failure is preserved, not re-wrapped).
	typed := &domain.StorageError{Operation: "download", Key: "deep", Err: errors.New("x")}
	s := NewErrorWrappingStorage(&fakeInner{err: typed})
	err := s.Download(ctx, "outer", "/tmp/d")
	var se *domain.StorageError
	if !errors.As(err, &se) || se.Key != "deep" {
		t.Errorf("typed error was re-wrapped: got %#v", err)
	}
}
