#!/bin/bash
# Claude Code Hook: Remind to check documentation after relevant changes
#
# This hook outputs a reminder to stderr when files are changed that might
# require documentation updates. Claude will see this output and can decide
# whether to update the documentation.

# Read hook input from stdin
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Exit silently if no file path
if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# Skip if the changed file is documentation itself (avoid infinite loops)
if [[ "$FILE_PATH" =~ \.(md|txt)$ ]] || [[ "$FILE_PATH" =~ ^.*/doc/.* ]] || [[ "$FILE_PATH" =~ README ]]; then
  exit 0
fi

# Skip hidden files and directories (except .github)
if [[ "$FILE_PATH" =~ /\.[^g] ]] && [[ ! "$FILE_PATH" =~ \.github ]]; then
  exit 0
fi

# Define files/patterns that likely require documentation updates
DOCS_RELEVANT=false

# Configuration files that affect usage/setup
if [[ "$FILE_PATH" =~ (Makefile|flake\.nix|\.goreleaser\.yml|\.golangci\.yml|go\.mod)$ ]]; then
  DOCS_RELEVANT=true
fi

# GitHub workflows
if [[ "$FILE_PATH" =~ \.github/workflows/.* ]]; then
  DOCS_RELEVANT=true
fi

# Main application code (new features, API changes)
if [[ "$FILE_PATH" =~ \.go$ ]]; then
  DOCS_RELEVANT=true
fi

# Output reminder to stderr (Claude will see this)
if [ "$DOCS_RELEVANT" = true ]; then
  echo "" >&2
  echo "📝 Documentation check: '$FILE_PATH' was modified." >&2
  echo "   Consider updating relevant documentation if this change affects:" >&2
  echo "   - README.md (project overview, quick start)" >&2
  echo "   - doc/DEVELOPMENT.md (development setup, make targets)" >&2
  echo "   - doc/ARCHITECTURE.md (code structure, patterns)" >&2
  echo "" >&2
fi

exit 0
