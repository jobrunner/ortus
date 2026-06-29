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

go_files() { grep -rIl '' --include='*.go' . 2>/dev/null | grep -v '/\.go/mod/'; }

# 1. Suppression budget ------------------------------------------------------
nosec=$(grep -rn '#nosec' --include='*.go' . | grep -v '/\.go/mod/' | wc -l | tr -d ' ')
nolint=$(grep -rn 'nolint' --include='*.go' . | grep -v '/\.go/mod/' | wc -l | tr -d ' ')
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
markers=$(grep -rnE '//[[:space:]]*(TODO|FIXME|HACK|XXX)([[:space:]:(]|$)' --include='*.go' . \
  | grep -v '/\.go/mod/' || true)
if [ -n "$markers" ]; then
  echo "  ▼ debt-guard: FAIL — debt markers found (keep them out of the tree; track in doc/tech-debt.md):" >&2
  echo "$markers" | sed 's/^/      /' >&2
  status=1
else
  echo "debt markers: none"
fi

# 3. Storage-extension guard -------------------------------------------------
hard=$(grep -rnE '"\.(gpkg|zip)"' --include='*.go' internal/adapters/storage/ \
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
