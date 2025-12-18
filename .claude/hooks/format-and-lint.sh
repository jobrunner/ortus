#!/bin/bash
# Claude Code Hook: Format and Lint Go files after Edit/Write
set -e

# Read hook input from stdin
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Exit if no file path
if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# Only process Go files
if [[ ! "$FILE_PATH" =~ \.go$ ]]; then
  exit 0
fi

# Check if file exists
if [ ! -f "$FILE_PATH" ]; then
  exit 0
fi

cd "$CLAUDE_PROJECT_DIR"

# Run gofmt (formatting)
if command -v gofmt &> /dev/null; then
  gofmt -w "$FILE_PATH" 2>/dev/null || true
fi

# Run goimports (import management)
if command -v goimports &> /dev/null; then
  goimports -w -local github.com/jobrunner/ortus "$FILE_PATH" 2>/dev/null || true
fi

# Run golangci-lint on the specific file (fast, focused linting)
if command -v golangci-lint &> /dev/null; then
  # Run linter but don't fail the hook - just report issues
  golangci-lint run --timeout=30s "$FILE_PATH" 2>&1 || true
fi

exit 0
