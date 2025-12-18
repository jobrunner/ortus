#!/bin/bash
# Claude Code Hook: Analyze documentation impact after code changes
#
# This hook analyzes code changes and provides specific suggestions about
# which documentation files need updating and what sections to review.
#
# Exit codes:
#   0 - Success (suggestions shown to Claude)
#   2 - Blocking error (would prevent the operation)

set -e

# Read hook input from stdin
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)

# Exit silently if no file path
if [ -z "$FILE_PATH" ]; then
    exit 0
fi

# Get project directory
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-.}"

# Skip if the changed file is documentation itself
if [[ "$FILE_PATH" =~ \.(md|txt)$ ]] || [[ "$FILE_PATH" =~ README ]]; then
    exit 0
fi

# Skip hidden files (except .github, .goreleaser, .golangci)
if [[ "$FILE_PATH" =~ /\. ]] && [[ ! "$FILE_PATH" =~ \.(github|goreleaser|golangci) ]]; then
    exit 0
fi

# Skip test files for documentation purposes
if [[ "$FILE_PATH" =~ _test\.go$ ]]; then
    exit 0
fi

# Initialize suggestions array
SUGGESTIONS=()
DOCS_TO_CHECK=()

# Helper function to add suggestion
add_suggestion() {
    SUGGESTIONS+=("$1")
}

add_doc() {
    DOCS_TO_CHECK+=("$1")
}

# Analyze based on file location and type
FILENAME=$(basename "$FILE_PATH")
DIRNAME=$(dirname "$FILE_PATH")

# === Configuration Files ===
if [[ "$FILENAME" == "config.go" ]] || [[ "$FILENAME" =~ config.*\.go$ ]]; then
    add_suggestion "Config structure changed - update config.yaml.example and environment variable documentation"
    add_doc "config.yaml.example"
    add_doc "README.md (Configuration section)"
    add_doc "doc/adr/0007-configuration-management.md"
fi

if [[ "$FILENAME" == "Makefile" ]]; then
    add_suggestion "Makefile changed - verify make targets in documentation"
    add_doc "doc/DEVELOPMENT.md (Make targets)"
    add_doc "README.md (Installation section)"
fi

if [[ "$FILENAME" == "flake.nix" ]]; then
    add_suggestion "Nix flake changed - update development setup instructions"
    add_doc "doc/DEVELOPMENT.md (Nix setup)"
fi

if [[ "$FILENAME" == "Dockerfile" ]] || [[ "$FILENAME" == "docker-compose.yaml" ]]; then
    add_suggestion "Docker configuration changed - update Docker usage documentation"
    add_doc "README.md (Docker section)"
fi

if [[ "$FILENAME" == ".goreleaser.yml" ]]; then
    add_suggestion "Release configuration changed - verify release documentation"
    add_doc "doc/DEVELOPMENT.md (Release process)"
fi

# === Main Application ===
if [[ "$FILE_PATH" =~ cmd/ortus/main\.go$ ]]; then
    add_suggestion "Main entry point changed - verify CLI flags and startup documentation"
    add_doc "README.md (CLI Flags section)"
    add_doc "README.md (Quick Start)"
fi

# === HTTP Handlers and API ===
if [[ "$FILE_PATH" =~ adapters/http/handlers\.go$ ]]; then
    add_suggestion "API handlers changed - update API endpoint documentation and curl examples"
    add_doc "README.md (API Endpoints section)"
    add_doc "doc/api.txt"
fi

if [[ "$FILE_PATH" =~ adapters/http/server\.go$ ]]; then
    add_suggestion "HTTP server changed - check middleware and routing documentation"
    add_doc "doc/ARCHITECTURE.md"
fi

if [[ "$FILE_PATH" =~ adapters/http/cors\.go$ ]]; then
    add_suggestion "CORS implementation changed - update CORS configuration documentation"
    add_doc "README.md (Configuration section)"
    add_doc "config.yaml.example"
fi

# === Domain Layer ===
if [[ "$DIRNAME" =~ internal/domain ]]; then
    add_suggestion "Domain model changed - review architecture documentation for consistency"
    add_doc "doc/ARCHITECTURE.md (Domain section)"
fi

# === Storage Adapters ===
if [[ "$DIRNAME" =~ adapters/storage ]]; then
    add_suggestion "Storage adapter changed - update storage configuration documentation"
    add_doc "README.md (Object Storage section)"
    add_doc "config.yaml.example"
fi

# === TLS/Security ===
if [[ "$FILE_PATH" =~ certmagic|tls ]]; then
    add_suggestion "TLS/security code changed - update TLS configuration documentation"
    add_doc "README.md (TLS section)"
    add_doc "doc/adr/0008-tls-certmagic.md"
fi

# === Ports/Interfaces ===
if [[ "$DIRNAME" =~ internal/ports ]]; then
    add_suggestion "Port interface changed - this may affect architecture documentation"
    add_doc "doc/ARCHITECTURE.md (Ports section)"
fi

# === New Go files (potential new features) ===
if [[ "$TOOL_NAME" == "Write" ]] && [[ "$FILE_PATH" =~ \.go$ ]]; then
    if [[ "$DIRNAME" =~ internal/(adapters|application|domain|ports) ]]; then
        add_suggestion "New Go file created - consider if architecture documentation needs updating"
        add_doc "doc/ARCHITECTURE.md"
    fi
fi

# === Output suggestions if any ===
if [ ${#SUGGESTIONS[@]} -gt 0 ]; then
    echo "" >&2
    echo "=== Documentation Analysis ===" >&2
    echo "File changed: $FILE_PATH" >&2
    echo "" >&2

    echo "Suggested actions:" >&2
    for suggestion in "${SUGGESTIONS[@]}"; do
        echo "  - $suggestion" >&2
    done
    echo "" >&2

    echo "Documentation to review:" >&2
    # Remove duplicates and print
    printf '%s\n' "${DOCS_TO_CHECK[@]}" | sort -u | while read -r doc; do
        if [ -n "$doc" ]; then
            echo "  - $doc" >&2
        fi
    done
    echo "" >&2

    echo "After completing the current task, please review and update the listed documentation if the changes affect user-facing features, configuration, or architecture." >&2
    echo "===============================" >&2
fi

exit 0
