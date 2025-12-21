package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewLocalStorage(t *testing.T) {
	storage := NewLocalStorage("/tmp/test")

	if storage == nil {
		t.Fatal("NewLocalStorage() returned nil")
	}

	if storage.basePath != "/tmp/test" {
		t.Errorf("basePath = %q, want %q", storage.basePath, "/tmp/test")
	}
}

func TestLocalStorageList(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test files
	testFiles := []string{
		"test1.gpkg",
		"test2.gpkg",
		"subdir/nested.gpkg",
		"ignored.txt",
		"also_ignored.sqlite",
	}

	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	storage := NewLocalStorage(tmpDir)
	objects, err := storage.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	// Should only list .gpkg files
	if len(objects) != 3 {
		t.Errorf("len(objects) = %d, want 3", len(objects))
	}

	// Verify object properties
	for _, obj := range objects {
		if obj.Size != 4 { // "test" is 4 bytes
			t.Errorf("object %q size = %d, want 4", obj.Key, obj.Size)
		}
		if obj.LastModified == 0 {
			t.Errorf("object %q LastModified should not be 0", obj.Key)
		}
	}
}

func TestLocalStorageListEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage(tmpDir)
	objects, err := storage.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(objects) != 0 {
		t.Errorf("len(objects) = %d, want 0", len(objects))
	}
}

func TestLocalStorageListNonExistent(t *testing.T) {
	storage := NewLocalStorage("/nonexistent/path")
	_, err := storage.List(context.Background())
	if err == nil {
		t.Error("List() should error for non-existent path")
	}
}

func TestLocalStorageExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test file
	testFile := filepath.Join(tmpDir, "exists.gpkg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	storage := NewLocalStorage(tmpDir)

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"existing file", "exists.gpkg", true},
		{"non-existing file", "nonexistent.gpkg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := storage.Exists(context.Background(), tt.key)
			if err != nil {
				t.Errorf("Exists() error = %v", err)
			}
			if exists != tt.want {
				t.Errorf("Exists() = %v, want %v", exists, tt.want)
			}
		})
	}
}

func TestLocalStorageGetReader(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	testContent := "test content"
	testFile := filepath.Join(tmpDir, "test.gpkg")
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	storage := NewLocalStorage(tmpDir)

	reader, err := storage.GetReader(context.Background(), "test.gpkg")
	if err != nil {
		t.Fatalf("GetReader() error = %v", err)
	}
	defer func() { _ = reader.Close() }()

	buf := make([]byte, len(testContent))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != len(testContent) {
		t.Errorf("Read() n = %d, want %d", n, len(testContent))
	}
	if string(buf) != testContent {
		t.Errorf("content = %q, want %q", string(buf), testContent)
	}
}

func TestLocalStorageGetReaderNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage(tmpDir)
	_, err = storage.GetReader(context.Background(), "nonexistent.gpkg")
	if err == nil {
		t.Error("GetReader() should error for non-existent file")
	}
}

func TestLocalStorageDownload(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "ortus-src-*")
	if err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(srcDir) }()

	destDir, err := os.MkdirTemp("", "ortus-dest-*")
	if err != nil {
		t.Fatalf("failed to create dest dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(destDir) }()

	// Create source file
	testContent := "test content for download"
	srcFile := filepath.Join(srcDir, "source.gpkg")
	if err := os.WriteFile(srcFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	storage := NewLocalStorage(srcDir)
	destFile := filepath.Join(destDir, "dest.gpkg")

	err = storage.Download(context.Background(), "source.gpkg", destFile)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	// Verify destination file
	content, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("failed to read dest file: %v", err)
	}
	if string(content) != testContent {
		t.Errorf("content = %q, want %q", string(content), testContent)
	}
}

func TestLocalStorageDownloadSameFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.gpkg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	storage := NewLocalStorage(tmpDir)

	// Download to same location should be a no-op
	err = storage.Download(context.Background(), "test.gpkg", testFile)
	if err != nil {
		t.Errorf("Download() to same location should not error, got: %v", err)
	}
}

func TestLocalStorageDownloadNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ortus-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage(tmpDir)
	err = storage.Download(context.Background(), "nonexistent.gpkg", "/tmp/dest.gpkg")
	if err == nil {
		t.Error("Download() should error for non-existent source")
	}
}

func TestLocalStorageDownloadCreatesDir(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "ortus-src-*")
	if err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(srcDir) }()

	destDir, err := os.MkdirTemp("", "ortus-dest-*")
	if err != nil {
		t.Fatalf("failed to create dest dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(destDir) }()

	// Create source file
	srcFile := filepath.Join(srcDir, "source.gpkg")
	if err := os.WriteFile(srcFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	storage := NewLocalStorage(srcDir)

	// Destination in nested directory that doesn't exist yet
	destFile := filepath.Join(destDir, "nested", "deep", "dest.gpkg")

	err = storage.Download(context.Background(), "source.gpkg", destFile)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(destFile); os.IsNotExist(err) {
		t.Error("destination file should exist")
	}
}

func TestLocalStorageFullPath(t *testing.T) {
	storage := NewLocalStorage("/data/packages")

	tests := []struct {
		key  string
		want string
	}{
		{"test.gpkg", "/data/packages/test.gpkg"},
		{"subdir/nested.gpkg", "/data/packages/subdir/nested.gpkg"},
		{"", "/data/packages"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := storage.FullPath(tt.key); got != tt.want {
				t.Errorf("FullPath(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
