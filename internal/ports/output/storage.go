// Package output defines the secondary/driven ports of the application.
package output

import (
	"context"
	"io"
)

// ObjectStorage defines the secondary port for object storage operations.
type ObjectStorage interface {
	// List returns all GeoPackage files in the storage.
	List(ctx context.Context) ([]StorageObject, error)

	// Download downloads a GeoPackage file to the local filesystem.
	Download(ctx context.Context, key string, dest string) error

	// GetReader returns a reader for the given object.
	GetReader(ctx context.Context, key string) (io.ReadCloser, error)

	// Exists checks if an object exists.
	Exists(ctx context.Context, key string) (bool, error)
}

// StorageObject represents a file in object storage.
type StorageObject struct {
	Key          string // Object key/path
	Size         int64  // Size in bytes
	LastModified int64  // Unix timestamp
	ETag         string // Content hash
}

// StorageType represents the type of storage backend.
type StorageType string

const (
	StorageTypeS3    StorageType = "s3"
	StorageTypeAzure StorageType = "azure"
	StorageTypeHTTP  StorageType = "http"
	StorageTypeLocal StorageType = "local"
)
