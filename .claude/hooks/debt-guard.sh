#!/bin/bash
# Claude Code Hook: surface tech-debt RATCHET violations right after an edit,
# before they reach a commit or CI. Runs scripts/debt-guard.sh (suppression
# budget, debt markers, storage-filter drift). Advisory + non-blocking: it only
# speaks up when the ratchet would fail, and never blocks the tool call — the
# hard gates are the git pre-commit hook and CI.
#
# (Coverage floors are NOT checked here — that needs a test run, too slow for a
# per-edit hook; it stays in CI / `make verify`.)

# Read (and ignore) the hook JSON on stdin so we don't block the pipe.
cat >/dev/null 2>&1 || true

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
[ -x ./scripts/debt-guard.sh ] || exit 0

if ! out=$(./scripts/debt-guard.sh 2>&1); then
	echo "⚠️  debt-guard (advisory): the tech-debt ratchet would FAIL — fix before commit/CI:"
	echo "$out"
fi
exit 0
