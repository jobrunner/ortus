# Contributing to ortus

Thanks for contributing! This page is the map to the project's guardrails — the
checks that keep ortus correct, fast, secure, and low-debt over time. Most of
them run automatically; this tells you how to work with them.

## Setup

See [`doc/DEVELOPMENT.md`](doc/DEVELOPMENT.md) for the toolchain (Go, CGO,
SpatiaLite). Then install the git hook once:

```sh
make hooks      # pre-commit: gofmt + build + the fast debt ratchet
```

## The one command before you push

```sh
make verify
```

This is the canonical green check — `fmt-check + vet + lint + test + build +
arch + debt-guard`. If it's green, CI almost certainly will be too. Run it
before every push.

## Commits & PRs

- **Conventional Commits** are enforced (`commitlint`): `feat:`, `fix:`,
  `chore:`, `test:`, `ci:`, `docs:`, … Releases & the changelog are derived from
  them by release-please — don't hand-edit `VERSION`/`CHANGELOG.md`.
- PRs merge as **merge commits** (squash is disabled). **All review threads
  must be resolved** before merge; Copilot re-reviews on each push.
- Keep PRs to one concern.

## The harness (what CI enforces)

Full details and the rationale for each gate live in
[`doc/tech-debt.md`](doc/tech-debt.md). In short, these are **ratchets** — they
fix the current quality level in place so it can only improve:

| You'll hit it if you… | Gate | Fix |
| --------------------- | ---- | --- |
| add an unjustified `//nolint` | `nolintlint` | name the linter + give a reason |
| add a `#nosec`/`//nolint` beyond the budget | suppression budget (`.debt-budget`) | remove one, or justify a bump in the PR |
| leave a `// TODO`/`FIXME`/`HACK` | debt-marker check | track it in `doc/tech-debt.md` instead |
| import across hexagonal layers | depguard (`make arch`) | respect domain→ports→adapters boundaries |
| drop a package below its coverage floor | coverage ratchet (`.coverage-floors`) | add tests (floors raise-only) |
| leak a goroutine in tests | `goleak` | `t.Cleanup` / close what you start |
| commit a secret | `gitleaks` | don't; allowlist genuine placeholders in `.gitleaks.toml` |
| add a non-permissive dependency | `go-licenses` (`make licenses`) | swap it, or extend the allowlist if acceptable |
| drift `config.yaml.example` from the struct | config-drift test | remove the stale key or wire it in |
| hardcode a source extension in a storage backend | storage-filter guard | use `domain.IsSupportedSourceFile` |

The ratchet runs at three escalating points: the Claude edit-hook (advisory),
`git pre-commit` (blocking), and CI (authoritative).

## Deeper / on-demand tooling

| Command | What |
| ------- | ---- |
| `make fuzz` | fuzz the parse boundaries (seeds also run in CI; deep fuzz runs weekly) |
| `make bench` | hot-path micro-benchmarks (CI posts a benchstat PR-vs-base delta) |
| `make mutation` | mutation testing (gremlins) — runs weekly in CI; test-effectiveness signal |
| `make licenses` | dependency license compliance |
| `make debt` | the full debt ratchet (suppressions + markers + coverage floors) |

See [`doc/load-test.md`](doc/load-test.md) for the local load-test + Grafana
observability stack, and [`doc/ARCHITECTURE.md`](doc/ARCHITECTURE.md) for the
hexagonal design the import boundaries enforce.

## Releases & supply chain

Released container images are cosign-signed (keyless) and carry SLSA provenance
and SPDX SBOM attestations — release notes include the `cosign verify` /
`cosign download sbom` commands.
