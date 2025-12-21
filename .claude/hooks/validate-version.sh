#!/usr/bin/env bash
# Hook: Validate VERSION file format (semver)
# Runs when VERSION or CHANGELOG.md files are modified

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
MODIFIED_FILE="${CLAUDE_MODIFIED_FILE:-}"

# Only run for VERSION or CHANGELOG.md changes
if [[ -z "$MODIFIED_FILE" ]] || [[ ! "$MODIFIED_FILE" =~ ^(VERSION|CHANGELOG\.md)$ ]]; then
    exit 0
fi

VERSION_FILE="$PROJECT_DIR/VERSION"
CHANGELOG_FILE="$PROJECT_DIR/CHANGELOG.md"

echo "🔢 Validating version..."

# Check VERSION file exists
if [[ ! -f "$VERSION_FILE" ]]; then
    echo "❌ VERSION file not found"
    exit 1
fi

# Read and validate version format (semver: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD])
VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
SEMVER_REGEX='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?(\+([0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*))?$'

if [[ ! "$VERSION" =~ $SEMVER_REGEX ]]; then
    echo "❌ Invalid version format: '$VERSION'"
    echo "   Expected semver format: MAJOR.MINOR.PATCH (e.g., 1.0.0, 0.1.0-alpha, 2.1.3+build.123)"
    exit 1
fi

echo "   Version: $VERSION ✓"

# Check that version is documented in CHANGELOG
if [[ -f "$CHANGELOG_FILE" ]]; then
    if ! grep -q "\[$VERSION\]" "$CHANGELOG_FILE" && ! grep -q "\[Unreleased\]" "$CHANGELOG_FILE"; then
        echo "⚠️  Version $VERSION not found in CHANGELOG.md"
        echo "   Make sure to document changes for this version."
    else
        echo "   CHANGELOG entry found ✓"
    fi
fi

echo "✅ Version validation passed"
