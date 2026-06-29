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
| D1 | storage (s3/azure/local) | Backends return **bare errors** (no `%w`, no `domain.StorageError`), so an object-store failure reaches `handleQueryError` as a generic 500 instead of the 503 path. Only geopackage/http wrap. | **MED** | Wrap in `domain.StorageError`; then the 503 mapping works uniformly. Behaviour change near sync — sequence deliberately. |
| D2 | storage/http.go `Exists` | A transport error (DNS/timeout/TLS) is reported as `false, nil` — a transient outage looks like a deleted object, so sync may drop a still-present source. | **MED** | Distinguish HTTP 404 from transport errors. S3/Azure `Exists` is more defensible (typed not-found). |
| D3 | geopackage `NewRepositoryTransformer` | Swallows `sql.Open` and `InitSpatialMetaDataFull` errors and can return a transformer that fails every later `ST_Transform`. | **MED** | Return `(nil, err)`; let the caller log/propagate. |
| D4 | mcp diagnostic tools | `addListTraces`/`addGetTrace`/`addListActiveSpans`/`addTracingStats` are ~7–20% covered — the filter/limit/nil-telemetry branches are untested logic, not wiring. The 48% package figure overstates safety. | **MED** | Test by **calling** the tools (existing tests only assert they're registered). |
| D5 | http `recoveryMiddleware` | The panic-recovery body is never exercised (33%). If it breaks, a handler panic drops the connection instead of returning 500 — exactly when the net is needed. | **MED** | One test that panics in a handler and asserts 500. |
| D6 | application/query.go | `defaultSRID` field is assigned from config and asserted in a test but **never read** in production (SRID defaulting happens in the HTTP/MCP layers). Inert plumbing that looks wired. | **LOW** | Either use it or remove field + config key. |
| D7 | storage/local.go (`#nosec G304`) | The `key`→`basePath` join has no `filepath.Clean`/prefix check (unlike raster's `safeJoin`); the comment overstates safety. Not exploitable today (keys come from a trusted listing). | **LOW** | Reuse a `safeJoin`-style guard. |
| D8 | tracing strategy | Three strategies for the same concern: geopackage/raster instrument spans inline, storage uses a `TracedStorage` decorator, mcp has none. Divergence risk. | **LOW** | Consider a tracing decorator for geopackage/raster mirroring `TracedStorage`; mcp entry points could record spans. |

### Already fixed in the harness PR (2026-06)

- Deleted dead code: geopackage `Transformer`/`TransformDB`, mcp `stringifyJSON`.
- **Correctness bug**: S3/Azure `List`/blob filter hardcoded `.gpkg`, silently
  dropping raster `.zip` bundles on those backends; now all four backends use
  `domain.IsSupportedSourceFile`, guarded by `debt-guard.sh`.
- Added missing `#nosec` reasons; enabled `nolintlint`, which immediately
  surfaced **5 redundant `//nolint` directives** (suppressing findings already
  covered by per-path exclusions / test-file relaxations) — removed. Suppression
  budget dropped 31 → 26.
