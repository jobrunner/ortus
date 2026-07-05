# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.15.0](https://github.com/jobrunner/ortus/compare/v0.14.0...v0.15.0) (2026-07-05)


### Features

* **gazetteer:** enrich /query by default + dataset attribution ([0624bd0](https://github.com/jobrunner/ortus/commit/0624bd0268959d24f83fdd91a095301055411ba4))
* **gazetteer:** enrich /query by default + expose dataset attribution ([a972961](https://github.com/jobrunner/ortus/commit/a9729613455e7dd4d0e5e0c5768db632b72381c4))


### Bug Fixes

* **gazetteer:** address PR review on default-on /query enrichment ([bfd4dc7](https://github.com/jobrunner/ortus/commit/bfd4dc7d88aec7a167d34416cd88b3adcf47399d))

## [0.14.0](https://github.com/jobrunner/ortus/compare/v0.13.0...v0.14.0) (2026-07-04)


### Features

* **gazetteer:** add /gazetteer endpoint + with-gazetteer + wiring (M4 part 2a) ([43381b1](https://github.com/jobrunner/ortus/commit/43381b1d13d919b717af30ea8525ec6a17f649a5))
* **gazetteer:** add gazetteer seam + inert skeleton (M0) ([6e6dd9e](https://github.com/jobrunner/ortus/commit/6e6dd9e15eaa47a0d13a1d0c09459b5faaf701a5))
* **gazetteer:** add Locate() + manifest/level-resolver seam (M2) ([b3b53f0](https://github.com/jobrunner/ortus/commit/b3b53f03854b898d31adb31afc2e8300cd26e588))
* **gazetteer:** add manifest/sidecar parsers + config surface (M4 part 1) ([eb7d0b1](https://github.com/jobrunner/ortus/commit/eb7d0b15e78ef0272fdba4043843da98e1296c5d))
* **gazetteer:** add MCP gazetteer tool (M4 part 2b) ([fd0f6c0](https://github.com/jobrunner/ortus/commit/fd0f6c0d958c5aa85a0741391bf460a7f7d1fa54))
* **gazetteer:** add name provenance and admin-level meaning to responses ([eb0a1cf](https://github.com/jobrunner/ortus/commit/eb0a1cf47da72480cd1a039d1932cc57797341ef))
* **gazetteer:** add salience strategy + Bearing() end-to-end (M3) ([a58f6b3](https://github.com/jobrunner/ortus/commit/a58f6b35f19f407909b6ad077bedd452aeaed841))
* **gazetteer:** add SpatialIndex cgo adapter (M1) ([8d79c92](https://github.com/jobrunner/ortus/commit/8d79c9239a2319c7730a48eb7981775ce756fe45))
* **gazetteer:** add SRID verification probe + plan status (M5) ([c37b259](https://github.com/jobrunner/ortus/commit/c37b259546639c83d0de13d809a243c5b72bf8c8))
* **gazetteer:** name provenance + admin-level meaning in responses ([efb8992](https://github.com/jobrunner/ortus/commit/efb89923f00243d367e39cfa7ff0de0e0b3fbbb2))
* **gazetteer:** proximity override (5 km) + config-injectable radii ([660e89c](https://github.com/jobrunner/ortus/commit/660e89ce9eb172c32eb9f333578a624d40619a74))
* **gazetteer:** reverse-geocoding + bearing (Peilung) — M0–M5, disabled by default ([dcd320d](https://github.com/jobrunner/ortus/commit/dcd320d4ed993f918877cc9ac34ff6eb633b7e11))
* **gazetteer:** strengthen VerifySRID to a magnitude check ([f4335d8](https://github.com/jobrunner/ortus/commit/f4335d8d61e5f8f96da4e080efc3b17a2e306d89))
* **geopackage:** tunable SQLite options + local load-test harness ([5bf6ef8](https://github.com/jobrunner/ortus/commit/5bf6ef844f0832ab8c5435736d439ceadefc7cfb))
* **geopackage:** tunable SQLite options + local load-test harness (O3) ([6acbd9f](https://github.com/jobrunner/ortus/commit/6acbd9fc37daaf97f50a6ca4e1a1e7355e9e566b))
* **http:** opt-in per-IP rate limiting (O2) ([20b6fc0](https://github.com/jobrunner/ortus/commit/20b6fc0b95664d5798c69cdb2ba62361823090a7))
* **http:** opt-in per-IP rate limiting (O2) ([a0fc537](https://github.com/jobrunner/ortus/commit/a0fc537fb3d74f5a33f8f56c1a9176d3e2bf3133))
* **loadtest:** observable load-test stack — Grafana single pane (metrics+traces+logs) + Vegeta ([172394f](https://github.com/jobrunner/ortus/commit/172394f8cfcda646d43a9ace35abded30099a120))
* **loadtest:** observable load-test stack (Grafana/Tempo/Loki/Prometheus + Vegeta) ([53047ab](https://github.com/jobrunner/ortus/commit/53047ab00dfb7cdbfe37fdc4a493d5b1d6274236))
* **mcp:** entry-point tracing via receiving middleware (D8) ([18d0f13](https://github.com/jobrunner/ortus/commit/18d0f136e5d62e8c427ff006c365a30a5c784ec4))
* **mcp:** entry-point tracing via receiving middleware (D8) ([3c67415](https://github.com/jobrunner/ortus/commit/3c67415610c5ea4c6b3602d3209b49554f7ba394))
* **ops:** readiness latch, load-failure visibility, source-id collision guard ([b87e3ab](https://github.com/jobrunner/ortus/commit/b87e3abd4436785cb569d60567264f7238c5b62a))
* **ops:** readiness latch, load-failure visibility, source-id collision guard (O1/O4/O5) ([dc874fe](https://github.com/jobrunner/ortus/commit/dc874fe4cae2d201b8578022625b432ccde2454d))


### Bug Fixes

* **app:** idempotent transformer close + rollback on New error (review) ([515581e](https://github.com/jobrunner/ortus/commit/515581e3414d44a58b2ca161384c2549f05a2a49))
* **ci:** point SPATIALITE_LIBRARY_PATH at the installed lib in Test job ([2645c50](https://github.com/jobrunner/ortus/commit/2645c5013fc58ea11e451406a387806dd6c59c43))
* **debt:** harden debt-guard per review (baseline check + backtick literals) ([12ed104](https://github.com/jobrunner/ortus/commit/12ed1045e4d6c2594355f234bf705e497d3ab751))
* **debt:** location-independent pre-commit + Go-only Claude hook (review) ([93653cd](https://github.com/jobrunner/ortus/commit/93653cd78a4cf85a0feb3bb3c9edc536adb457f9))
* **debt:** run SpatiaLite in CI for coverage + harden ratchet scripts ([8c1b9c0](https://github.com/jobrunner/ortus/commit/8c1b9c06792ed1379cbb891294be87e514e6ed12))
* **gazetteer:** address Copilot review — SRID guard, manifest-driven ResolveChain, stricter validation ([60f8e4a](https://github.com/jobrunner/ortus/commit/60f8e4a0cb95ad06adcf10f71d440a752da0a56c))
* **gazetteer:** address Copilot review on the fixture PR ([d54227c](https://github.com/jobrunner/ortus/commit/d54227c9566945db6d5f930c049c02332bb6f986))
* **gazetteer:** address PR review — fail-fast level ref, honest docs/examples ([c5eca05](https://github.com/jobrunner/ortus/commit/c5eca051450e5e1cc359dc7257fac48a6e70d735))
* **gazetteer:** harden per Copilot re-review — manifest identifiers, error propagation, ctx ([f7a833e](https://github.com/jobrunner/ortus/commit/f7a833e6942288e39e96c518849e42de5c203475))
* **geopackage:** harden SQLite DSN build and load-test error handling ([633ad82](https://github.com/jobrunner/ortus/commit/633ad824994ed4ec621648bf51ebac01b2a453ba))
* **http:** harden rate-limiter per Copilot review ([f3295d3](https://github.com/jobrunner/ortus/commit/f3295d3ad8c2dbf39037fc3f08c02ecb9743294d))
* **loadtest:** address review + fix prod compose metrics scrape ([861701d](https://github.com/jobrunner/ortus/commit/861701debd7bbbf93bdeb49c804943a5337dabb0))
* **ops:** harden readiness + collision-guard edge cases (Copilot review) ([d79ecd1](https://github.com/jobrunner/ortus/commit/d79ecd1d6769628a175073cb918726791be32de9))
* **skills:** address PR review on the build skills ([f25e6db](https://github.com/jobrunner/ortus/commit/f25e6dbaad12e983463b109d7242b2d195e68e49))
* **storage:** clean base in safeJoin + drop stale config example (review) ([1d26567](https://github.com/jobrunner/ortus/commit/1d26567e24d5960138677f0ea11900c44d96e5c1))
* **storage:** consistent error typing + transport-error + transformer init (D1–D3) ([3fc959e](https://github.com/jobrunner/ortus/commit/3fc959ed6207173c44a2502820906092ef31df50))
* **storage:** consistent error typing + transport-error + transformer init (D1–D3) ([5bb01a7](https://github.com/jobrunner/ortus/commit/5bb01a7ca097a3185d9d3524142a9ae448def2f4))

## [0.13.0] - 2026-06-25

### Changed (BREAKING — public API & MCP vocabulary, ADR-0012 Stage C)
- **HTTP API:** routes `/api/v1/packages*` → `/api/v1/sources*` (`/sources`,
  `/sources/{id}`, `/sources/{id}/layers`); the `{packageId}` path parameter is now
  `{sourceId}`. JSON keys `package_id`/`package_name` → `source_id`/`source_name`,
  the source-list key `packages` → `sources`, health `packages_{loaded,ready}` →
  `sources_{loaded,ready}`, and sync `packages_{added,removed,total}` →
  `sources_{added,removed,total}`.
- **MCP tools:** `list_packages`/`get_package`/`get_package_layers` →
  `list_sources`/`get_source`/`get_source_layers`; the `package_id` tool argument →
  `source_id`.
- **Domain:** `QueryResult.PackageID/PackageName` → `SourceID/SourceName`,
  `QueryRequest.PackageID` → `SourceID`, `domain.ErrPackageNotFound` →
  `ErrSourceNotFound`.
- Clean hard rename with **no compatibility shim** — no `/api/v2`, no dual-output,
  no MCP tool aliases: the service is not yet deployed, so the old names are removed
  outright. The embedded OpenAPI spec, Swagger UI, and query frontend are updated to
  match. This completes ADR-0012; the codebase, observability, API and MCP now speak
  `source` uniformly.

### Changed (internal vocabulary + observability — ADR-0012 Stage A+B)
- Renamed the source abstraction from "Package" to "Source" across the
  application core: `PackageRegistry`→`SourceRegistry`, `LoadPackage`/
  `UnloadPackage`/`ListPackages`/`GetPackage`/`GetPackageStatus`/`ReadyPackageIDs`/
  `PackageCount`→`LoadSource`/`UnloadSource`/`ListSources`/`GetSource`/
  `GetSourceStatus`/`ReadySourceIDs`/`SourceCount`, `PackageHealth`→`SourceHealth`,
  `QueryPointInPackage`→
  `QueryPointInSource`, and the `input.PackageRegistry` port→`input.SourceRegistry`.
- **Observability rename (breaks dashboards/alerts):** span names
  `PackageRegistry.*`→`SourceRegistry.*`, span attributes `ortus.package.*`→
  `ortus.source.*` (now consistent across the GeoPackage *and* raster adapters),
  metrics `ortus.packages.{loaded,ready}`→`ortus.sources.{loaded,ready}`.
- **Public API/MCP left unchanged in Stage A+B** — the breaking public rename is
  the Stage C entry above, now landing in the same release. Verified: full suite,
  including the span-name contract test and HTTP/MCP tests, stays green.

### Fixed
- **Hot-reload served stale data.** A file-watcher *modify* event reloaded a
  source, but the adapter returned its cached, pre-modification instance — the
  change never took effect. `LoadPackage` now unloads an already-loaded source
  first (reload semantics for all callers).
- **Query timeout is now enforced.** `query.timeout` was configured but unused;
  a hung/expensive adapter query could pin a goroutine indefinitely. The query
  service now applies the configured deadline via `context.WithTimeout`.

### Security
- **Storage path traversal guarded.** Remote object keys are validated before
  joining onto the local cache dir (`registry.safeLocalPath`), so a hostile
  bucket key like `../../etc/…` can no longer write outside the data directory.

### Changed
- Raster unpack directories orphaned by a crash are swept at startup
  (`raster.CleanupOrphaned`) to prevent disk exhaustion.
- `app.handleFileEvent` derives source ids via `registry.DeriveSourceID`
  (kind-agnostic) instead of the GeoPackage adapter's helper.

### Docs
- `doc/production-readiness-review.md`: critical architecture/ops/test review
  with a prioritized roadmap (incl. the unfinished ADR-0012 vocabulary migration).

## [0.10.0] - 2026-06-24

### Added
- **Raster bundle adapter** (`internal/adapters/raster`): a second `SpatialSource`
  implementation that serves point queries against raster bundles (`*.zip` = manifest
  + Cloud Optimized GeoTIFF + integer-value→attribute mapping). Unpacks and
  schema-validates the manifest against the embedded JSON Schema, samples the COG at
  the query coordinate (nearest-neighbor) via `tingold/gocog`, and maps the pixel value
  to `Feature.Properties` — the same `QueryResult` shape as GeoPackages.
- COG reader **`tingold/gocog`** adopted after a spike ([ADR-0013](doc/adr/0013-cog-reader-library.md));
  bundle COGs must use `COMPRESS=LZW` (gocog's DEFLATE decoder is broken).
- Source discovery widened to raster bundles: the file watcher and the local/HTTP
  storage listings now surface `.zip` alongside `.gpkg`.

### Changed
- The raster adapter is wired as a registry provider in `app.go` next to the GeoPackage
  adapter; bundles unpack into OS temp dirs (not the watched storage path) and are
  cleaned up on unload.

### Notes
- Dependency footprint: gocog pulls `fasthttp`, `paulmach/orb`, `golang.org/x/image`,
  and (via `orb/maptile`→`orb/geojson`) `mongo-driver/bson` is compiled into the binary.
  See ADR-0013 for the upstream-fix follow-up.

## [0.9.0] - 2026-06-23

### Added
- **`SpatialSource` output port** — the seam for plugging in additional source kinds (raster bundles) behind the existing point-query pipeline. The GeoPackage repository is its first implementation (`Supports`/`Open`/`Prepare`/`QueryPoint`/`Close`).
- **`domain.Source` with a `Kind` discriminator** (`vector`|`raster`), replacing `domain.GeoPackage` as the registry/query currency, plus a `GeomRaster` geometry-type constant.
- **Provider routing in the registry**: sources are routed to the first adapter whose `Supports` matches; each entry records its owning adapter and `registry.Query` delegates to it. New `domain.ErrUnsupportedSource`.
- **Raster bundle design docs** under `doc/raster-bundle/` (bundle spec, JSON schema, Köppen reference pipeline, implementation plan) and **ADR-0012** for the staged `Package → Source` vocabulary migration.
- **`input.Syncer` driving port** + `input.SyncResult`.

### Changed
- **HTTP and MCP adapters now depend on the driving ports** (`input.QueryService`/`PackageRegistry`/`HealthChecker`/`Syncer`) instead of concrete application services — no adapter imports `internal/application` anymore. Compile-time assertions guard the contracts.
- Moved `ErrRateLimited` to the domain package.

### Removed
- Dead `output.GeoPackageRepository` port and the unused `Repository.GetConnection() *sql.DB` accessor (a database-handle leak out of the adapter).

### Tests
- Integration tests for the SpatiaLite query engine against a real GeoPackage fixture (point-in-polygon incl. fallback scan + R-tree path, `scanFeature`, index create/probe, coordinate transform): geopackage coverage **4% → ~62%**.
- Provider-routing tests (`providerFor`, `Query` delegation, `ErrUnsupportedSource`, nil-repo guard).
- New coverage for previously thin packages: **config 0% → 95%**, **watcher 11% → 79%** (incl. a real fsnotify hot-reload test), **storage 14% → 51%** (HTTP adapter via `httptest`, traced-storage decorator).

## [0.8.1] - 2026-06-16

### Security
- Pinned `aquasecurity/trivy-action` from mutable `@master` to `@0.36.0` in both `ci.yml` and `docker-release.yml`. Removes the supply-chain risk of a mid-tag-bump hijack.
- Tightened workflow `permissions:` to least-privilege. Top-level `contents: read` on every workflow; jobs that need more (image push, SARIF upload, GitHub release creation) override explicitly. Previously `docker-release.yml` granted `packages: write` and `security-events: write` to every job indiscriminately, and `release.yml` granted `contents: write` workflow-wide.
- Fixed a real `template-injection` finding in `ci.yml`: the PR base-ref was interpolated directly into a `run:` bash block via `${{ github.event.pull_request.base.ref }}`. Now injected via `env:` so a maliciously-named branch can't break out of the bash context.
- Disabled the Go module cache in the release workflow (`actions/setup-go` `cache: false`). Stops a poisoned cache from another job ending up baked into a release artifact.

### Added
- **actionlint** as a CI job (`actions-lint`). Runs on every PR/push, catches workflow-level bugs and Actions-specific security anti-patterns. shellcheck integration filtered to severity=error so style suggestions don't gate.
- **zizmor** weekly scan workflow (`actions-security.yml`) at Mon 06:30 UTC. SARIF results upload to the Security tab; high-severity findings open or comment on a `security`-labelled GitHub issue (same pattern as `vuln-scan.yml`).
- **`.github/zizmor.yml`** config: documents the tag-pinning + Dependabot policy (vs. SHA-pinning) and disables a small number of audits that are noise at v0.x (`artipacked`, `dependabot-cooldown`).

## [0.8.0] - 2026-06-16

### Added
- **MCP (Model Context Protocol) server** in-process, off by default. Exposes 9 read-only tools to AI agents — 5 diagnostic (`list_traces`, `get_trace`, `list_active_spans`, `tracing_stats`, `health`) backed directly by the tracing ring buffer, and 4 query (`query_point`, `list_packages`, `get_package`, `get_package_layers`) backed by the existing application services. The diagnostic tools are the payoff for the tracing infrastructure built in PR #13: an agent can now debug ortus through a structured conversation rather than `kubectl logs`.
- Two MCP transports: **streamable HTTP** on its own port (default 9091, separate from the public REST API so a NetworkPolicy can isolate it) for remote agents, and **stdio** via `./ortus mcp` for Claude Desktop integration.
- Bearer-token authentication from `ORTUS_MCP_TOKEN` env var (never the config file). Constant-time comparison against timing attacks. Loopback hosts (`127.0.0.1`, `localhost`, `::1`) exempt from the token requirement — local processes are considered trusted.
- New config block `mcp.{enabled,host,port,path}` + `ORTUS_MCP_TOKEN` env var.
- `doc/MCP.md` with the tool catalogue, auth model, Claude Desktop integration walkthrough, and limitations.

### Build
- Added `github.com/modelcontextprotocol/go-sdk` v1.6.1 as a direct dependency (official Anthropic Go SDK).

## [0.7.1] - 2026-06-13

### Build
- Bump Go toolchain from 1.25.8 to 1.26.4 across the build chain: `flake.nix` (`pkgs.go_1_24` → `pkgs.go_1_26`), `flake.lock` (nixpkgs bumped to 2026-06-10 to expose `go_1_26`), `go.mod` (`go 1.26.0` + new `toolchain go1.26.4` directive), `.github/workflows/ci.yml` and `release.yml` (`GO_VERSION` env). Clears 10 stdlib CVEs the Security job was flagging (GO-2026-{5039,5037,4982,4980,4971,4947,4946,4918,4870,4865} in `net/textproto`, `crypto/x509`, `html/template`, `net`, `net/http`, `crypto/tls`). `govulncheck ./...` now reports 0 reachable vulnerabilities.

### Added
- `.github/dependabot.yml` — weekly Dependabot updates for `github-actions` and `gomod` (latter also bumps the `toolchain` directive in `go.mod`). OTel and AWS/Azure SDKs grouped into single PRs to avoid version-skew between sibling modules.
- `.github/workflows/vuln-scan.yml` — weekly scheduled `govulncheck` run on master. Pulls the toolchain version from `go.mod` via `go-version-file`. On finding, opens (or comments on) a GitHub issue tagged `security`, so newly-disclosed CVEs become visible even between PRs.

## [0.7.0] - 2026-06-13

### Fixed
- Prometheus `path` label no longer explodes per package ID (issue #14). The HTTP metrics middleware now uses the matched gorilla/mux route template ("/api/v1/packages/{packageId}") as the `path` label, so 100 different package IDs collapse to one label combination instead of 100. Test contract in `internal/adapters/http/metrics_test.go`.
- HTTP metrics middleware is now actually wired into the router. Previously the `Collector.Middleware` method existed but was never installed, so `ortus_http_requests_total` and `ortus_http_request_duration_seconds` were never emitted by real requests.

### Added
- OTLP push export for metrics. The MeterProvider now bundles a Prometheus reader (kept for the existing `/metrics` scrape) with an optional OTLP `PeriodicReader`. Configure via `metrics.otlp.{enabled,endpoint,transport,insecure,headers,interval}` or `ORTUS_METRICS_OTLP_*` env vars. Endpoint falls back to `tracing.endpoint` when unset so a single collector can serve both signals.

### Changed
- **Breaking (internal)**: removed the `output.MetricsCollector` port and its `NoOpMetrics` no-op. Services now receive `metric.Meter` directly (OTel-native API). Each service defines its own instruments — there is no central metric registry. Call sites that wrote `s.metrics.IncQueryCount(id, ok)` now write `s.queryCount.Add(ctx, 1, metric.WithAttributes(...))`. Tests use `noop.NewMeterProvider().Meter("test")` from `go.opentelemetry.io/otel/metric/noop`.
- HTTP request instruments (`ortus.http.requests`, `ortus.http.request.duration`) moved from the `metrics` package to `internal/adapters/http/` where the label values originate, keeping `metrics` mux-free.
- `metrics` package is now a thin lifecycle wrapper around the OTel `MeterProvider`. The Prometheus-shaped public methods (`IncQueryCount`, `ObserveQueryDuration`, `Middleware`, etc.) are gone.

### Build
- Added dependencies: `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`, `…/otlpmetricgrpc`.

## [0.6.0] - 2026-06-13

### Fixed (review iteration)
- RingBuffer now finalizes a trace only when its root has ended AND every span has ended, instead of evicting as soon as the root ends (OTel permits children to outlive their parent)
- RingBuffer `isRoot` detection now also treats spans with remote parents as local roots, so distributed-trace continuations land in the buffer correctly
- `tracer.sprint()` no longer silently drops unsupported attribute types — handles `error`, `fmt.Stringer`, and falls back to `fmt.Sprintf("%v", v)`
- `App.Startup` span status now reflects real outcome (Error if any startup step failed); previously always set to OK after `RecordError`
- `TraceFilter.Status` docstring corrected to match the OTel `codes.Code` casing ("Ok", "Error", "Unset")
- `--tracing-endpoint` help text and `TracingConfig.Endpoint` comment corrected to describe host:port (no URL parsing happens in the exporter setup)

### Added
- OpenTelemetry tracing across HTTP, application services, repository, storage, watcher, and sync — every named operation produces a span, enforced by a coverage test
- In-memory trace-grouped ring buffer with separate FIFO pools for success and error traces (default 256 each); error traces never get evicted by routine successes
- `ListActive()` snapshot of in-flight spans so hanging operations remain visible (essential for diagnosing things that never finish)
- `X-Trace-Id` response header on every HTTP response, including 4xx/5xx and panics
- slog `SpanContextHandler` auto-injects `trace_id` and `span_id` into any `logger.*Context` call that carries a span
- Panic recovery in background goroutines (watcher event handler, sync scheduler, metrics server) with panic recorded on the active span and full stack via `RecordError`
- Outbound HTTP instrumentation: `otelhttp` transport for the HTTP storage adapter, `otelaws` middleware for S3, `otelhttp` transport for Azure Blob — retries and per-attempt latency now visible as child spans
- OTLP exporter error handler routes failures through slog at Warn level and exposes a counter via `telemetry.OTelErrorCount()`
- New CLI flags: `--tracing`, `--tracing-endpoint`, `--tracing-transport`, `--tracing-sample-ratio` and matching `ORTUS_TRACING_*` env vars + `tracing:` config block
- `doc/TRACING.md` reference documenting the configuration surface, the span catalogue, and the MCP integration contract

### Changed
- Prometheus metrics now produced via the OTel meter API and exported in Prometheus format — metric names and labels unchanged, scrape configs keep working
- Application/domain code depends on a hexagonal `output.Tracer` port rather than OTel directly; the OTel adapter lives in `internal/adapters/telemetry`

### Build
- Bump CI to Go 1.25.8 and golangci-lint to v2.12.2 to match the toolchain required by the new OpenTelemetry dependencies

## [0.5.1] - 2025-12-27

### Fixed
- Packages now show `ready=true` on server restart when R-tree indexes already exist

## [0.5.0] - 2025-12-26

### Fixed
- GeoPackage spatial index creation now works without `geometry_columns` table
- SpatiaLite's `CreateSpatialIndex()` replaced with direct R-tree virtual table creation
- Query performance improved from ~6 seconds to ~8-150ms for large GeoPackages

### Changed
- Database opened in read-write mode to allow R-tree index creation
- R-tree indexes are persisted in GeoPackage files for faster subsequent starts
- Point queries now use R-tree pre-filter + ST_Contains for precise geometry matching

## [0.4.1] - 2025-12-25

### Added
- `--disable-frontend` CLI flag to disable the web frontend at `/`
- `server.frontend_enabled` config option (default: `true`)
- Environment variable `ORTUS_SERVER_FRONTEND_ENABLED` support

## [0.4.0] - 2025-12-25

### Added
- Embedded web frontend at root path (`/`) for interactive coordinate queries
- Support for major European coordinate systems: WGS84, Web Mercator, ETRS89/UTM zones 32N & 33N, DHDN/Gauß-Krüger zones 2 & 3
- Mobile-first responsive design with dynamic labels adapting to selected coordinate system
- Geolocation button to use current device position
- Expandable result cards with feature properties, geometry preview, and license information

## [0.3.1] - 2025-12-23

### Fixed
- `derivePackageID` edge cases: properly handles empty paths and files named only with extension (e.g., ".gpkg")
- Race condition in package removal: captures both ID and path in single lock acquisition
- Sync service rate limiting: initializes `lastAPISync` to allow immediate first API call
- Concurrent sync prevention: adds mutex to prevent scheduled and API-triggered syncs from running simultaneously
- Watcher event precedence: create events now override pending delete events (handles quick delete+recreate)

### Changed
- Refactored watcher `eventLoop` into smaller functions to reduce cognitive complexity

### Added
- Comprehensive tests for `derivePackageID` edge cases
- Tests for watcher helper functions (`fsnotifyOpToOperation`, `isGeoPackageFile`, `Operation.String`)

## [0.3.0] - 2025-12-22

### Added
- Automatic removal of packages deleted from remote storage during sync
- `packages_removed` field in sync API response
- Proper file deletion detection in local file watcher (fixed fsnotify operation handling)

### Changed
- `Sync()` now returns `SyncStats` with both `Added` and `Removed` counts
- File watcher now correctly uses fsnotify operation types instead of file existence check

### Fixed
- File watcher `determineOperation` now correctly detects file deletions using fsnotify events
- Local cache files are now deleted when packages are removed from remote storage

## [0.2.0] - 2025-12-22

### Added
- Remote Storage Sync: Periodic synchronization with S3/Azure/HTTP to detect and load new GeoPackages
- Sync API endpoint `POST /api/v1/sync` with rate limiting (2 requests/minute, 30s cooldown)
- `SyncConfig` for configurable sync intervals (`ORTUS_SYNC_ENABLED`, `ORTUS_SYNC_INTERVAL`)
- Storage type constants (`StorageTypeLocal`, `StorageTypeS3`, `StorageTypeAzure`, `StorageTypeHTTP`)
- ADR-0011 documenting Remote Storage Sync design decisions
- Docker CI/CD pipeline with multi-architecture support (amd64, arm64)
- Automated Docker image builds and security scanning
- Claude Code hooks for local Docker validation (hadolint, trivy)
- VERSION file for centralized version management
- CHANGELOG.md for tracking changes

### Changed
- HTTP server now accepts optional `SyncService` dependency
- App lifecycle manages SyncService start/stop

## [0.1.0] - 2024-12-21

### Added
- Initial release of Ortus GeoPackage query server
- REST API with point queries (`/api/v1/query`)
- Multiple GeoPackage support with hot-reload
- Automatic coordinate transformation (SRID support)
- Object storage integration (AWS S3, Azure Blob, HTTP, Local)
- TLS/HTTPS with Let's Encrypt via CertMagic
- Prometheus metrics endpoint
- Health checks (`/health`, `/health/live`, `/health/ready`)
- OpenAPI 3.0 specification and Swagger UI
- Multi-platform Docker support (Alpine and Ubuntu variants)
- Configurable geometry output in query results
- Comprehensive unit and integration tests

### Security
- Non-root user in Docker containers
- Read-only GeoPackage access
- CORS configuration support

[Unreleased]: https://github.com/jobrunner/ortus/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/jobrunner/ortus/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/jobrunner/ortus/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/jobrunner/ortus/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/jobrunner/ortus/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/jobrunner/ortus/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/jobrunner/ortus/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jobrunner/ortus/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jobrunner/ortus/releases/tag/v0.1.0
