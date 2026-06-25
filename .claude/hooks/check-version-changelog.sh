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
    # Informational only — do NOT block. Versioning + CHANGELOG are owned by
    # release-please now (see .github/workflows/release-please.yml): you commit
    # with Conventional Commit messages, and release-please opens a release PR
    # that bumps VERSION and cuts the CHANGELOG. A manual bump per commit is no
    # longer expected, so this is just a reminder to write a good commit message.
    echo "" >&2
    echo "ℹ️  Significant code changes detected. VERSION/CHANGELOG are managed by" >&2
    echo "   release-please from your Conventional Commit messages — no manual" >&2
    echo "   bump needed. Just make sure the commit message is conventional" >&2
    echo "   (feat:/fix:/…) so the next release PR picks it up." >&2
    echo "" >&2
    exit 0
fi

echo "✅ VERSION and CHANGELOG.md are staged for commit" >&2
exit 0
