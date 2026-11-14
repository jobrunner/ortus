#!/usr/bin/env bash
#
# vuln-gate.sh — govulncheck gate, absolute on master, a ratchet on PRs.
#
# Without a base directory: fails on every reachable (symbol-level)
# vulnerability. With one (a checkout of the PR's base commit): fails only on
# reachable vulnerabilities the PR adds, i.e. that the head has and the base
# does not.
#
# Why the PR ratchet: an absolute gate turns a CVE disclosed overnight into a
# red check on EVERY open PR, including the PR that fixes a different CVE.
# Two independent findings then deadlock each other's fix (2026-11-13: the Go
# toolchain bump failed the Docker scan, the base-image bump failed this one,
# and the ruleset has no bypass). Pre-existing findings are still listed as a
# warning, and the master run stays absolute: that is the alarm, and
# vuln-scan.yml opens the fix PR.
#
# Each tree is scanned with the Go toolchain its own go.mod declares, so a
# stdlib finding follows the toolchain: a bump clears it from the head and
# passes, a downgrade brings it back on the head only and fails. Scanning the
# base with the head's toolchain would hide a downgrade as pre-existing.
#
# If the base scan fails, the gate falls back to absolute: an unknown
# baseline must not let anything through.
#
# Usage: scripts/vuln-gate.sh [base-dir]
# Exit:  0 ok, 1 findings, 2 scan error
set -euo pipefail

BASE_DIR="${1:-}"

# Reachable OSV IDs of the module in $1, one per line. govulncheck's JSON mode
# exits 0 even with findings (a non-zero exit is a scan error), and a finding
# is symbol-level, i.e. reachable, when its innermost frame names a function.
scan() {
    local dir=$1 out=$2
    (
        # Explicit exit: scan() runs inside `if`, where errexit is off, and a
        # failed cd would scan the head twice and pass every finding.
        cd "$dir" || exit 2
        # Overrides GOTOOLCHAIN=local (setup-go sets it); the go command
        # downloads the declared toolchain if it is not the local one.
        GOTOOLCHAIN=$(go mod edit -json | jq -r '.Toolchain // ("go" + .Go)')
        export GOTOOLCHAIN
        govulncheck -format json ./...
    ) > "$out"
}
reachable() {
    jq -r 'select(.finding) | .finding | select(.trace[0].function != null) | .osv' "$1" | sort -u
}
describe() {
    # "GO-…: summary (fixed in …)" for the IDs on stdin, from the head scan.
    local id fixed summary
    while read -r id; do
        summary=$(jq -r --arg id "$id" 'select(.osv) | .osv | select(.id == $id) | .summary' "$HEAD_JSON" | head -1)
        fixed=$(jq -r --arg id "$id" 'select(.finding) | .finding | select(.osv == $id) | .fixed_version // "no fix yet"' "$HEAD_JSON" | sort -u | paste -sd, -)
        echo "  $id: $summary (fixed in $fixed)"
    done
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
HEAD_JSON="$TMP/head.json"

scan . "$HEAD_JSON" || { echo "::error::govulncheck failed on the head — scan error, not a finding"; exit 2; }
reachable "$HEAD_JSON" > "$TMP/head.ids"

: > "$TMP/base.ids"
mode="absolute"
if [ -n "$BASE_DIR" ]; then
    if scan "$BASE_DIR" "$TMP/base.json"; then
        reachable "$TMP/base.json" > "$TMP/base.ids"
        mode="ratchet"
    else
        echo "::warning::govulncheck failed on the base checkout — gating absolutely instead"
    fi
fi

comm -23 "$TMP/head.ids" "$TMP/base.ids" > "$TMP/new.ids"
comm -12 "$TMP/head.ids" "$TMP/base.ids" > "$TMP/old.ids"

if [ -s "$TMP/old.ids" ]; then
    echo "::warning::$(wc -l < "$TMP/old.ids" | tr -d ' ') reachable vulnerabilities already on the base branch (not caused by this PR, not gated here):"
    describe < "$TMP/old.ids"
fi

if [ -s "$TMP/new.ids" ]; then
    if [ "$mode" = "ratchet" ]; then
        echo "::error::This PR adds reachable vulnerabilities:"
    else
        echo "::error::Reachable vulnerabilities:"
    fi
    describe < "$TMP/new.ids"
    echo "Details: govulncheck ./..."
    exit 1
fi

echo "vuln-gate ($mode): no reachable vulnerabilities to gate on."
