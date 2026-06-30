#!/bin/bash
# Claude Code Hook: build the Diátaxis docs strictly after editing a docs page
# or the mkdocs config. Advisory + non-blocking — surfaces broken links / nav
# issues immediately. The hard gate is the CI "Docs" job and `make docs`.
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
case "$FILE_PATH" in
  */docs/*.md|*/mkdocs.yml|docs/*.md|mkdocs.yml) ;;
  *) exit 0 ;;
esac
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v uvx >/dev/null 2>&1 || exit 0  # no uv -> skip silently (CI is the gate)
if ! out=$(uvx --with mkdocs-material mkdocs build --strict 2>&1); then
  echo "⚠️  docs (advisory): 'mkdocs build --strict' would FAIL — fix before commit/CI:"
  echo "$out" | grep -iE 'warning|error|aborted' | head -20
fi
exit 0
