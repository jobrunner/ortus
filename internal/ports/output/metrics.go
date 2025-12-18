package output

import "time"

// MetricsCollector defines the secondary port for metrics collection.
type MetricsCollector interface {
	// IncQueryCount increments the query counter.
	IncQueryCount(packageID string, success bool)

	// ObserveQueryDuration records query duration.
	ObserveQueryDuration(packageID string, duration time.Duration)

	// SetPackagesLoaded sets the number of loaded packages.
	SetPackagesLoaded(count int)

	// SetPackagesReady sets the number of ready packages.
	SetPackagesReady(count int)

	// IncStorageOperations increments storage operation counter.
	IncStorageOperations(operation string, success bool)

	// ObserveStorageDuration records storage operation duration.
	ObserveStorageDuration(operation string, duration time.Duration)
}

// NoOpMetrics is a no-op implementation of MetricsCollector.
type NoOpMetrics struct{}

// IncQueryCount implements MetricsCollector.
func (n *NoOpMetrics) IncQueryCount(_ string, _ bool) {}

// ObserveQueryDuration implements MetricsCollector.
func (n *NoOpMetrics) ObserveQueryDuration(_ string, _ time.Duration) {}

// SetPackagesLoaded implements MetricsCollector.
func (n *NoOpMetrics) SetPackagesLoaded(_ int) {}

// SetPackagesReady implements MetricsCollector.
func (n *NoOpMetrics) SetPackagesReady(_ int) {}

// IncStorageOperations implements MetricsCollector.
func (n *NoOpMetrics) IncStorageOperations(_ string, _ bool) {}

// ObserveStorageDuration implements MetricsCollector.
func (n *NoOpMetrics) ObserveStorageDuration(_ string, _ time.Duration) {}
