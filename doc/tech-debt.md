# Technical debt — policy, harness, and register

ortus keeps technical debt low by **ratcheting**, not by occasional cleanup
sprints: automated gates fix the current level in place so debt can only go
**down** over time. New code must clear the bar the existing code already
clears. This document is the policy, the description of each gate, and the
register of debt we knowingly carry.

## The harness

| Gate | Runs in | What it enforces | How to satisfy |
| ---- | ------- | ---------------- | -------------- |
| **Linters** (24 of them) | Lint job / `make lint` | errcheck, staticcheck, gosec, gocyclo≤16, gocognit≤20, dupl, revive, … | fix the finding |
| **`nolintlint`** | Lint job | every `//nolint` names the linter **and** carries a reason; no unused directives | write `//nolint:tool // why` |
| **Import boundaries** | Lint + `make arch` | depguard hexagonal rules, gomodguard blocklist | keep layers clean |
| **Suppression budget** | Architecture job / `make debt-guard` | total `#nosec` + `//nolint` ≤ `.debt-budget` (ratchet down) | remove one, or justify a bump in the PR |
| **Debt markers** | Architecture job / `make debt-guard` | zero `// TODO/FIXME/HACK/XXX` markers in `*.go` | track it here instead |
| **Storage-filter drift** | Architecture job / `make debt-guard` | no storage backend hardcodes a source extension | route through `domain.IsSupportedSourceFile` |
| **Coverage floors** | Test job / `make debt-coverage` | per-package statement coverage ≥ `.coverage-floors` (ratchet up) | add tests |
| **deadcode** | advisory `make debt-deadcode` | unreachable funcs (informational) | triage by hand — see below |

`make verify` runs everything except the coverage floors and the deadcode
advisory (it already runs the test suite; `make debt-coverage` adds a dedicated
coverage run). `make debt` runs both ratchet scripts together.

### Working with the ratchets

- **Coverage floors** (`.coverage-floors`): per-package, a few points below the
  current value so routine churn doesn't trip them, but a real regression does.
  They may only be **raised**. Packages omitted are exempt **on purpose**:
  composition root (`internal/app`), `cmd/ortus`, thin SDK wrappers
  (`metrics`, `tls`, S3/Azure I/O), and `ports` constructors — wiring whose
  unit-coverage has low value. Floors by category: core logic ≈75–100%,
  in-process adapters ≈65–78%, I/O adapters ≈49%.
- **Suppression budget** (`.debt-budget`): the single number is the total of
  `#nosec` + `//nolint`. When you remove suppressions, lower it to lock the
  gain in. New suppressions are allowed only with a reviewed bump.
- **deadcode is advisory, not a gate.** `golang.org/x/tools/cmd/deadcode`
  reports interface-dispatched methods and exported API used only by tests as
  "unreachable" — too many false positives for CI. Run it periodically and
  triage; the `unused` linter (in CI) is the blocking dead-code check.

### Two deliberate non-gates

- **gosec `G104` stays globally excluded.** `errcheck` (enabled, passing) is the
  real unchecked-error gate; G104 is largely redundant with it and noisy. The
  one class errcheck misses by default — errors discarded with `_ =` / in
  `defer` — is a code-review concern, tracked below, not a blanket gate.
- **No `godox`.** Its default keyword set flags the word "bug" in prose; the
  marker check in `debt-guard.sh` matches only leading `// TODO/FIXME/…` forms.

## Register of known debt

Found in the 2026-06 audit; carried knowingly. Priority = impact × likelihood.
Fix opportunistically and lower the relevant baseline when you do.

| # | Area | Debt | Priority | Note |
|---|------|------|----------|------|
| ~~D1~~ | storage (s3/azure/local) | ✅ **Fixed** — backend errors now normalized to `*domain.StorageError` via an `ErrorWrappingStorage` decorator, so storage failures map to 503 uniformly. | — | done |
| ~~D2~~ | storage/http.go `Exists` | ✅ **Fixed** — distinguishes 404 (→ `false,nil`) from transport errors and unexpected statuses (→ error). | — | done |
| ~~D3~~ | geopackage `NewRepositoryTransformer` | ✅ **Fixed** — returns `(nil, err)` on open / metadata-init failure; the composition root propagates it. | — | done |
| ~~D4~~ | mcp diagnostic tools | ✅ **Fixed** — call-level tests for `list_traces`/`get_trace`/`list_active_spans`/`tracing_stats` (filter/limit/since-parse/nil-telemetry branches). Tools 7–20% → 90–100%; package 49.6% → 72.9%. | — | done |
| ~~D5~~ | http `recoveryMiddleware` | ✅ **Fixed** — test panics in a handler and asserts 500 (plus a no-panic pass-through). | — | done |
| ~~D6~~ | application/query.go | ✅ **Fixed** — removed the inert `defaultSRID` end-to-end (struct field, `QueryServiceConfig`, `query.default_srid` config key + viper default, test). SRID defaulting stays at the HTTP/MCP edges. Inert key → no behaviour change. | — | done |
| ~~D7~~ | storage/local.go | ✅ **Fixed** — added a package-local `safeJoin` (Clean + abs/`..`/prefix checks) used by `Download`/`GetReader`; `#nosec G304` justifications now reference it. | — | done |
| ~~D8~~ | tracing strategy | ✅ **Resolved** — MCP now records an entry span per received method (`mcp.<method>`, `mcp.tool.name`) via receiving middleware, closing the only adapter with *zero* spans. The remaining "drift" is a **deliberate, documented** policy, not accidental — see note. | — | done |

### Tracing strategy — deliberate, not drift

The three adapters instrument differently **on purpose**, matched to span shape:

- **storage** — a thin `TracedStorage` decorator. Its spans are attribute-light
  (operation, key, object count), all knowable at the boundary, so a decorator
  captures everything without touching the backends.
- **geopackage / raster** — inline instrumentation. Their spans carry rich,
  method-*internal* attributes (`ortus.rtree.used`, `db.statement`, feature/layer
  counts, `has_index`) that only exist mid-method. A generic decorator could not
  capture these — it would *regress* trace quality and the coverage test in
  `telemetry/coverage_test.go`. So inline is the right altitude here.
- **mcp** — a receiving-middleware entry span (`mcp.<method>`), since the tool
  surface has no rich per-method attributes to set; the value is tying the call
  to its downstream query/health spans in one trace.

### Fixed in the tracing PR (2026-06)

- **D8** — MCP entry-point tracing via `AddReceivingMiddleware`; documented the
  deliberate per-adapter tracing policy above.

### Fixed in the cleanup PR (2026-06)

- **D6** — removed the inert `defaultSRID` plumbing (config key `query.default_srid`,
  `QueryServiceConfig.DefaultSRID`, the struct field, and the test assertion).
- **D7** — `storage/local.go` now joins keys via a `safeJoin` traversal guard.

### Fixed in the coverage PR (2026-06)

- **D4** — call-level tests for the MCP diagnostic tools (mcp floor raised 48 → 70).
- **D5** — `recoveryMiddleware` panic→500 test (http floor raised 70 → 71).

### Fixed in the error-handling PR (2026-06)

- **D1** — `storage.ErrorWrappingStorage` decorator wraps all backend errors as
  `*domain.StorageError` (applied in the composition root, innermost decorator).
- **D2** — `HTTPStorage.Exists` now surfaces transport errors instead of
  reporting them as "not found".
- **D3** — `NewRepositoryTransformer` returns an error instead of a `nil`/broken
  transformer.

### Already fixed in the harness PR (2026-06)

- Deleted dead code: geopackage `Transformer`/`TransformDB`, mcp `stringifyJSON`.
- **Correctness bug**: S3/Azure `List`/blob filter hardcoded `.gpkg`, silently
  dropping raster `.zip` bundles on those backends; now all four backends use
  `domain.IsSupportedSourceFile`, guarded by `debt-guard.sh`.
- Added missing `#nosec` reasons; enabled `nolintlint`, which immediately
  surfaced **5 redundant `//nolint` directives** (suppressing findings already
  covered by per-path exclusions / test-file relaxations) — removed. Suppression
  budget dropped 31 → 26.
