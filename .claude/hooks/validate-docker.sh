#!/usr/bin/env bash
# Hook: Validate Dockerfiles with hadolint and build test
# Runs when Dockerfile* files are modified

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
MODIFIED_FILE="${CLAUDE_MODIFIED_FILE:-}"

# Only run for Dockerfile changes
if [[ -z "$MODIFIED_FILE" ]] || [[ ! "$MODIFIED_FILE" =~ Dockerfile ]]; then
    exit 0
fi

# Check if hadolint is available
if ! command -v hadolint &> /dev/null; then
    echo "⚠️  hadolint not found. Install with: brew install hadolint (macOS) or nix-shell -p hadolint"
    echo "   Skipping Dockerfile linting."
    exit 0
fi

echo "🐳 Validating Dockerfile: $MODIFIED_FILE"

# Run hadolint
DOCKERFILE_PATH="$PROJECT_DIR/$MODIFIED_FILE"
if [[ -f "$DOCKERFILE_PATH" ]]; then
    echo "   Running hadolint..."
    if ! hadolint "$DOCKERFILE_PATH" 2>&1; then
        echo "❌ hadolint found issues in $MODIFIED_FILE"
        exit 1
    fi
    echo "✅ hadolint passed"
else
    echo "⚠️  File not found: $DOCKERFILE_PATH"
fi
