// Package gazetteer provides the reverse-geocoding and bearing ("Peilung")
// service. It orchestrates the spatial-index output port and (from M2) a pure
// salience strategy. The service is inert until it is enabled and wired with a
// spatial index, so the composition root can leave it unwired with no effect on
// the generic query path.
package gazetteer

import (
	"context"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ErrDisabled is returned by an inert gazetteer service — one that has not been
// enabled or has not been wired with a spatial index.
var ErrDisabled = fmt.Errorf("gazetteer disabled: %w", domain.ErrUnavailable)

// Service is the GazetteerService skeleton. M0 establishes the seam (ports +
// domain types) without query logic; Locate/Bearing report the feature is not
// yet available so the composition root can leave it inert.
type Service struct {
	index   output.SpatialIndex
	enabled bool
}

// NewService creates a gazetteer service. It is inert unless enabled is true and
// a spatial index is supplied; an inert service returns ErrDisabled from its
// query methods.
func NewService(index output.SpatialIndex, enabled bool) *Service {
	return &Service{index: index, enabled: enabled}
}

// Locate reverse-geocodes a coordinate to its administrative hierarchy.
func (s *Service) Locate(_ context.Context, _ domain.Coordinate) (*domain.Locality, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("gazetteer Locate: %w", domain.ErrUnsupported)
}

// Bearing returns the most salient nearby place as a bearing fix.
func (s *Service) Bearing(_ context.Context, _ domain.Coordinate, _ domain.BearingPolicy) (*domain.Fix, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("gazetteer Bearing: %w", domain.ErrUnsupported)
}

// ready reports whether the service has been enabled and wired with a spatial
// index; until both hold it is inert and returns ErrDisabled.
func (s *Service) ready() error {
	if !s.enabled || s.index == nil {
		return ErrDisabled
	}
	return nil
}

// Compile-time assertion that the service satisfies its driving port.
var _ input.Gazetteer = (*Service)(nil)
