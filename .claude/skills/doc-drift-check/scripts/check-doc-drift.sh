#!/usr/bin/env bash
#
# Documentation-drift harness: the MECHANICAL half of the doc-drift-check skill.
# It fails when the parts of the code↔OpenAPI↔docs contract that can be checked
# deterministically have drifted. The semantic half (is the prose accurate?) is
# the agent's job — see ../SKILL.md.
#
# Checks (hard = fails the gate; soft = skipped when a tool/ref is missing):
#   1. hard  — the embedded spec and the api/ copy are byte-identical
#   2. hard  — the embedded spec parses and every local $ref resolves
#   3. hard  — routes ↔ spec contract test (TestRoutesMatchOpenAPISpec)
#   4. soft  — oasdiff: no BREAKING (ERR) changes vs origin/master
#   5. soft  — mkdocs --strict build (no broken links / nav)
#
# Usage:
#   check-doc-drift.sh          # full gate (all checks it can run)
#   check-doc-drift.sh --fast   # only the instant checks (1 + 2); for the pre-PR hook
#   check-doc-drift.sh --fix    # auto-zero the one mechanical drift it can: resync the api/ copy
#
set -uo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
cd "$PROJECT_DIR" || exit 1

EMBEDDED="internal/adapters/http/openapi.yaml"
API_COPY="api/openapi/openapi.yaml"

FAST=0; FIX=0
for arg in "$@"; do
  case "$arg" in
    --fast) FAST=1 ;;
    --fix)  FIX=1 ;;
  esac
done

fail=0
note() { printf '%s\n' "$*" >&2; }
bad()  { printf '  ✗ %s\n' "$*" >&2; fail=1; }
ok()   { printf '  ✓ %s\n' "$*" >&2; }
skip() { printf '  – %s (skipped: %s)\n' "$1" "$2" >&2; }

note "== doc-drift harness =="

# --- 1. OpenAPI: embedded == api/ copy --------------------------------------
if [ ! -f "$EMBEDDED" ] || [ ! -f "$API_COPY" ]; then
  bad "OpenAPI files missing ($EMBEDDED / $API_COPY)"
elif diff -q "$EMBEDDED" "$API_COPY" >/dev/null 2>&1; then
  ok "OpenAPI copies identical"
elif [ "$FIX" = 1 ]; then
  cp "$EMBEDDED" "$API_COPY" && ok "OpenAPI copies resynced (--fix): $API_COPY"
else
  bad "OpenAPI copies differ — the api/ copy is stale. Resync: cp $EMBEDDED $API_COPY (or run with --fix)"
fi

# --- 2. OpenAPI parses + local $refs resolve --------------------------------
if command -v python3 >/dev/null 2>&1; then
  if python3 - "$EMBEDDED" <<'PY'
import sys, re, yaml
p = sys.argv[1]
txt = open(p).read()
try:
    d = yaml.safe_load(txt)
except Exception as e:
    print("parse error: %s" % e); sys.exit(1)
refs = set(re.findall(r"\$ref: '#/components/schemas/([A-Za-z0-9]+)'", txt))
schemas = set((d.get('components', {}).get('schemas') or {}).keys())
missing = sorted(refs - schemas)
if missing:
    print("unresolved schema $refs: %s" % ", ".join(missing)); sys.exit(1)
sys.exit(0)
PY
  then ok "OpenAPI parses; all schema \$refs resolve"
  else bad "OpenAPI invalid (see message above)"
  fi
else
  skip "OpenAPI parse/ref check" "python3 not found"
fi

if [ "$FAST" = 1 ]; then
  [ "$fail" = 0 ] && note "== fast gate OK ==" || note "== fast gate FAILED =="
  exit "$fail"
fi

# --- 3. routes ↔ spec contract test -----------------------------------------
if command -v go >/dev/null 2>&1; then
  if go test ./internal/adapters/http/ -run TestRoutesMatchOpenAPISpec -count=1 >/tmp/doc-drift-contract.log 2>&1; then
    ok "routes ↔ spec contract test passes"
  else
    bad "routes ↔ spec contract test FAILED:"; sed 's/^/      /' /tmp/doc-drift-contract.log >&2
  fi
else
  skip "routes ↔ spec contract test" "go not found"
fi

# --- 4. oasdiff: no breaking (ERR) changes vs origin/master -----------------
OASDIFF="$(command -v oasdiff || echo "$(go env GOPATH 2>/dev/null)/bin/oasdiff")"
if [ -x "$OASDIFF" ] && git rev-parse --verify -q origin/master >/dev/null 2>&1; then
  if git show origin/master:"$EMBEDDED" > /tmp/doc-drift-base.yaml 2>/dev/null; then
    if "$OASDIFF" breaking /tmp/doc-drift-base.yaml "$EMBEDDED" --fail-on ERR >/tmp/doc-drift-oasdiff.log 2>&1; then
      ok "oasdiff: no breaking changes vs origin/master"
    else
      bad "oasdiff: breaking change(s) vs origin/master:"; sed 's/^/      /' /tmp/doc-drift-oasdiff.log >&2
    fi
  else
    skip "oasdiff breaking check" "no base spec on origin/master"
  fi
else
  skip "oasdiff breaking check" "oasdiff or origin/master unavailable"
fi

# --- 5. mkdocs --strict ------------------------------------------------------
if command -v uvx >/dev/null 2>&1; then
  if uvx --with mkdocs-material mkdocs build --strict >/tmp/doc-drift-mkdocs.log 2>&1; then
    ok "mkdocs --strict builds"
    rm -rf site 2>/dev/null || true
  else
    bad "mkdocs --strict failed (broken links / nav):"; tail -5 /tmp/doc-drift-mkdocs.log | sed 's/^/      /' >&2
  fi
else
  skip "mkdocs --strict build" "uvx not found"
fi

if [ "$fail" = 0 ]; then
  note "== doc-drift harness: GREEN (mechanical drift = 0) =="
else
  note "== doc-drift harness: DRIFT DETECTED — fix per the doc-drift-check skill =="
fi
exit "$fail"
