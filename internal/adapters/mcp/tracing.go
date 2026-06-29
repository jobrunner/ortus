package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// tracingMiddleware wraps every received MCP method in a span. Unlike the
// GeoPackage/raster repositories (which instrument rich, method-internal
// attributes inline) and the storage adapter (a thin TracedStorage decorator),
// the MCP surface had no spans at all — this closes that gap at the single
// receive chokepoint, so a tool call and its downstream query/health spans
// share one trace. For tools/call the tool name is recorded as an attribute.
func tracingMiddleware(tracer output.Tracer) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx, span := tracer.Start(ctx, "mcp."+method, output.WithSpanKind(output.SpanKindServer))
			defer span.End()

			if ctr, ok := req.(*mcp.CallToolRequest); ok && ctr.Params != nil {
				span.SetAttributes(output.String("mcp.tool.name", ctr.Params.Name))
			}

			res, err := next(ctx, method, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(output.StatusError, "mcp method failed")
			} else {
				span.SetStatus(output.StatusOK, "")
			}
			return res, err
		}
	}
}
