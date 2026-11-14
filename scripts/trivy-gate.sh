#!/usr/bin/env bash
#
# trivy-gate.sh — image vulnerability gate, absolute on master, a ratchet on PRs.
#
# Reads Trivy JSON reports (scanned with --ignore-unfixed and the gate's
# severities, so every entry is fixable). Without a base report: fails on
# every finding. With one (the same image variant built from the PR's base
# commit and scanned with the same DB): fails only on findings the PR adds,
# keyed by vulnerability ID + package.
#
# Same reasoning as vuln-gate.sh: an absolute gate on PRs deadlocked two
# independent security fixes on 2026-11-13. The master run stays absolute.
#
# A missing or unreadable base report (base build failed) falls back to
# absolute gating.
#
# Usage: scripts/trivy-gate.sh <label> <head.json> [base.json]
# Exit:  0 ok, 1 findings, 2 bad input
set -euo pipefail

LABEL=$1
HEAD_JSON=$2
BASE_JSON="${3:-}"

[ -s "$HEAD_JSON" ] || { echo "::error::trivy-gate ($LABEL): head report missing: $HEAD_JSON"; exit 2; }

# One line per finding: ID<TAB>package<TAB>installed<TAB>fixed<TAB>severity
findings() {
    jq -r '.Results[]?.Vulnerabilities[]?
           | [.VulnerabilityID, .PkgName, .InstalledVersion, .FixedVersion, .Severity] | @tsv' "$1" | sort -u
}
keys() { cut -f1,2 | sort -u; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

findings "$HEAD_JSON" > "$TMP/head.tsv"
keys < "$TMP/head.tsv" > "$TMP/head.keys"

: > "$TMP/base.keys"
mode="absolute"
if [ -n "$BASE_JSON" ]; then
    if [ -s "$BASE_JSON" ] && findings "$BASE_JSON" > "$TMP/base.tsv"; then
        keys < "$TMP/base.tsv" > "$TMP/base.keys"
        mode="ratchet"
    else
        echo "::warning::trivy-gate ($LABEL): no usable base report — gating absolutely instead"
    fi
fi

comm -23 "$TMP/head.keys" "$TMP/base.keys" > "$TMP/new.keys"
comm -12 "$TMP/head.keys" "$TMP/base.keys" > "$TMP/old.keys"

# Full rows of the head findings whose key is in the given key file.
rows() { awk -F'\t' 'NR == FNR { k[$1 FS $2]; next } ($1 FS $2) in k' "$1" "$TMP/head.tsv" \
    | awk -F'\t' '{ printf "  %-8s %-20s %s %s → %s\n", $5, $1, $2, $3, $4 }'; }

if [ -s "$TMP/old.keys" ]; then
    echo "::warning::trivy-gate ($LABEL): $(wc -l < "$TMP/old.keys" | tr -d ' ') fixable findings already in the base image (not caused by this PR, not gated here)"
    rows "$TMP/old.keys"
fi

if [ -s "$TMP/new.keys" ]; then
    if [ "$mode" = "ratchet" ]; then
        echo "::error::trivy-gate ($LABEL): this PR adds $(wc -l < "$TMP/new.keys" | tr -d ' ') fixable findings"
    else
        echo "::error::trivy-gate ($LABEL): $(wc -l < "$TMP/new.keys" | tr -d ' ') fixable findings"
    fi
    rows "$TMP/new.keys"
    exit 1
fi

echo "trivy-gate ($LABEL, $mode): no fixable findings to gate on."
