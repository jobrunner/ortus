#!/usr/bin/env bash
#
# vuln-autofix.sh — apply the fixes govulncheck names for reachable findings.
#
# For every reachable (symbol-level) finding with a fixed version:
#   - stdlib  → raise the `toolchain` directive in go.mod to the fixed Go release
#   - modules → go get <module>@<fixed version>
# then go mod tidy. Only go.mod/go.sum change: every workflow reads the Go
# version from go.mod (setup-go go-version-file), and the Docker builds follow
# the toolchain directive (GOTOOLCHAIN=auto).
#
# Used by vuln-scan.yml to open the fix PR unattended; runnable locally too.
# Writes one Markdown list item per applied fix to <summary-file>.
#
# Usage: scripts/vuln-autofix.sh <summary-file>
# Exit:  0 fixes applied, 3 nothing to fix, 2 scan/fix error
set -euo pipefail

SUMMARY=$1
# Follow go.mod's toolchain, before and after the bump, also where the
# environment pins GOTOOLCHAIN=local (setup-go does).
export GOTOOLCHAIN=auto

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

govulncheck -format json ./... > "$TMP/scan.json" || { echo "govulncheck failed" >&2; exit 2; }

# module<TAB>fixed version<TAB>OSV ID for every reachable finding with a fix.
jq -r 'select(.finding) | .finding
       | select(.trace[0].function != null and .fixed_version != null)
       | [.trace[0].module, .fixed_version, .osv] | @tsv' "$TMP/scan.json" | sort -u > "$TMP/fixes.tsv"

: > "$SUMMARY"
if [ ! -s "$TMP/fixes.tsv" ]; then
    echo "No reachable vulnerability with an available fix."
    exit 3
fi

# Highest fixed version per module (several findings may name different ones).
# Semver puts a release above its prereleases (v1.2.3 > v1.2.3-rc.1), sort -V
# does not; mapping the prerelease "-" to "~" (which sorts before anything)
# fixes that.
cut -f1 "$TMP/fixes.tsv" | sort -u | while read -r module; do
    target=$(awk -F'\t' -v m="$module" '$1 == m { print $2 }' "$TMP/fixes.tsv" \
        | sed 's/-/~/' | sort -V | tail -1 | sed 's/~/-/')
    ids=$(awk -F'\t' -v m="$module" '$1 == m { print $3 }' "$TMP/fixes.tsv" | sort -u | paste -sd, - | sed 's/,/, /g')
    if [ "$module" = "stdlib" ]; then
        current=$(go mod edit -json | jq -r '.Toolchain // ("go" + .Go)')
        go mod edit -toolchain="go${target#v}"
        echo "- Go toolchain ${current} → go${target#v} (${ids})" >> "$SUMMARY"
    else
        current=$(go list -m -f '{{.Version}}' "$module")
        go get "${module}@${target}"
        echo "- \`${module}\` ${current} → ${target} (${ids})" >> "$SUMMARY"
    fi
done
go mod tidy

cat "$SUMMARY"
