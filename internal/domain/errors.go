package domain

import (
	"errors"
	"fmt"
)

// Base error types (sentinel errors).
var (
	ErrNotFound     = errors.New("not found")
	ErrInvalidInput = errors.New("invalid input")
	ErrUnsupported  = errors.New("unsupported operation")
	ErrInternal     = errors.New("internal error")
	ErrUnavailable  = errors.New("service unavailable")
)

// Specific errors.
var (
	ErrPackageNotFound       = fmt.Errorf("geopackage: %w", ErrNotFound)
	ErrLayerNotFound         = fmt.Errorf("layer: %w", ErrNotFound)
	ErrInvalidCoordinate     = fmt.Errorf("coordinate: %w", ErrInvalidInput)
	ErrInvalidSRID           = fmt.Errorf("srid: %w", ErrInvalidInput)
	ErrUnsupportedProjection = fmt.Errorf("projection: %w", ErrUnsupported)
	ErrIndexCreationFailed   = fmt.Errorf("index creation: %w", ErrInternal)
	ErrNotReady              = fmt.Errorf("service not ready: %w", ErrUnavailable)
	ErrStorageUnavailable    = fmt.Errorf("storage: %w", ErrUnavailable)
)

// ValidationError represents a detailed validation error.
type ValidationError struct {
	Field      string      // Field that failed validation
	Value      interface{} // The invalid value
	Constraint string      // The constraint that was violated
	Message    string      // Human-readable message
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for %s: %s (value: %v, constraint: %s)",
		e.Field, e.Message, e.Value, e.Constraint)
}

// Unwrap returns the underlying error type.
func (e *ValidationError) Unwrap() error {
	return ErrInvalidInput
}

// QueryError represents an error during a query operation.
type QueryError struct {
	PackageID string // GeoPackage identifier
	Layer     string // Layer name
	Err       error  // Underlying error
}

// Error implements the error interface.
func (e *QueryError) Error() string {
	if e.Layer != "" {
		return fmt.Sprintf("query error in package %s, layer %s: %v",
			e.PackageID, e.Layer, e.Err)
	}
	return fmt.Sprintf("query error in package %s: %v", e.PackageID, e.Err)
}

// Unwrap returns the underlying error.
func (e *QueryError) Unwrap() error {
	return e.Err
}

// StorageError represents an error during storage operations.
type StorageError struct {
	Operation string // Operation that failed (download, list, etc.)
	Key       string // Object key
	Err       error  // Underlying error
}

// Error implements the error interface.
func (e *StorageError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("storage error during %s for %s: %v",
			e.Operation, e.Key, e.Err)
	}
	return fmt.Sprintf("storage error during %s: %v", e.Operation, e.Err)
}

// Unwrap returns the underlying error.
func (e *StorageError) Unwrap() error {
	return e.Err
}

// IndexError represents an error during spatial index operations.
type IndexError struct {
	PackageID string // GeoPackage identifier
	Layer     string // Layer name
	Err       error  // Underlying error
}

// Error implements the error interface.
func (e *IndexError) Error() string {
	return fmt.Sprintf("index error for layer %s in package %s: %v",
		e.Layer, e.PackageID, e.Err)
}

// Unwrap returns the underlying error.
func (e *IndexError) Unwrap() error {
	return e.Err
}

// ConfigError represents a configuration error.
type ConfigError struct {
	Field   string // Configuration field
	Message string // Error message
}

// Error implements the error interface.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("configuration error for %s: %s", e.Field, e.Message)
}

// Unwrap returns the underlying error.
func (e *ConfigError) Unwrap() error {
	return ErrInvalidInput
}
