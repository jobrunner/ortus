package http

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package's tests if a goroutine outlives them — a guard
// against resource leaks in this long-running service (H1).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
