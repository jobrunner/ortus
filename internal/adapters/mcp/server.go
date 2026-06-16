// Package mcp adapts ortus to the Model Context Protocol so AI agents
// (Claude Desktop, Claude Code, …) can query traces, package metadata,
// and perform point queries via a typed tool surface.
//
// The MCP server runs on its own HTTP port (so a network policy can
// isolate it from the public REST API) and is protected by a bearer
// token. When binding to loopback, the token is optional — local
// processes are considered trusted.
//
// The same package also exports RunStdio for the local `./ortus mcp`
// CLI mode used by Claude Desktop without HTTP.
package mcp

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/domain"
)

// Deps is what every MCP server needs to do its job. We pass concrete
// pointers rather than ports here because the MCP layer is the topmost
// adapter; introducing yet another port abstraction wouldn't gain
// anything testability-wise.
type Deps struct {
	Buffer        *telemetry.RingBuffer // may be nil when tracing is off — tools degrade gracefully
	QueryService  *application.QueryService
	Registry      *application.PackageRegistry
	HealthService *application.HealthService
	Version       string
}

// Server bundles the MCP server lifecycle around an http.Server.
type Server struct {
	server  *http.Server
	handler http.Handler // exposed for tests via Handler(); used by ListenAndServe
	logger  *slog.Logger
	addr    string
}

// Handler returns the underlying http.Handler. Used by tests that want
// to drive the MCP server via httptest.Server without binding a port.
// Not part of the stable public API; the production code path is Run().
func (s *Server) Handler() http.Handler { return s.handler }

// Options configures the HTTP-mode MCP server. Token is required when
// host is not loopback (enforced by config validation).
type Options struct {
	Host  string
	Port  int
	Path  string
	Token string
}

// New constructs an HTTP-mode MCP server. Use Run / Shutdown to manage
// its lifecycle. For stdio mode (used by `./ortus mcp`), call RunStdio
// directly.
func New(opts Options, deps Deps, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	// One Server instance is reused across HTTP requests. The SDK creates
	// a fresh ServerSession per Streamable-HTTP connection internally.
	srv := buildMCPServer(deps, logger)

	mux := http.NewServeMux()
	streamHandler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Logger: logger},
	)
	mux.Handle(opts.Path, bearerAuthMiddleware(opts.Token, streamHandler))

	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	return &Server{
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		handler: mux,
		logger:  logger,
		addr:    addr,
	}
}

// Run starts the HTTP server. Returns when the server stops (cleanly or
// not). Safe to call as a background goroutine.
func (s *Server) Run() error {
	s.logger.Info("starting MCP server", "address", s.addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts the MCP server down.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down MCP server")
	return s.server.Shutdown(ctx)
}

// RunStdio runs the MCP server over stdin/stdout. Use this from the
// `./ortus mcp` CLI mode for Claude Desktop integration. No auth — the
// transport is the host's stdio.
func RunStdio(ctx context.Context, deps Deps, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	srv := buildMCPServer(deps, logger)
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// buildMCPServer constructs the SDK server and registers every tool.
// Used by both transports (HTTP + stdio).
func buildMCPServer(deps Deps, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "ortus",
		Version: deps.Version,
	}, nil)

	registerDiagnosticTools(srv, deps, logger)
	registerQueryTools(srv, deps, logger)

	return srv
}

// bearerAuthMiddleware enforces a `Authorization: Bearer <token>` header
// on every request. Comparison is constant-time to avoid timing attacks.
// When token is empty (loopback-only mode), it lets every request
// through — the caller is responsible for ensuring the listener is
// loopback in that case.
func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		want := "Bearer " + token
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ortus-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Convenience type aliases so the per-tool files don't need to import
// the SDK and the domain package together.
type (
	toolCtx       = context.Context
	callRequest   = mcp.CallToolRequest
	callResult    = mcp.CallToolResult
	queryRequest  = domain.QueryRequest
	queryResponse = domain.QueryResponse
	coordinate    = domain.Coordinate
)
