#!/bin/bash
# Claude Code Hook: Validate .PHONY targets match actual Makefile targets
#
# This hook runs after Edit/Write on the Makefile and warns if the .PHONY
# declarations don't match the actual targets defined in the file.

set -e

# Read hook input from stdin
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Exit if no file path or not the Makefile
if [ -z "$FILE_PATH" ]; then
  exit 0
fi

if [[ ! "$FILE_PATH" =~ Makefile$ ]]; then
  exit 0
fi

# Check if file exists
if [ ! -f "$FILE_PATH" ]; then
  exit 0
fi

cd "$CLAUDE_PROJECT_DIR"

# Extract .PHONY targets (all words after .PHONY:)
PHONY_TARGETS=$(grep -E '^\.PHONY:' "$FILE_PATH" | sed 's/\.PHONY://g' | tr ' ' '\n' | grep -v '^$' | sort -u)

# Extract actual targets (lines matching "target:" or "target: deps" but not starting with .)
ACTUAL_TARGETS=$(grep -E '^[a-zA-Z_][a-zA-Z0-9_-]*:' "$FILE_PATH" | cut -d: -f1 | sort -u)

# Find targets missing from .PHONY
MISSING_PHONY=""
for target in $ACTUAL_TARGETS; do
  if ! echo "$PHONY_TARGETS" | grep -qx "$target"; then
    MISSING_PHONY="$MISSING_PHONY $target"
  fi
done

# Find .PHONY entries that don't exist as targets
EXTRA_PHONY=""
for phony in $PHONY_TARGETS; do
  if ! echo "$ACTUAL_TARGETS" | grep -qx "$phony"; then
    EXTRA_PHONY="$EXTRA_PHONY $phony"
  fi
done

# Output warnings to stderr if there are mismatches
if [ -n "$MISSING_PHONY" ] || [ -n "$EXTRA_PHONY" ]; then
  echo "" >&2
  echo "Makefile .PHONY check: Mismatch detected!" >&2

  if [ -n "$MISSING_PHONY" ]; then
    echo "   Targets missing from .PHONY:$MISSING_PHONY" >&2
  fi

  if [ -n "$EXTRA_PHONY" ]; then
    echo "   .PHONY entries without targets:$EXTRA_PHONY" >&2
  fi

  echo "" >&2
  echo "   Please update the .PHONY declarations at the top of the Makefile." >&2
  echo "" >&2
fi

exit 0
