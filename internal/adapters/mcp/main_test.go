package mcp_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package's tests if a goroutine outlives them — a guard
// against resource leaks in this long-running service (H1).
//
// The Streamable-HTTP server sessions the SDK spins up are intentionally
// persistent: each stays alive (with a blocked jsonrpc2 read loop) until the
// client sends an explicit DELETE, and the handler only force-closes them via an
// UNEXPORTED, test-only closeAll — so a consumer cannot drain them. Tests that
// exercise raw HTTP (e.g. the bearer-auth checks) create such sessions without a
// clean MCP teardown, leaving those SDK read goroutines running at package exit
// (surfaced by the go-sdk v1.7.0 lifecycle change). Ignore exactly that SDK read
// loop by top-of-stack function so our own leaks are still caught.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/modelcontextprotocol/go-sdk/mcp.(*streamableServerConn).Read"),
	)
}
