#!/usr/bin/env bash
# Hook: Validate Dockerfiles with hadolint
# Runs when Dockerfile* files are modified via Edit/Write

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Read hook input from stdin (JSON format)
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Exit silently if no file path
if [[ -z "$FILE_PATH" ]]; then
    exit 0
fi

# Only run for Dockerfile changes
if [[ ! "$FILE_PATH" =~ Dockerfile ]]; then
    exit 0
fi

# Check if hadolint is available
if ! command -v hadolint &> /dev/null; then
    echo "⚠️  hadolint not found. Install with: brew install hadolint (macOS) or nix-shell -p hadolint" >&2
    echo "   Skipping Dockerfile linting." >&2
    exit 0
fi

# Check if file exists
if [[ ! -f "$FILE_PATH" ]]; then
    exit 0
fi

echo "🐳 Validating Dockerfile: $FILE_PATH" >&2

# Run hadolint
echo "   Running hadolint..." >&2
if ! hadolint "$FILE_PATH" >&2; then
    echo "❌ hadolint found issues in $FILE_PATH" >&2
    exit 1
fi
echo "✅ hadolint passed" >&2

exit 0
