#!/usr/bin/env bash
#
# coverage-gate.sh — per-package coverage ratchet.
#
# Computes per-package statement coverage from a Go coverprofile and fails if
# any package listed in .coverage-floors is below its floor. Packages not
# listed are exempt (composition root, cmd, thin SDK wrappers — see
# docs/explanation/technical-debt.md). The floors are a RATCHET: they may only ever be raised.
#
# Usage: scripts/coverage-gate.sh [coverprofile]   (default: coverage.out)
set -euo pipefail

# Force a C numeric locale: awk's printf "%.1f" and numeric comparisons must use a
# dot decimal separator. Under a comma-decimal locale (e.g. de_DE.UTF-8) "%.1f" yields
# "100,0", which awk then compares as a STRING ("100,0" < "99" is true) → spurious
# BELOW-FLOOR failures. CI runs under C; make local runs match it.
export LC_ALL=C

PROFILE="${1:-coverage.out}"
FLOORS="$(dirname "$0")/../.coverage-floors"
MODULE="github.com/jobrunner/ortus"

[ -f "$PROFILE" ] || { echo "coverage-gate: profile not found: $PROFILE" >&2; exit 2; }
[ -f "$FLOORS" ]  || { echo "coverage-gate: floors file not found: $FLOORS" >&2; exit 2; }

BYPKG="$(mktemp)"
trap 'rm -f "$BYPKG"' EXIT

# Per-package + global statement coverage from the profile.
#   line: <module>/<pkg>/<file>.go:s.c,e.c <numStmts> <count>
awk -v module="$MODULE/" '
  NR == 1 && $1 == "mode:" { next }
  {
    path = $1; sub(/:.*/, "", path)        # strip :start.col,end.col
    sub(module, "", path)                  # strip module prefix
    pkg = path; sub(/\/[^\/]*$/, "", pkg)  # dir = package
    stmts = $2; cnt = $3
    tot[pkg] += stmts; gtot += stmts
    if (cnt > 0) { cov[pkg] += stmts; gcov += stmts }
  }
  END {
    for (p in tot) printf "%s %d %d\n", p, cov[p], tot[p]
    printf "TOTAL %d %d\n", gcov, gtot
  }
' "$PROFILE" > "$BYPKG"

fail=0
printf "%-42s %8s %7s\n" "package" "cov" "floor"
printf -- "------------------------------------------------------------\n"
while read -r pkg floor; do
  [ -z "$pkg" ] && continue
  case "$pkg" in \#*) continue ;; esac
  read -r c t <<<"$(awk -v p="$pkg" '$1==p {print $2, $3}' "$BYPKG")"
  if [ -z "${t:-}" ] || [ "${t:-0}" -eq 0 ]; then
    printf "%-42s %8s %7s  NO DATA\n" "$pkg" "-" "$floor"; fail=1; continue
  fi
  pct=$(awk -v c="$c" -v t="$t" 'BEGIN{printf "%.1f", 100*c/t}')
  if awk -v p="$pct" -v f="$floor" 'BEGIN{exit !(p < f)}'; then
    printf "%-42s %7s%% %6s%%  ▼ BELOW FLOOR\n" "$pkg" "$pct" "$floor"; fail=1
  else
    printf "%-42s %7s%% %6s%%\n" "$pkg" "$pct" "$floor"
  fi
done < "$FLOORS"

if [ "$fail" -ne 0 ]; then
  echo
  echo "coverage-gate: FAIL — a package dropped below its floor." >&2
  echo "The fix is to ADD TESTS. Floors are a raise-only ratchet; lowering one" >&2
  echo "is a deliberate, justified exception that must be called out in the PR." >&2
  exit 1
fi
echo
echo "coverage-gate: OK — all floored packages at or above their floor."
