package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// AzureStorage implements ObjectStorage for Azure Blob Storage.
type AzureStorage struct {
	client    *azblob.Client
	container string
	prefix    string
}

// AzureConfig holds Azure Blob Storage configuration.
type AzureConfig struct {
	Container        string
	AccountName      string
	AccountKey       string
	ConnectionString string
	Prefix           string
}

// NewAzureStorage creates a new Azure Blob Storage adapter.
func NewAzureStorage(cfg AzureConfig) (*AzureStorage, error) {
	var client *azblob.Client
	var err error

	if cfg.ConnectionString != "" {
		client, err = azblob.NewClientFromConnectionString(cfg.ConnectionString, nil)
	} else {
		// Build connection string from account name and key
		url := "https://" + cfg.AccountName + ".blob.core.windows.net/"
		cred, err := azblob.NewSharedKeyCredential(cfg.AccountName, cfg.AccountKey)
		if err != nil {
			return nil, err
		}
		client, err = azblob.NewClientWithSharedKeyCredential(url, cred, nil)
		if err != nil {
			return nil, err
		}
	}

	if err != nil {
		return nil, err
	}

	return &AzureStorage{
		client:    client,
		container: cfg.Container,
		prefix:    cfg.Prefix,
	}, nil
}

// List returns all GeoPackage files in the Azure container.
func (s *AzureStorage) List(ctx context.Context) ([]output.StorageObject, error) {
	var objects []output.StorageObject

	pager := s.client.NewListBlobsFlatPager(s.container, &azblob.ListBlobsFlatOptions{
		Prefix: &s.prefix,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, blob := range page.Segment.BlobItems {
			obj, ok := s.blobToStorageObject(blob)
			if ok {
				objects = append(objects, obj)
			}
		}
	}

	return objects, nil
}

// blobToStorageObject converts an Azure blob to a StorageObject.
// Returns false if the blob should be skipped (not a .gpkg file).
func (s *AzureStorage) blobToStorageObject(blob *container.BlobItem) (output.StorageObject, bool) {
	name := *blob.Name

	// Only include .gpkg files
	if !strings.HasSuffix(strings.ToLower(name), ".gpkg") {
		return output.StorageObject{}, false
	}

	// Remove prefix from key
	relKey := strings.TrimPrefix(name, s.prefix)
	relKey = strings.TrimPrefix(relKey, "/")

	obj := output.StorageObject{
		Key: relKey,
	}

	s.extractBlobProperties(blob, &obj)
	return obj, true
}

// extractBlobProperties extracts properties from an Azure blob.
func (s *AzureStorage) extractBlobProperties(blob *container.BlobItem, obj *output.StorageObject) {
	if blob.Properties == nil {
		return
	}
	if blob.Properties.ContentLength != nil {
		obj.Size = *blob.Properties.ContentLength
	}
	if blob.Properties.LastModified != nil {
		obj.LastModified = blob.Properties.LastModified.Unix()
	}
	if blob.Properties.ETag != nil {
		obj.ETag = string(*blob.Properties.ETag)
	}
}

// Download downloads a blob from Azure to the local filesystem.
func (s *AzureStorage) Download(ctx context.Context, key string, dest string) error {
	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}

	// Download blob
	resp, err := s.client.DownloadStream(ctx, s.container, s.fullKey(key), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Write to file
	f, err := os.Create(dest) //#nosec G304 -- dest is a controlled local path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, resp.Body)
	return err
}

// GetReader returns a reader for the given blob.
func (s *AzureStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.client.DownloadStream(ctx, s.container, s.fullKey(key), nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Exists checks if a blob exists in Azure.
func (s *AzureStorage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.DownloadStream(ctx, s.container, s.fullKey(key), &azblob.DownloadStreamOptions{
		Range: azblob.HTTPRange{Offset: 0, Count: 1},
	})
	if err != nil {
		return false, nil //nolint:nilerr // error indicates blob doesn't exist, which is not an error condition for Exists
	}
	return true, nil
}

// fullKey returns the full blob name including prefix.
func (s *AzureStorage) fullKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}
