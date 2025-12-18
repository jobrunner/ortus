// Package storage provides object storage adapters.
package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// LocalStorage implements ObjectStorage for local filesystem.
type LocalStorage struct {
	basePath string
}

// NewLocalStorage creates a new local storage adapter.
func NewLocalStorage(basePath string) *LocalStorage {
	return &LocalStorage{basePath: basePath}
}

// List returns all GeoPackage files in the local directory.
func (s *LocalStorage) List(_ context.Context) ([]output.StorageObject, error) {
	var objects []output.StorageObject

	err := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Only include .gpkg files
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".gpkg") {
			return nil
		}

		relPath, err := filepath.Rel(s.basePath, path)
		if err != nil {
			return err
		}

		objects = append(objects, output.StorageObject{
			Key:          relPath,
			Size:         info.Size(),
			LastModified: info.ModTime().Unix(),
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	return objects, nil
}

// Download copies a file to the destination (no-op for local storage).
func (s *LocalStorage) Download(_ context.Context, key string, dest string) error {
	srcPath := filepath.Join(s.basePath, key)

	// Normalize paths for comparison to handle ./path vs path differences
	cleanSrc := filepath.Clean(srcPath)
	cleanDest := filepath.Clean(dest)

	// If source and dest are the same, nothing to do
	if cleanSrc == cleanDest {
		return nil
	}

	// Create destination directory if needed
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}

	// Copy file
	src, err := os.Open(srcPath) //#nosec G304 -- srcPath is constructed from basePath
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(dest) //#nosec G304 -- dest is a controlled local path
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()

	_, err = io.Copy(dst, src)
	return err
}

// GetReader returns a reader for the given object.
func (s *LocalStorage) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(s.basePath, key)) //#nosec G304 -- path is constructed from basePath
}

// Exists checks if a file exists.
func (s *LocalStorage) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(s.basePath, key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// FullPath returns the full path for a key.
func (s *LocalStorage) FullPath(key string) string {
	return filepath.Join(s.basePath, key)
}
