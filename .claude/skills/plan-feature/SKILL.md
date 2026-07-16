---
name: plan-feature
description: >-
  Plan a new ortus feature from scratch and review that plan against the real
  code BEFORE any implementation. Use at the very start of a feature — especially
  inside a per-ticket dev container (`make dev NAME=<slug>` drops you here). Runs
  a short interview, writes a plan to .claude/plans/, then adversarially checks
  the plan against the actual architecture (hexagonal ports/adapters, depguard/
  gomodguard import boundaries, config.go env keys, the HTTP/MCP surface, OpenAPI
  + docs) so the plan is grounded in reality, not assumptions. Finish by getting
  explicit go/no-go before writing code.
---

# Feature planning + plan-vs-code review

The goal: turn a rough feature idea into a **grounded, reviewed plan** before a
line of code is written. A plan that ignores the real architecture wastes a whole
implementation cycle. This skill is deliberately two-phase: **plan**, then
**review the plan against the code**.

## When to use
- Starting any non-trivial feature. `make dev NAME=<slug>` launches Claude in the
  ticket container and invokes this skill automatically.
- The environment already has: the ortus MCP server (live instance), context7
  (library docs), gopls (Go LSP), superpowers, and the repo's skills — use them.

## Phase 1 — Interview (short, high-signal)
Ask only what you can't infer from the repo. Cover:
1. **Outcome**: what can a user/operator do after this ships that they can't now?
2. **Surface**: HTTP endpoint(s)? MCP tool(s)? config keys? frontend? data package?
3. **Inputs/outputs**: request params, response fields (+ types, nullability,
   conditional fields), errors.
4. **Data & sources**: which source type / adapter (gazetteer, raster/COG, gpkg)?
   new data package or existing?
5. **Constraints**: performance targets, backward-compat, package-size limits,
   startup-time budget.

Don't over-interview — one focused round, then proceed and confirm as you go.

## Phase 2 — Draft the plan
Write to `.claude/plans/<slug>.md`. Structure: Context · Decisions (confirmed) ·
Architecture/topology · Files to create/change · Verification (end-to-end) ·
Risks. Prefer concrete file paths and function names over prose.

## Phase 3 — Review the plan against reality (the important part)
**Never guess — open the code and quote it.** For each planned change, verify it
fits the actual system. Dispatch `Explore` for breadth and
`software-architecture-planner` for the design critique; cross-check at minimum:

- **Hexagonal boundaries**: does the change respect ports/adapters? Domain logic
  in `internal/domain` / core, IO in `internal/adapters/*`. New dependency
  direction must point inward.
- **Import boundaries (CI-enforced)**: depguard + gomodguard will fail the build
  if a layer imports something it shouldn't. Confirm the plan's imports are
  allowed. See the architecture harness — respect it or CI fails.
- **Config**: new settings go through `internal/config/config.go` (struct tag +
  `viper.SetDefault`, env prefix `ORTUS_`, `.`→`_`). List the exact new keys.
- **HTTP surface**: routes in `internal/adapters/http/server.go`; response shapes
  in `handlers.go`/`gazetteer.go`. Any new/changed endpoint or field means the
  OpenAPI spec + docs must change too — flag it (run the `doc-drift-check` skill
  before the PR).
- **MCP surface**: tools in `internal/adapters/mcp/*` (and their registration
  conditions).
- **Existing patterns**: find the closest existing feature and mirror its shape
  (error envelope, telemetry, tests).

Produce a **realism report**: for each planned item → `fits` / `conflicts (why +
where)` / `needs decision`. Fold fixes back into the plan.

## Phase 4 — Go/No-go
Summarize the reviewed plan and the open decisions. Get an explicit **go** before
implementing. When approved, implement against the plan; validate with
`make verify`, and run `doc-drift-check` before opening a PR. For performance-
sensitive work, use the `perf-test` skill (`make dev-perf`) to measure.
