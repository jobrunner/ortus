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

### A1 🔴 `Package` → `Source` vocabulary migration is unfinished (ADR-0012)
The domain is `Source`, but the application, observability, HTTP API and MCP
still say "Package", and the **raster adapter already uses `source`** — so traces
are now inconsistent (`ortus.package.id` from the GeoPackage path vs
`ortus.source.id` from the raster path). This is the largest drift.
- **Stage A (internal, mechanical):** `PackageRegistry`→`SourceRegistry`,
  `LoadPackage`/`UnloadPackage`/`ListPackages`/`GetPackage`/`PackageCount`/
  `derivePackageID`, `PackageHealth`, `input.PackageRegistry`. Compiler-checked.
- **Stage B (observability contract):** span names `PackageRegistry.*`, span
  attrs `ortus.package.*`, metrics `ortus.packages.{loaded,ready}` → `…sources…`.
  Align the GeoPackage adapter's `ortus.package.*` attrs with the raster adapter.
  Bundle into one release; note in CHANGELOG (dashboards/alerts break).
- **Stage C (public, breaking):** HTTP JSON keys `package_id`/`package_name`,
  route `/api/v1/packages`, MCP tool names `list_packages`/`get_package`/
  `get_package_layers`. Needs `/api/v2` or dual-output + MCP tool aliases.
- Execute A+B as one internal PR; C as a separate versioned PR. See ADR-0012.

### A2 🟠 Application services depend on the concrete `*PackageRegistry`
`QueryService`, `HealthService`, `SyncService` take the concrete registry, not
the `input.*` port. Driving ports are otherwise wired (HTTP/MCP). Injecting the
interface would complete the hexagon and ease mocking. (The new `DeriveSourceID`
should join the registry port when A1 lands.)

### A3 🟢 Duplicated id-derivation & file-filter
Three id-derivation copies (`registry.derivePackageID`, `geopackage.DerivePackageID`,
`raster.deriveSourceID`) and two `isSupportedSourceFile` copies (storage, watcher).
Consolidate after A1 (the registry is now the canonical deriver via `DeriveSourceID`).

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
| 🟠 `mcp/tools_query.go` | 48% | `get_package` / `get_package_layers` (empty id, not-found, success) — currently ~10–14%. |
| 🟠 `geopackage` index errors | 62% | `CreateSpatialIndex` failure branches (create/populate fail, concurrent unload). |
| 🟢 `storage` s3/azure | 51% | error paths (need SDK fakes / localstack/azurite — integration). |
| 🟢 `app` Start/Shutdown | 6% | startup/shutdown ordering, panic-recovery in background servers (integration). |
| 🟢 `cmd`, `tls`, `metrics` | 0% | flag→config mapping; cert/DNS-01 (integration); metric registration. |

## Suggested sequencing
1. **This branch** (done): the four 🔴/🟠 correctness/ops fixes + their tests.
2. **Next PR**: ADR-0012 Stage A+B (vocabulary + observability) — large but mechanical.
3. **Then**: O1 (id collisions) + O2 (rate limiting) + the `http`/`mcp` coverage gaps.
4. **Then**: A2 (port interfaces) + A3 (dedup), O3 (pool tuning, load-tested).
5. **Separate, versioned**: ADR-0012 Stage C (API/MCP renames behind /api/v2 + aliases).
