#!/usr/bin/env bash
# Hook: Check that VERSION and CHANGELOG.md are updated for significant changes
# This hook runs BEFORE git commit to ensure version tracking is maintained
#
# Trigger: PreToolUse on Bash commands containing "git commit"
#
# Environment variables:
#   CLAUDE_PROJECT_DIR - Project root directory
#   SKIP_VERSION_CHECK - Set to "1" to skip this check (for documentation-only changes)

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Read hook input from stdin (JSON format)
INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)

# Only run for Bash tool
if [[ "$TOOL_NAME" != "Bash" ]]; then
    exit 0
fi

# Only run for git commit commands
if [[ ! "$COMMAND" =~ git[[:space:]].*commit ]]; then
    exit 0
fi

# Check if we're in a git repository
if ! git -C "$PROJECT_DIR" rev-parse --git-dir > /dev/null 2>&1; then
    exit 0
fi

# Get staged files
STAGED_FILES=$(git -C "$PROJECT_DIR" diff --cached --name-only 2>/dev/null || echo "")

if [[ -z "$STAGED_FILES" ]]; then
    # No staged files, nothing to check
    exit 0
fi

# Check for significant code changes (Go files in internal/, cmd/, or root)
SIGNIFICANT_CHANGES=false
while IFS= read -r file; do
    if [[ "$file" =~ ^(internal/|cmd/|pkg/).*\.go$ ]] || [[ "$file" =~ ^[^/]+\.go$ ]]; then
        SIGNIFICANT_CHANGES=true
        break
    fi
done <<< "$STAGED_FILES"

# If no significant changes, skip check
if [[ "$SIGNIFICANT_CHANGES" != "true" ]]; then
    exit 0
fi

# Allow skipping for documentation-only PRs
if [[ "${SKIP_VERSION_CHECK:-}" == "1" ]]; then
    echo "⚠️  VERSION/CHANGELOG check skipped (SKIP_VERSION_CHECK=1)" >&2
    exit 0
fi

# Check if VERSION or CHANGELOG.md are in staged files
VERSION_STAGED=false
CHANGELOG_STAGED=false

while IFS= read -r file; do
    if [[ "$file" == "VERSION" ]]; then
        VERSION_STAGED=true
    fi
    if [[ "$file" == "CHANGELOG.md" ]]; then
        CHANGELOG_STAGED=true
    fi
done <<< "$STAGED_FILES"

# Report findings
MISSING_FILES=()

if [[ "$VERSION_STAGED" != "true" ]]; then
    MISSING_FILES+=("VERSION")
fi

if [[ "$CHANGELOG_STAGED" != "true" ]]; then
    MISSING_FILES+=("CHANGELOG.md")
fi

if [[ ${#MISSING_FILES[@]} -gt 0 ]]; then
    echo "" >&2
    echo "⚠️  Significant code changes detected but version tracking files not updated!" >&2
    echo "" >&2
    echo "   Missing updates: ${MISSING_FILES[*]}" >&2
    echo "" >&2
    echo "   For feature additions or changes, please:" >&2
    echo "   1. Bump VERSION (current: $(cat "$PROJECT_DIR/VERSION" 2>/dev/null || echo 'N/A'))" >&2
    echo "   2. Add entry to CHANGELOG.md under [Unreleased] or new version" >&2
    echo "" >&2
    echo "   To skip this check for documentation-only changes:" >&2
    echo "   export SKIP_VERSION_CHECK=1" >&2
    echo "" >&2
    # Return non-zero to BLOCK the commit
    exit 1
fi

echo "✅ VERSION and CHANGELOG.md are staged for commit" >&2
exit 0
