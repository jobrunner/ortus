#!/bin/bash
# Claude Code Hook: surface tech-debt RATCHET violations right after an edit,
# before they reach a commit or CI. Runs scripts/debt-guard.sh (suppression
# budget, debt markers, storage-filter drift). Advisory + non-blocking: it only
# speaks up when the ratchet would fail, and never blocks the tool call — the
# hard gates are the git pre-commit hook and CI.
#
# (Coverage floors are NOT checked here — that needs a test run, too slow for a
# per-edit hook; it stays in CI / `make verify`.)

# Only relevant to Go edits — debt-guard inspects first-party *.go (+ .debt-budget).
# Parse the edited path like format-and-lint.sh and skip non-Go edits.
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
if [[ ! "$FILE_PATH" =~ \.go$ ]]; then
	exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
[ -x ./scripts/debt-guard.sh ] || exit 0

if ! out=$(./scripts/debt-guard.sh 2>&1); then
	echo "⚠️  debt-guard (advisory): the tech-debt ratchet would FAIL — fix before commit/CI:"
	echo "$out"
fi
exit 0
