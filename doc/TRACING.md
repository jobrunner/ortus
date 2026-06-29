# Tracing & Observability

Ortus emits OpenTelemetry traces for HTTP requests, query execution, GeoPackage
repository operations, storage I/O, and sync runs. The data is exported in two
places at once:

1. **In-memory ring buffer** — keeps the last N completed traces (default 256)
   so the upcoming MCP server can return concrete trace data to Claude without
   talking to an external backend.
2. **OTLP exporter** — sends spans to a collector (Jaeger, Tempo, OpenTelemetry
   Collector, etc.) when `tracing.endpoint` is configured.

Both feeds are wired off the same `TracerProvider`, so there is no duplication
and no risk of drift.

## Enable tracing

The simplest way:

```bash
./ortus --tracing --tracing-endpoint=localhost:4318
```

Or via environment variables:

```bash
ORTUS_TRACING_ENABLED=true \
ORTUS_TRACING_ENDPOINT=localhost:4318 \
ORTUS_TRACING_TRANSPORT=http \
./ortus
```

Or via `config.yaml`:

```yaml
tracing:
  enabled: true
  service_name: ortus
  environment: prod
  endpoint: otel-collector.observability:4318
  transport: http        # "http" (default) or "grpc"
  insecure: true         # disable TLS to the collector
  sample_ratio: 1.0      # 0.0..1.0; 1.0 = AlwaysOn, 0.0 = NeverSample
  buffer_size: 256       # traces retained for the MCP server
  headers:
    Authorization: Bearer <token>
  attributes:
    cluster: prod-eu
```

### Sampling

The sampler is `ParentBased(...)`:

- `sample_ratio >= 1.0` → `AlwaysSample` (default — useful in dev/test).
- `0.0 < sample_ratio < 1.0` → `TraceIDRatioBased(ratio)`.
- `sample_ratio <= 0.0` → `NeverSample` (spans only emitted for incoming traced
  requests).

Whatever the local decision, an incoming request that already carries a
sampled trace context is honoured (W3C `traceparent` + `tracestate`).

## What gets traced

Every named operation produces a span; a coverage test in
`internal/adapters/telemetry/coverage_test.go` enforces the full list below.

| Span name                              | Where                          | Notable attributes                                       |
|----------------------------------------|--------------------------------|----------------------------------------------------------|
| `GET /api/v1/query/{sourceId}` (etc.) | `otelmux` middleware           | standard `http.*`                                        |
| `mcp.tools/call` (etc.)                | `internal/adapters/mcp`        | `mcp.tool.name` (one span per received MCP method)       |
| `App.Startup` / `App.Shutdown`         | `internal/app`                 | `ortus.{tracing,metrics,sync,watcher}.enabled`           |
| `App.handleFileEvent`                  | `internal/app`                 | `watcher.{path,operation}`                               |
| `Watcher.handle`                       | `internal/adapters/watcher`    | `watcher.{path,operation}`                               |
| `QueryService.QueryPoint`              | `internal/application`         | `ortus.coordinate.{x,y,srid}`, `ortus.sources.queried`  |
| `QueryService.QueryPointInSource`     | `internal/application`         | `ortus.source.{id,name}`, `ortus.features.count`        |
| `QueryService.queryLayer`              | `internal/application`         | `ortus.layer.{name,srid,geometry_type}`                  |
| `QueryService.transformCoordinate`     | `internal/application`         | `ortus.coordinate.{from_srid,to_srid}`                   |
| `SourceRegistry.LoadAll`              | `internal/application`         | `ortus.sources.{loaded,failed}`                         |
| `SourceRegistry.LoadSource`          | `internal/application`         | `ortus.source.{id,path}`                                |
| `SourceRegistry.UnloadSource`        | `internal/application`         | `ortus.source.id`                                       |
| `SourceRegistry.ListSources`         | `internal/application`         | `ortus.sources.count`                                   |
| `SourceRegistry.GetSource`           | `internal/application`         | `ortus.source.id`                                       |
| `SourceRegistry.GetSourceStatus`     | `internal/application`         | `ortus.source.{id,status}`                              |
| `SourceRegistry.Sync`                 | `internal/application`         | `ortus.sync.{added,removed}`                             |
| `Repository.Open`                      | `internal/adapters/geopackage` | `db.system=sqlite`, `ortus.source.{id,path}`            |
| `Repository.Close`                     | `internal/adapters/geopackage` | `db.system=sqlite`, `ortus.source.id`                   |
| `Repository.QueryPoint`                | `internal/adapters/geopackage` | `db.system=sqlite`, `ortus.layer.*`, `ortus.features.count` |
| `Repository.executePointQuery`         | `internal/adapters/geopackage` | `db.statement`, `ortus.rtree.used`                       |
| `Repository.CreateSpatialIndex`        | `internal/adapters/geopackage` | `db.system=sqlite`                                       |
| `Repository.GetLayers`                 | `internal/adapters/geopackage` | `ortus.layers.count`                                     |
| `Repository.HasSpatialIndex`           | `internal/adapters/geopackage` | `db.statement`, `ortus.index.exists`                     |
| `RepositoryTransformer.Transform`      | `internal/adapters/geopackage` | `db.statement`, `ortus.coordinate.{from_srid,to_srid}`   |
| `Transformer.Transform` / `IsSupported`| `internal/adapters/geopackage` | `db.statement`, `ortus.coordinate.{from,to}_srid`        |
| `ObjectStorage.List/Download/...`      | `internal/adapters/storage`    | `storage.system`, `storage.key`                          |
| `HealthService.IsHealthy`              | `internal/application`         | `health.healthy`                                         |
| `HealthService.IsReady`                | `internal/application`         | `health.ready`, `health.reason`                          |
| `HealthService.GetHealthDetails`       | `internal/application`         | `health.sources_{loaded,ready}`                         |
| `HealthService.GetSourceHealth`       | `internal/application`         | `health.sources.count`                                  |
| `SyncService.do{Sync,SyncWithResult}`  | `internal/application`         | `sync.trigger` (`scheduled` \| `manual`)                 |

In addition, the HTTP recovery middleware records any panic on the active
span (with stack trace) and marks the span status as Error.

Every span carries the resource attributes `service.name`,
`service.version` (if built with `-ldflags`), `deployment.environment`,
plus any `tracing.attributes:` entries.

## Log correlation

When tracing is active, the HTTP request log includes `trace_id` and
`span_id`, so a log line can be jumped to the trace in Jaeger/Tempo (or the
in-memory buffer) directly.

## For the MCP server (next iteration)

The application keeps the `*telemetry.Provider` on `app.App.TelemetryProvider`.
The buffer is reachable as:

```go
buf := app.TelemetryProvider.Buffer()    // *telemetry.RingBuffer

// Finished traces — both successful and error pools, time-merged newest first.
buf.ListTraces(telemetry.TraceFilter{
    MinDuration:  10 * time.Millisecond,
    Status:       "Error",
    NameContains: "QueryPoint",
    Limit:        20,
})
buf.GetTrace(traceID)

// In-flight operations — answer to "what's running RIGHT NOW?". Essential
// for diagnosing hangs the finished pool can't see by definition.
buf.ListActive()    // []*ActiveSpan, sorted by start time, with AgeMS field

// Health/sizing.
buf.Stats()   // Capacity, TracesActive, SpansActive, TracesStored, ErrorTracesStored, Evicted
```

### Retention guarantees

The ring buffer keeps **two FIFO pools**, each of size `tracing.buffer_size`:

- Successful traces (status `Unset` / `Ok`)
- Error traces (status `Error`)

Successful traces never evict error traces. Under burst load — say a flood of
fast-succeeding queries — the last N errors remain queryable. This is the
property that makes "diagnose any past failure" actually work.

### Trace-ID in HTTP responses

Every HTTP response carries an `X-Trace-Id` header. Users reporting "GET
/api/v1/query returned 500 at 14:23" can quote it and the MCP server can
`GetTrace(id)` directly.

### Log/trace correlation

The slog logger is wrapped with a `SpanContextHandler` — any
`logger.InfoContext(ctx, ...)` call inside a traced operation
auto-includes `trace_id` and `span_id` attributes, so a stray Warn-level
log can be jumped to its parent trace.

### OTel-internal errors

Failures of the OTLP exporter (collector unreachable, auth errors, etc.) are
routed through slog at Warn level and counted via
`telemetry.OTelErrorCount()`. The MCP server can expose this so "missing
spans" gets diagnosed as "collector down" instead of "Ortus is broken".

### Outbound HTTP / SDK calls

S3 (via the AWS SDK middleware), Azure Blob (via the azcore HTTP transport),
and the HTTP storage backend all wrap their HTTP clients with `otelhttp`.
Retries, DNS resolution, TLS handshakes, and per-attempt status are visible
as child spans rather than just one aggregate `ObjectStorage.*` span.

### Background goroutine panic safety

The watcher event handler, sync ticker, and metrics server all run under
panic recovery: a panic is surfaced as a span error (with the panic value)
and logged, but the loop survives instead of crashing the process. Without
this, a single bad event could empty the entire ring buffer on restart.

## Metrics

Prometheus metrics on `/metrics` are now produced via the OTel meter API and
exported through `go.opentelemetry.io/otel/exporters/prometheus`. The metric
names (`ortus_queries_total`, `ortus_sources_loaded` …) and labels are kept
stable; only the underlying instrument library changed. This unlocks pulling
the same instrumentation through OTLP later if desired, without touching the
collectors that already scrape `/metrics`.
