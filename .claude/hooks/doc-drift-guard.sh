#!/usr/bin/env bash
# Hook: run the documentation-drift gate before a PR is opened.
#
# Trigger: PreToolUse on Bash commands that create a PR (`gh pr create`).
# On drift it exits 2 (blocking) so Claude fixes it — via the doc-drift-check
# skill — before the PR exists. Everything else passes through instantly.
#
# It runs the FULL mechanical gate (OpenAPI copies in sync, spec parses, routes↔
# spec contract test, oasdiff, mkdocs --strict). The SEMANTIC review (is the prose
# accurate?) is the doc-drift-check skill's job — this only catches mechanical drift.
#
# Env: SKIP_DOC_DRIFT=1 bypasses the gate (escape hatch).

set -uo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)

# Only for Bash, and only for PR creation.
[ "$TOOL_NAME" = "Bash" ] || exit 0
[[ "$COMMAND" =~ gh[[:space:]]+pr[[:space:]]+create ]] || exit 0

if [ "${SKIP_DOC_DRIFT:-}" = "1" ]; then
    echo "⚠️  doc-drift gate skipped (SKIP_DOC_DRIFT=1)" >&2
    exit 0
fi

HARNESS="$PROJECT_DIR/.claude/skills/doc-drift-check/scripts/check-doc-drift.sh"
if [ ! -x "$HARNESS" ]; then
    # Harness missing → don't block PR creation, just note it.
    echo "ℹ️  doc-drift harness not found ($HARNESS) — skipping pre-PR drift gate." >&2
    exit 0
fi

if OUT=$(CLAUDE_PROJECT_DIR="$PROJECT_DIR" bash "$HARNESS" 2>&1); then
    echo "✅ doc-drift gate passed — docs/spec match the code." >&2
    exit 0
fi

# Drift detected — block the PR and tell Claude how to fix it.
{
    echo ""
    echo "⛔ Documentation drift detected — not opening the PR yet."
    echo ""
    echo "$OUT"
    echo ""
    echo "Fix it with the doc-drift-check skill (compare code ↔ OpenAPI ↔ docs and"
    echo "pull the docs/spec back in line), then retry. The api/ copy can be"
    echo "auto-resynced with: bash $HARNESS --fix"
    echo "Escape hatch (not recommended): SKIP_DOC_DRIFT=1."
} >&2
exit 2
