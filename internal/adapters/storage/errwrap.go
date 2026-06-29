package storage

import (
	"context"
	"errors"
	"io"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ErrorWrappingStorage decorates an ObjectStorage so every non-nil error is a
// *domain.StorageError. This gives all backends (local, s3, azure, http) one
// consistent error type without each wrapping at every return site, and lets
// the HTTP layer map storage failures to 503 uniformly (handleQueryError).
// It is a decorator and adds no business logic — same pattern as TracedStorage.
type ErrorWrappingStorage struct {
	inner output.ObjectStorage
}

// NewErrorWrappingStorage wraps inner so its errors surface as *domain.StorageError.
func NewErrorWrappingStorage(inner output.ObjectStorage) *ErrorWrappingStorage {
	return &ErrorWrappingStorage{inner: inner}
}

// wrapStorage normalizes err to a *domain.StorageError. nil stays nil; an error
// that already is (or wraps) a *domain.StorageError passes through unchanged so
// the operation/key set closest to the failure is preserved.
func wrapStorage(op, key string, err error) error {
	if err == nil {
		return nil
	}
	var se *domain.StorageError
	if errors.As(err, &se) {
		return err
	}
	return &domain.StorageError{Operation: op, Key: key, Err: err}
}

// List implements ObjectStorage.
func (s *ErrorWrappingStorage) List(ctx context.Context) ([]output.StorageObject, error) {
	objs, err := s.inner.List(ctx)
	return objs, wrapStorage("list", "", err)
}

// Download implements ObjectStorage.
func (s *ErrorWrappingStorage) Download(ctx context.Context, key, dest string) error {
	return wrapStorage("download", key, s.inner.Download(ctx, key, dest))
}

// GetReader implements ObjectStorage.
func (s *ErrorWrappingStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := s.inner.GetReader(ctx, key)
	return r, wrapStorage("get_reader", key, err)
}

// Exists implements ObjectStorage.
func (s *ErrorWrappingStorage) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := s.inner.Exists(ctx, key)
	return ok, wrapStorage("exists", key, err)
}
