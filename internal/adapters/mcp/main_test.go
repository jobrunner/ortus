package mcp_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package's tests if a goroutine outlives them — a guard
// against resource leaks in this long-running service (H1).
//
// This deliberately carries no ignore rules. An earlier revision ignored the
// SDK's streamableServerConn.Read loop on the assumption that those sessions
// are drained by the client's DELETE. They are not: the stateful handler also
// creates sessions it never hands out an Mcp-Session-Id for, which nothing can
// close. Serving the transport statelessly removes those sessions entirely (see
// the Stateless option in server.go), so the gate is honest again — every
// leftover goroutine is now a real leak.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
