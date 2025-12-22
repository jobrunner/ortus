package application

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// mockRepository implements output.GeoPackageRepository for testing.
type mockRepository struct {
	packages map[string]*domain.GeoPackage
	features map[string][]domain.Feature
	openErr  error
}

func (m *mockRepository) Open(_ context.Context, path string) (*domain.GeoPackage, error) {
	if m.openErr != nil {
		return nil, m.openErr
	}
	if m.packages != nil {
		if pkg, ok := m.packages[path]; ok {
			return pkg, nil
		}
	}
	// Derive package ID from filename (same as derivePackageID in registry.go)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	packageID := strings.TrimSuffix(base, ext)

	return &domain.GeoPackage{
		ID:   packageID,
		Name: base,
		Path: path,
	}, nil
}

func (m *mockRepository) Close(_ context.Context, _ string) error {
	return nil
}

func (m *mockRepository) GetLayers(_ context.Context, packageID string) ([]domain.Layer, error) {
	if m.packages != nil {
		if pkg, ok := m.packages[packageID]; ok {
			return pkg.Layers, nil
		}
	}
	return nil, domain.ErrPackageNotFound
}

func (m *mockRepository) QueryPoint(_ context.Context, packageID, layerName string, _ domain.Coordinate) ([]domain.Feature, error) {
	key := packageID + ":" + layerName
	if m.features != nil {
		if features, ok := m.features[key]; ok {
			return features, nil
		}
	}
	return []domain.Feature{}, nil
}

func (m *mockRepository) CreateSpatialIndex(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockRepository) HasSpatialIndex(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// mockStorage implements output.ObjectStorage for testing.
type mockStorage struct {
	objects     []output.StorageObject
	downloadErr error
	listErr     error
}

func (m *mockStorage) List(_ context.Context) ([]output.StorageObject, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.objects, nil
}

func (m *mockStorage) Download(_ context.Context, _, _ string) error {
	return m.downloadErr
}

func (m *mockStorage) GetReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockStorage) Exists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// mockTransformer implements output.CoordinateTransformer for testing.
type mockTransformer struct {
	shouldFail bool
}

func (m *mockTransformer) Transform(_ context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if m.shouldFail {
		return domain.Coordinate{}, domain.ErrUnsupportedProjection
	}
	coord.SRID = targetSRID
	return coord, nil
}

func (m *mockTransformer) IsSupported(_, _ int) bool {
	return !m.shouldFail
}
