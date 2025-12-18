package storage

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// HTTPStorage implements ObjectStorage for HTTP(S) downloads.
type HTTPStorage struct {
	client    *http.Client
	baseURL   string
	indexFile string
	username  string
	password  string
}

// HTTPConfig holds HTTP storage configuration.
type HTTPConfig struct {
	BaseURL   string
	IndexFile string // default: index.txt
	Timeout   time.Duration
	Username  string
	Password  string
}

// NewHTTPStorage creates a new HTTP storage adapter.
func NewHTTPStorage(cfg HTTPConfig) *HTTPStorage {
	if cfg.IndexFile == "" {
		cfg.IndexFile = "index.txt"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}

	return &HTTPStorage{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		baseURL:   strings.TrimSuffix(cfg.BaseURL, "/"),
		indexFile: cfg.IndexFile,
		username:  cfg.Username,
		password:  cfg.Password,
	}
}

// List returns all GeoPackage files listed in the index file.
func (s *HTTPStorage) List(ctx context.Context) ([]output.StorageObject, error) {
	indexURL := s.baseURL + "/" + s.indexFile

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}

	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching index file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("index file returned status %d", resp.StatusCode)
	}

	// Parse index file (one filename per line)
	var objects []output.StorageObject
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Only include .gpkg files
		if !strings.HasSuffix(strings.ToLower(line), ".gpkg") {
			continue
		}

		objects = append(objects, output.StorageObject{
			Key: line,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading index file: %w", err)
	}

	return objects, nil
}

// Download downloads a file from HTTP to the local filesystem.
func (s *HTTPStorage) Download(ctx context.Context, key string, dest string) error {
	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}

	fileURL := s.baseURL + "/" + key

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return err
	}

	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d for %s", resp.StatusCode, key)
	}

	// Write to file
	f, err := os.Create(dest) //#nosec G304 -- dest is a controlled local path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, resp.Body)
	return err
}

// GetReader returns a reader for the given file.
func (s *HTTPStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	fileURL := s.baseURL + "/" + key

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}

	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, key)
	}

	return resp.Body, nil
}

// Exists checks if a file exists via HTTP HEAD request.
func (s *HTTPStorage) Exists(ctx context.Context, key string) (bool, error) {
	fileURL := s.baseURL + "/" + key

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, fileURL, nil)
	if err != nil {
		return false, err
	}

	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return false, nil //nolint:nilerr // intentionally ignoring error when connection fails
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK, nil
}
