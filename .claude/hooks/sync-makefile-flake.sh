#!/bin/bash
# Claude Code Hook: Sync Makefile targets with Nix Flake documentation
#
# This hook runs after Edit/Write on the Makefile and:
# 1. Validates that the Makefile has properly documented targets (## comments)
# 2. Shows a summary of available targets that will appear in nix develop
# 3. Reminds to reload the shell if targets changed

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

# Count documented targets (those with ## comments)
DOCUMENTED=$(grep -cE '^[a-zA-Z_-]+:.*?## .*$' "$FILE_PATH" 2>/dev/null || echo "0")

# Count all targets
ALL_TARGETS=$(grep -cE '^[a-zA-Z_][a-zA-Z0-9_-]*:' "$FILE_PATH" 2>/dev/null || echo "0")

# Find undocumented targets
UNDOCUMENTED_LIST=""
while IFS= read -r target; do
  if ! grep -qE "^${target}:.*##" "$FILE_PATH"; then
    UNDOCUMENTED_LIST="$UNDOCUMENTED_LIST $target"
  fi
done < <(grep -oE '^[a-zA-Z_][a-zA-Z0-9_-]*:' "$FILE_PATH" | tr -d ':' | sort -u)

# Output info to stderr
echo "" >&2
echo "Makefile -> Nix Flake Sync:" >&2
echo "  $DOCUMENTED von $ALL_TARGETS Targets dokumentiert (mit ## Kommentar)" >&2

if [ -n "$UNDOCUMENTED_LIST" ]; then
  echo "" >&2
  echo "  Undokumentierte Targets (werden nicht in 'nix develop' angezeigt):" >&2
  echo "   $UNDOCUMENTED_LIST" >&2
  echo "" >&2
  echo "  Tipp: Füge '## Beschreibung' nach dem Target hinzu, z.B.:" >&2
  echo "    target-name: ## Kurze Beschreibung des Targets" >&2
fi

echo "" >&2
echo "  Die Targets werden automatisch beim nächsten 'nix develop' angezeigt." >&2
echo "  (Bei aktiver Shell: 'exit' und erneut 'nix develop' ausführen)" >&2
echo "" >&2

exit 0
