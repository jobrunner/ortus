#!/usr/bin/env bash
#
# vuln-autofix.sh — apply the fixes govulncheck names for reachable findings.
#
# For every reachable (symbol-level) finding with a fixed version:
#   - stdlib  → raise the `toolchain` directive in go.mod to the fixed Go release
#   - modules → go get <module>@<fixed version>
# then go mod tidy, and a re-scan proves the fixes. Only go.mod/go.sum
# change: every workflow reads the Go version from go.mod (setup-go
# go-version-file), and the Docker builds follow the toolchain directive
# (GOTOOLCHAIN=auto).
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

# Highest of the versions on stdin by SemVer precedence (semver.org §11):
# numeric core, a release above its prereleases, prerelease identifiers
# numerically if both are numeric, else in ASCII order, numeric below
# alphanumeric, a shorter list below a longer one with the same prefix.
# Build metadata (+…) is ignored. sort -V gets prereleases wrong.
semver_max() {
    awk '
    function cmp(a, b,    pa, pb, ca, cb, xa, xb, na, nb, i, n) {
        sub(/^v/, "", a); sub(/^v/, "", b)
        sub(/\+.*/, "", a); sub(/\+.*/, "", b)
        pa = index(a, "-") ? substr(a, index(a, "-") + 1) : ""
        pb = index(b, "-") ? substr(b, index(b, "-") + 1) : ""
        if (index(a, "-")) a = substr(a, 1, index(a, "-") - 1)
        if (index(b, "-")) b = substr(b, 1, index(b, "-") - 1)
        split(a, ca, "."); split(b, cb, ".")
        for (i = 1; i <= 3; i++)
            if (ca[i] + 0 != cb[i] + 0) return ca[i] + 0 < cb[i] + 0 ? -1 : 1
        if (pa == pb) return 0
        if (pa == "") return 1
        if (pb == "") return -1
        na = split(pa, xa, "."); nb = split(pb, xb, ".")
        n = na < nb ? na : nb
        # "" x forces string comparison: awk compares numeric-looking split()
        # fields as numbers, so "1e10" would rank above "1e2".
        for (i = 1; i <= n; i++) {
            if ("" xa[i] == "" xb[i]) continue
            if (xa[i] ~ /^[0-9]+$/ && xb[i] ~ /^[0-9]+$/) return xa[i] + 0 < xb[i] + 0 ? -1 : 1
            if (xa[i] ~ /^[0-9]+$/) return -1
            if (xb[i] ~ /^[0-9]+$/) return 1
            return "" xa[i] < "" xb[i] ? -1 : 1
        }
        return na < nb ? -1 : (na > nb ? 1 : 0)
    }
    NF { if (max == "" || cmp($0, max) > 0) max = $0 }
    END { print max }'
}

# Highest fixed version per module (several findings may name different ones).
cut -f1 "$TMP/fixes.tsv" | sort -u | while read -r module; do
    target=$(awk -F'\t' -v m="$module" '$1 == m { print $2 }' "$TMP/fixes.tsv" | semver_max)
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

# Re-scan: a later go get or the tidy can move a module back below its fix,
# and the fix PR's ratchet gate would not notice (the finding is on master
# too). Every finding this run fixed must be gone, or there is no PR.
govulncheck -format json ./... > "$TMP/rescan.json" || { echo "govulncheck failed on the re-scan" >&2; exit 2; }
jq -r 'select(.finding) | .finding | select(.trace[0].function != null) | .osv' "$TMP/rescan.json" | sort -u > "$TMP/left.ids"
cut -f3 "$TMP/fixes.tsv" | sort -u | comm -12 - "$TMP/left.ids" > "$TMP/unfixed.ids"
if [ -s "$TMP/unfixed.ids" ]; then
    echo "Still reachable after the fixes: $(paste -sd, - < "$TMP/unfixed.ids")" >&2
    exit 2
fi

cat "$SUMMARY"
