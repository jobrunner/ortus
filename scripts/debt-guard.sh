#!/usr/bin/env bash
#
# debt-guard.sh — fast, test-free technical-debt ratchet checks.
#
#   1. Suppression budget: total #nosec + //nolint may not exceed the baseline
#      in .debt-budget (ratchet down only — new suppressions force a justified
#      bump or, better, a fix).
#   2. No new debt markers: TODO/FIXME/HACK/XXX comment markers are kept at
#      zero (the codebase has none; this keeps it that way).
#   3. Storage-extension guard: no storage backend may hardcode a source-file
#      extension — all must route through domain.IsSupportedSourceFile so the
#      backends can't drift (this is how raster bundles were once silently
#      dropped on S3/Azure).
#
# Usage: scripts/debt-guard.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
BUDGET_FILE=".debt-budget"
status=0

[ -f "$BUDGET_FILE" ] || {
  echo "debt-guard: baseline file not found: $BUDGET_FILE (run from repo root)" >&2
  exit 2
}

# The counters below use `git grep` (tracked files only), so outside a git
# worktree (e.g. a source export without .git) every count would silently be 0
# and the ratchet would pass vacuously — fail fast instead.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "debt-guard: not inside a git worktree — git grep has nothing to scan" >&2
  exit 2
}

# count <pattern> — number of matching directive lines in first-party *.go.
# Uses `git grep` so only TRACKED files count: a recursive filesystem grep also
# scans nested worktrees (.claude/worktrees/…) and untracked scratch files,
# which double-counts suppressions and fails the ratchet for code that isn't
# part of the tree. Tolerant of zero matches: git grep exits 1 with no hits,
# which would abort under `set -euo pipefail`, so swallow it and still count.
count() {
  { git grep -n "$1" -- '*.go' || true; } | { grep -c . || true; } | tr -d ' '
}

# 1. Suppression budget — count the actual directive forms (//nolint, #nosec),
# not the bare substring, so prose can't inflate the number.
nosec=$(count '#nosec')
nolint=$(count '//nolint')
total=$((nosec + nolint))
baseline=$(grep -vE '^\s*#|^\s*$' "$BUDGET_FILE" | head -1 | tr -d ' ')

echo "suppressions: #nosec=$nosec //nolint=$nolint total=$total (baseline $baseline)"
if [ "$total" -gt "$baseline" ]; then
  echo "  ▼ debt-guard: FAIL — suppressions grew past the baseline." >&2
  echo "    Remove a suppression (preferred), or justify a bump in .debt-budget in the PR." >&2
  status=1
elif [ "$total" -lt "$baseline" ]; then
  echo "  ✓ suppressions dropped — lower the baseline in .debt-budget to $total to lock it in."
fi

# 2. No new debt markers -----------------------------------------------------
# Leading-marker form only ("// TODO", "// FIXME:") so prose like "...a bug,"
# doesn't false-positive.
markers=$(git grep -nE '//[[:space:]]*(TODO|FIXME|HACK|XXX)([[:space:]:(]|$)' -- '*.go' || true)
if [ -n "$markers" ]; then
  echo "  ▼ debt-guard: FAIL — debt markers found (keep them out of the tree; track in docs/explanation/technical-debt.md):" >&2
  echo "$markers" | sed 's/^/      /' >&2
  status=1
else
  echo "debt markers: none"
fi

# 3. Storage-extension guard -------------------------------------------------
# Match both quote styles Go allows for a literal extension: interpreted
# strings ("...") and raw strings (`...`), so the guard can't be sidestepped
# by switching quotes.
hard=$(git grep -nE '["`]\.(gpkg|zip)["`]' -- 'internal/adapters/storage/*.go' \
  | grep -v '_test.go' || true)
if [ -n "$hard" ]; then
  echo "  ▼ debt-guard: FAIL — storage backend hardcodes a source extension; use domain.IsSupportedSourceFile:" >&2
  echo "$hard" | sed 's/^/      /' >&2
  status=1
else
  echo "storage filters: all via domain.IsSupportedSourceFile"
fi

[ "$status" -eq 0 ] && echo "debt-guard: OK"
exit "$status"
