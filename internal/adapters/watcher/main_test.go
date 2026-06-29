package watcher

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package's tests if any goroutine outlives them — the
// watcher spawns a long-lived fsnotify loop, so a Watcher that isn't Stopped
// (or a leaked event goroutine) is exactly the kind of bug this catches.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
