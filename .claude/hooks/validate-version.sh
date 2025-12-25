#!/usr/bin/env bash
# Hook: Validate VERSION file format (semver)
# Runs when VERSION or CHANGELOG.md files are modified via Edit/Write

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Read hook input from stdin (JSON format)
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Exit silently if no file path
if [[ -z "$FILE_PATH" ]]; then
    exit 0
fi

# Get filename
FILENAME=$(basename "$FILE_PATH")

# Only run for VERSION or CHANGELOG.md changes
if [[ "$FILENAME" != "VERSION" ]] && [[ "$FILENAME" != "CHANGELOG.md" ]]; then
    exit 0
fi

VERSION_FILE="$PROJECT_DIR/VERSION"
CHANGELOG_FILE="$PROJECT_DIR/CHANGELOG.md"

echo "🔢 Validating version..." >&2

# Check VERSION file exists
if [[ ! -f "$VERSION_FILE" ]]; then
    echo "❌ VERSION file not found" >&2
    exit 1
fi

# Read and validate version format (semver: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD])
VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
SEMVER_REGEX='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?(\+([0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*))?$'

if [[ ! "$VERSION" =~ $SEMVER_REGEX ]]; then
    echo "❌ Invalid version format: '$VERSION'" >&2
    echo "   Expected semver format: MAJOR.MINOR.PATCH (e.g., 1.0.0, 0.1.0-alpha, 2.1.3+build.123)" >&2
    exit 1
fi

echo "   Version: $VERSION ✓" >&2

# Check that version is documented in CHANGELOG
if [[ -f "$CHANGELOG_FILE" ]]; then
    if ! grep -q "\[$VERSION\]" "$CHANGELOG_FILE" && ! grep -q "\[Unreleased\]" "$CHANGELOG_FILE"; then
        echo "⚠️  Version $VERSION not found in CHANGELOG.md" >&2
        echo "   Make sure to document changes for this version." >&2
    else
        echo "   CHANGELOG entry found ✓" >&2
    fi
fi

echo "✅ Version validation passed" >&2

exit 0
