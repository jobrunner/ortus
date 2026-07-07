---
name: doc-drift-check
description: >-
  Documentation-drift harness. Compares the running code (the reality) against
  the OpenAPI spec and the prose docs, then pulls the docs/spec back in line so
  drift is zero. Run this BEFORE opening any PR, and whenever HTTP routes,
  response shapes, config keys, CLI flags, env vars, MCP tools, or the frontend
  change. Don't guess — read the code and compare the ist-stände; every claim
  must be backed by a specific handler/schema/route. Finish by running the
  bundled scripts/check-doc-drift.sh gate (must be green).
---

# Documentation-drift harness (code ↔ OpenAPI ↔ docs)

Docs rot silently: an endpoint gains a field, a default flips, a feature ships —
and the spec/prose still describe yesterday. This skill makes the **code the
single source of truth** and drives the OpenAPI spec and the docs back to zero
drift. It is both a **playbook** (the semantic comparison, which needs judgment)
and a **gate** (`scripts/check-doc-drift.sh`, the mechanical part that fails CI/PRs).

**Run it before every PR.** A PreToolUse hook (`.claude/hooks/doc-drift-guard.sh`)
also runs the mechanical gate on `gh pr create` and blocks the PR if it drifts.

## The three ist-stände

1. **Reality (authoritative):** the Go code.
   - Routes: `internal/adapters/http/server.go` (every `HandleFunc`/`Methods`).
   - Response shapes: `internal/adapters/http/handlers.go` (`formatQueryResponse`,
     `formatSource`, health/sync handlers, `writeError`), `gazetteer.go`
     (`gazetteerSections`, `formatLocality`, `formatFix`).
   - Config: `internal/config/config.go` (struct tags, `viper.SetDefault`, env
     prefix `ORTUS_`, `.`→`_`).
   - MCP tools: `internal/adapters/mcp/*.go` (which tools `AddTool`, and under
     what condition).
   - Frontend: `internal/adapters/http/frontend.go`.
2. **OpenAPI:** `internal/adapters/http/openapi.yaml` (the embedded, canonical
   spec served at `/openapi.json`) and `api/openapi/openapi.yaml` (a copy that
   MUST stay byte-identical).
3. **Docs:** `docs/reference/*.md`, `docs/how-to/*.md`, `docs/tutorials/**`, `README.md`.

## Procedure

**Step 0 — Never guess.** For every claim, open the code and quote it. If you
can't find the code that backs a documented behavior, the doc is wrong (or the
behavior was removed).

**Step 1 — Extract reality.** From the code, write down: the route table
(method + path), each endpoint's exact request params and response JSON keys
(+ types, nullability, and *conditional* fields like the gazetteer block only on
`/query` and only for WGS84), the config keys + defaults, the registered MCP
tools (and their registration conditions), and the error envelope.

**Step 2 — Diff OpenAPI, fix.**
- Every registered `/api/v1` route ⇔ a documented path (the `TestRoutesMatchOpenAPISpec`
  fitness function enforces this).
- Schemas match the real response keys/required/nullable. Watch shared schemas:
  a schema reused by two endpoints must not advertise a field only one returns
  (e.g. `/query` has the `gazetteer` block, `/query/{sourceId}` must not).
- Keep `api/openapi/openapi.yaml` **byte-identical** to the embedded spec
  (`cp internal/adapters/http/openapi.yaml api/openapi/openapi.yaml`).

**Step 3 — Diff docs, fix.** Check response-field lists, params + defaults,
config keys, rate limits, feature status ("upcoming" vs shipped), and examples
against reality. Fix the prose. Common rot: renamed fields, flipped defaults,
missing new fields, stale "planned/next iteration" wording, wrong counts.

**Step 4 — Run the gate.** `bash .claude/skills/doc-drift-check/scripts/check-doc-drift.sh`
must be green. Then `make verify` (includes the contract test) and `make docs`
(mkdocs --strict). oasdiff must report **0 errors** vs `origin/master`.

## Respect deliberate exclusions

Not every gap is drift — some omissions are intentional and encoded in tests.
Before "fixing" a missing item, check for a documented decision. Known ones:

- **`POST /api/v1/sync` is intentionally NOT in the OpenAPI spec** — it's
  operator-only, not part of the documented query contract. `TestRoutesMatchOpenAPISpec`
  enforces its *absence*. Document it in prose (`http-api.md`) but never in the spec.

When you find such a case, honor it and note it — don't override a maintainer's
decision to make a checker happy.

## What the harness checks (mechanical) vs. what you check (semantic)

`scripts/check-doc-drift.sh` deterministically fails on: the two OpenAPI copies
diverging, the spec not parsing / dangling `$ref`s, the routes↔spec contract
test, oasdiff breaking changes, and a broken `mkdocs --strict` build. Run it with
`--fast` for just the instant checks (used by the pre-PR hook) or `--fix` to
auto-resync the api/ copy.

It **cannot** judge whether the prose is *accurate* — that's this skill's job.
Green harness + faithful prose = zero drift.
