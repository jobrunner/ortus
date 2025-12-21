package domain

import (
	"errors"
	"testing"
)

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:      "longitude",
		Value:      200.0,
		Constraint: "[-180, 180]",
		Message:    "longitude must be between -180 and 180",
	}

	// Test Error() output
	got := err.Error()
	if got == "" {
		t.Error("Error() should not return empty string")
	}

	// Test Unwrap()
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("ValidationError should unwrap to ErrInvalidInput")
	}
}

func TestQueryError(t *testing.T) {
	tests := []struct {
		name      string
		err       *QueryError
		wantEmpty bool
	}{
		{
			name: "with layer",
			err: &QueryError{
				PackageID: "test-pkg",
				Layer:     "test-layer",
				Err:       errors.New("query failed"),
			},
			wantEmpty: false,
		},
		{
			name: "without layer",
			err: &QueryError{
				PackageID: "test-pkg",
				Err:       errors.New("query failed"),
			},
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if (got == "") != tt.wantEmpty {
				t.Errorf("Error() = %q, wantEmpty = %v", got, tt.wantEmpty)
			}

			// Test Unwrap
			if !errors.Is(tt.err, tt.err.Err) {
				t.Error("Unwrap should return the underlying error")
			}
		})
	}
}

func TestStorageError(t *testing.T) {
	tests := []struct {
		name string
		err  *StorageError
	}{
		{
			name: "with key",
			err: &StorageError{
				Operation: "download",
				Key:       "file.gpkg",
				Err:       errors.New("network error"),
			},
		},
		{
			name: "without key",
			err: &StorageError{
				Operation: "list",
				Err:       errors.New("access denied"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got == "" {
				t.Error("Error() should not return empty string")
			}

			// Test Unwrap
			if !errors.Is(tt.err, tt.err.Err) {
				t.Error("Unwrap should return the underlying error")
			}
		})
	}
}

func TestIndexError(t *testing.T) {
	err := &IndexError{
		PackageID: "test-pkg",
		Layer:     "test-layer",
		Err:       errors.New("index creation failed"),
	}

	got := err.Error()
	if got == "" {
		t.Error("Error() should not return empty string")
	}

	// Test Unwrap
	if !errors.Is(err, err.Err) {
		t.Error("Unwrap should return the underlying error")
	}
}

func TestConfigError(t *testing.T) {
	err := &ConfigError{
		Field:   "storage.path",
		Message: "path not found",
	}

	got := err.Error()
	if got == "" {
		t.Error("Error() should not return empty string")
	}

	// Test Unwrap
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("ConfigError should unwrap to ErrInvalidInput")
	}
}

func TestSentinelErrors(t *testing.T) {
	// Test that specific errors wrap base errors correctly
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{"ErrPackageNotFound", ErrPackageNotFound, ErrNotFound},
		{"ErrLayerNotFound", ErrLayerNotFound, ErrNotFound},
		{"ErrInvalidCoordinate", ErrInvalidCoordinate, ErrInvalidInput},
		{"ErrInvalidSRID", ErrInvalidSRID, ErrInvalidInput},
		{"ErrUnsupportedProjection", ErrUnsupportedProjection, ErrUnsupported},
		{"ErrIndexCreationFailed", ErrIndexCreationFailed, ErrInternal},
		{"ErrNotReady", ErrNotReady, ErrUnavailable},
		{"ErrStorageUnavailable", ErrStorageUnavailable, ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.wantErr) {
				t.Errorf("%s should wrap %v", tt.name, tt.wantErr)
			}
		})
	}
}
