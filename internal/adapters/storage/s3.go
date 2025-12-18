package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// S3Storage implements ObjectStorage for AWS S3.
type S3Storage struct {
	client *s3.Client
	bucket string
	prefix string
}

// S3Config holds S3 configuration.
type S3Config struct {
	Bucket          string
	Region          string
	Prefix          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

// NewS3Storage creates a new S3 storage adapter.
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	var opts []func(*config.LoadOptions) error

	opts = append(opts, config.WithRegion(cfg.Region))

	// Use explicit credentials if provided
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID,
				cfg.SecretAccessKey,
				"",
			),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	var clientOpts []func(*s3.Options)
	if cfg.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)

	return &S3Storage{
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
	}, nil
}

// List returns all GeoPackage files in the S3 bucket.
func (s *S3Storage) List(ctx context.Context) ([]output.StorageObject, error) {
	var objects []output.StorageObject

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)

			// Only include .gpkg files
			if !strings.HasSuffix(strings.ToLower(key), ".gpkg") {
				continue
			}

			// Remove prefix from key
			relKey := strings.TrimPrefix(key, s.prefix)
			relKey = strings.TrimPrefix(relKey, "/")

			objects = append(objects, output.StorageObject{
				Key:          relKey,
				Size:         aws.ToInt64(obj.Size),
				LastModified: obj.LastModified.Unix(),
				ETag:         strings.Trim(aws.ToString(obj.ETag), "\""),
			})
		}
	}

	return objects, nil
}

// Download downloads a file from S3 to the local filesystem.
func (s *S3Storage) Download(ctx context.Context, key string, dest string) error {
	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}

	// Get object from S3
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
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

// GetReader returns a reader for the given object.
func (s *S3Storage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Exists checks if an object exists in S3.
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		// Check if it's a not found error
		return false, nil //nolint:nilerr // error indicates object doesn't exist, which is not an error condition for Exists
	}
	return true, nil
}

// fullKey returns the full S3 key including prefix.
func (s *S3Storage) fullKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}
