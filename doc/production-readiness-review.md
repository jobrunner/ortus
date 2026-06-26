# Production-Readiness & Architecture Review (2026-06)

A critical end-to-end review of ortus for production operation: architecture
drift, test coverage in critical paths, refactoring opportunities, and
operational risks. Findings are split into **Fixed in this branch** and
**Recommended (prioritized)**. Severity: 🔴 high · 🟠 medium · 🟢 low.

## Fixed in this branch (`analysis/hardening`)

| # | Issue | Severity | Fix |
|---|---|---|---|
| 1 | **Hot-reload served stale data.** A file-watcher *modify* event called `LoadPackage`, but both adapters' `Open` return the **cached** instance for an already-loaded id — so a changed `.gpkg`/`.zip` was never actually re-read. | 🔴 | `registry.LoadPackage` now unloads an already-loaded source first (reload semantics for all callers). Test: `TestLoadPackageReloadsModifiedSource`, `TestHandleFileEvent`. |
| 2 | **Query timeout configured but never enforced.** `query.timeout` (default 30s) was unused; a hung/expensive adapter query could pin a goroutine forever (DoS / resource exhaustion). | 🔴 | `QueryService` applies `cfg.Query.Timeout` via `context.WithTimeout` when the caller set no deadline. Test: `TestQueryTimeoutIsEnforced`. |
| 3 | **Orphaned raster temp dirs.** Bundles unpack into OS temp and are only removed on `Close`; a crash/OOM/SIGKILL leaks them until the disk fills. | 🔴 | `raster.CleanupOrphaned()` sweeps stale `ortus-raster-*` dirs at startup. Test: `TestCleanupOrphaned`. |
| 4 | **Storage path traversal.** `filepath.Join(localPath, obj.Key)` trusted remote object keys; a hostile bucket key `../../etc/…` could write outside the cache dir. | 🟠 | `registry.safeLocalPath` rejects absolute/escaping keys in `LoadAll`/`Sync`. Test: `TestSafeLocalPath`. |
| 5 | **Adapter-coupling in `app.handleFileEvent`.** Delete used `geopackage.DerivePackageID` for *all* source kinds. | 🟠 | Added `registry.DeriveSourceID`; app uses it (single source of truth, kind-agnostic). |

All green: `go build`/`test`/`-race`/`golangci-lint ./...`.

## Recommended — Architecture drift

### A1 ✅ `Package` → `Source` vocabulary migration is complete (ADR-0012)
The whole migration has shipped. Traces, metrics, the HTTP API and the MCP
tools now speak `source` consistently across both the GeoPackage and raster
adapters.
- **Stage A (internal, mechanical) — done (#49):** `PackageRegistry`→`SourceRegistry`,
  `LoadSource`/`UnloadSource`/`ListSources`/`GetSource`/`SourceCount`,
  `SourceHealth`, `input.SourceRegistry`. Compiler-checked.
- **Stage B (observability contract) — done (#49):** span names `SourceRegistry.*`,
  span attrs `ortus.source.*`, metrics `ortus.sources.{loaded,ready}` — now
  aligned across the GeoPackage and raster adapters.
- **Stage C (public, breaking) — done:** HTTP JSON keys `source_id`/`source_name`,
  route `/api/v1/sources`, MCP tool names `list_sources`/`get_source`/
  `get_source_layers`. Clean hard rename (no dual-output/alias — the service was
  not yet deployed); shipped under a breaking minor bump. See ADR-0012.

### A2 ✅ Application services depend on a port interface, not the concrete registry
`QueryService`, `HealthService`, `SyncService` now accept small consumer-side
interfaces (`sourceQuerier`/`sourceInspector`/`sourceSyncer`) instead of
`*SourceRegistry` — "accept interfaces", decoupled and mockable. (`input.*` was
not reused because it lacks `ReadySourceIDs`/`Query`/`Sync`/`SourceCount`.)

### A3 ✅ id-derivation & file-filter de-duplicated
`DeriveSourceID` and `IsSupportedSourceFile` now live once in `internal/domain`
(`sourceid.go`); the registry, raster, geopackage, storage and watcher all use
them. The dead `geopackage.DeriveSourceID` and the duplicate `isSupportedSourceFile`
copies are gone; the three duplicate tests collapsed into one domain test.

### A4 ✅ MCP↔telemetry coupling removed via a trace-query port
**Resolved.** `internal/adapters/mcp` no longer imports the telemetry adapter.
The trace DTOs (`CapturedTrace`/`CapturedSpan`/`CapturedEvent`/`ActiveSpan`/
`TraceFilter`/`Stats`) and the query surface now live in `input.TelemetryQuery`
(`internal/ports/input/telemetry.go`); the telemetry ring buffer implements that
port (type-aliasing the DTOs so its internals are unchanged), MCP depends only on
the port, and `app` wires them. The temporary depguard exception is **removed** —
depguard is green with no adapter→adapter allowance.

### Architecture harness (drift prevention)
The hexagonal import boundaries are now **enforced**, not just reviewed:
`depguard` rules in `.golangci.yml` codify domain → ports → application →
adapters → app and forbid inward/sideways imports (incl. adapter→adapter);
`gomodguard` blocks direct use of `mongo-driver` (transitive via gocog, ADR-0013);
`go mod tidy -diff` gates module hygiene; `commitlint` guards the Conventional
Commits release-please depends on. Run locally via `make arch` (folded into
`make verify`); in CI via the Lint + Architecture jobs + the Commit Lint
workflow. Findings A1–A4 are the kind of drift this harness now prevents from
recurring.

Contract-drift layer added on top: an **HTTP-route↔OpenAPI consistency test**
(`http.TestRoutesMatchOpenAPISpec` — every `/api/v1` route is documented and
vice-versa), an **MCP-tool golden snapshot** (`mcp.TestMCPToolContract` — tool
names + input schemas are frozen; a rename must update the golden), an
**`oasdiff` breaking-change gate** (`openapi-diff.yml` — breaking spec changes
vs. the PR base fail CI), and **CODEOWNERS** auto-requesting review on the
`ports/`/`domain/` seams and the harness config.

## Recommended — Operational hardening

### O1 🟠 Source-id collisions across extensions
Source ids are filename stems regardless of extension, so `foo.gpkg` and
`foo.zip` both map to id `foo` — with the new reload semantics they would
repeatedly evict each other. Recommend: reject id collisions at load with a
clear error (registry-level), or namespace by kind. Document the "ids must be
globally unique across all source files" rule.

### O2 🟠 HTTP rate limiting is configured but not applied
`config.RateLimitConfig` exists and defaults are set, but no middleware enforces
it on query endpoints (only `/api/v1/sync` self-rate-limits). Operators may
believe they're protected. Wire a limiter middleware in `http.NewServer` when
`server.rate_limit.enabled`, or remove the config to avoid a false sense of safety.

### O3 🟠 SQLite connection-pool defaults untuned
`geopackage.openDB` never sets `SetMaxOpenConns/SetMaxIdleConns/SetConnMaxLifetime`.
Under concurrent read load across many packages this can serialize or churn
connections. Add conservative read-oriented pool limits (load-test first;
queries are read-only, R-tree build happens once at load).

### O4 🟢 Health readiness is permissive when empty
`/health/ready` returns ready with **zero** sources loaded ("no_packages"), so a
freshly-started, data-less instance is added to a load balancer. Make this an
explicit policy (config flag `ready_when_empty`, default false) for k8s setups.

### O5 🟢 Partial `LoadAll`/`Sync` failures are low-visibility
One unloadable source is logged at WARN and skipped; a startup with N failed
sources still reports success. Surface a `loaded/failed` summary metric/log at
startup and consider a `require_full_load` strict mode.

## Recommended — Test coverage (still thin in critical paths)

The fixes above added the highest-risk missing tests (hot-reload routing, query
timeout, orphaned-dir cleanup, path-traversal). Remaining gaps, by risk:

| Area | Cov | Add |
|---|---|---|
| 🟠 `http/handlers.go` | 68% | `handleQueryError` (domain-error→HTTP-status table), `parseQueryParams` edges (x/y vs lon/lat, (0,0), lone lon, `properties=`), `handleSync` (nil / rate-limited 429 / error), `formatQueryResponse` (with-geometry, empty license). |
| 🟠 `mcp/tools_query.go` | 48% | `get_source` / `get_source_layers` (empty id, not-found, success) — currently ~10–14%. |
| 🟠 `geopackage` index errors | 62% | `CreateSpatialIndex` failure branches (create/populate fail, concurrent unload). |
| 🟢 `storage` s3/azure | 51% | error paths (need SDK fakes / localstack/azurite — integration). |
| 🟢 `app` Start/Shutdown | 6% | startup/shutdown ordering, panic-recovery in background servers (integration). |
| 🟢 `cmd`, `tls`, `metrics` | 0% | flag→config mapping; cert/DNS-01 (integration); metric registration. |

## Suggested sequencing
1. **This branch** (done): the four 🔴/🟠 correctness/ops fixes + their tests.
2. **Done (#49)**: ADR-0012 Stage A+B (vocabulary + observability).
3. **Then**: O1 (id collisions) + O2 (rate limiting) + the `http`/`mcp` coverage gaps.
4. **Then**: A2 (port interfaces) + A3 (dedup), O3 (pool tuning, load-tested).
5. **Done**: ADR-0012 Stage C (API/MCP hard rename to `source`; no /api/v2/aliases — pre-deployment).
