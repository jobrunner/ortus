#!/usr/bin/env python3
"""CodeCharta ratchet gate.

CodeCharta is a visualizer; it has no built-in "fail when worse". This turns its
merged map into three ratchets over metrics that aren't already gated elsewhere
(coverage has its own floors in the Test job; mutation is a separate gate):

  1. Complexity cap — no file may exceed its cap on the per-file SUM of function
     complexity. Files listed in the baseline are capped at their recorded value (so
     they can't grow); everything else is capped at default_cap. This stops a file
     becoming a monolith, but is satisfiable by splitting a file (the sum moves with
     the code). Ratchet: lower the numbers as code is simplified.
  1b. Function-complexity cap — same shape, on max_complexity_per_function (the single
     most complex function in a file). This is the overall-complexity control that
     canNOT be gamed by moving code between files: a function keeps its complexity
     wherever it lives, so passing requires actually simplifying the function.
  2. Hotspot gate — a file that is both complex (complexity >= min_complexity) and
     under-tested (line_coverage < min_coverage) fails, unless it is grandfathered
     in `allow`. This blocks NEW complex-and-untested files; shrink `allow` as the
     existing ones get tests.

Usage: codecharta-ratchet.py <map.cc.json[.gz]> [config.json]
Exit codes: 0 = within ratchet, 1 = a metric regressed (per-file report),
2 = usage / unreadable input / malformed map.
"""
import gzip
import json
import sys


def load_json(path):
    opener = gzip.open if path.endswith(".gz") else open
    with opener(path, "rt", encoding="utf-8") as fh:
        return json.load(fh)


def cap_check(files, metric, default_cap, baseline, label, cfg_path):
    """Per-file ceiling on `metric`: files in `baseline` are frozen at their recorded
    value (can't grow), everything else must stay <= default_cap. Returns
    (violations, hints); a baseline file now BELOW its cap yields a ratchet-down hint.
    Files missing the metric are skipped."""
    violations, hints = [], []
    for rel, attrs in sorted(files.items()):
        val = attrs.get(metric)
        if val is None:
            continue
        cap = baseline.get(rel, default_cap)
        if val > cap:
            where = "baseline" if rel in baseline else f"default_cap {default_cap}"
            violations.append(f"{label}: {rel} = {val:.0f} > {cap} ({where})")
        elif rel in baseline and val < cap:
            hints.append(f"ratchet down: {rel} {label} {cap} -> {val:.0f} in {cfg_path}")
    # Flag stale baseline entries (file renamed/deleted) so the config stays clean —
    # a dead entry otherwise silently stops applying, mirroring the hotspot.allow hint.
    for rel in sorted(baseline):
        if rel not in files:
            hints.append(f"{label} baseline: {rel} is not in the map (renamed/deleted?) — remove it from {cfg_path}")
    return violations, hints


def leaves(node, parts):
    """Yield (relpath, attributes) for every File node, path relative to repo root
    (the map's root node name — 'root' — is stripped, so the rest matches the
    committed baseline keys like 'internal/adapters/...')."""
    p = parts + [node["name"]]
    if node.get("type") == "File":
        yield "/".join(p[1:]), (node.get("attributes") or {})
    for child in node.get("children", []):
        yield from leaves(child, p)


def main():
    if len(sys.argv) < 2:
        print("usage: codecharta-ratchet.py <map.cc.json[.gz]> [config.json]", file=sys.stderr)
        return 2
    map_path = sys.argv[1]
    cfg_path = sys.argv[2] if len(sys.argv) > 2 else ".codecharta-ratchet.json"
    try:
        doc = load_json(map_path)
        cfg = load_json(cfg_path)
    except (OSError, ValueError) as e:  # missing/unreadable file or invalid JSON
        print(f"::error::cannot read input ({e})", file=sys.stderr)
        return 2
    nodes = doc.get("nodes")
    if not nodes:
        nodes = (doc.get("data") or {}).get("nodes")
    if not nodes:
        print(f"::error::{map_path}: no nodes in map — cannot run the ratchet", file=sys.stderr)
        return 2
    files = dict(leaves(nodes[0], []))

    try:
        cx = cfg["complexity"]
        metric, default_cap, baseline = cx["metric"], cx["default_cap"], cx["baseline"]
        fc = cfg["function_complexity"]
        fmetric, fdefault, fbaseline = fc["metric"], fc["default_cap"], fc["baseline"]
        hs = cfg["hotspot"]
        min_cx, min_cov, allow = hs["min_complexity"], hs["min_coverage"], set(hs["allow"])
    except (KeyError, TypeError) as e:  # missing key or wrong shape (typo in config)
        print(f"::error::{cfg_path}: malformed config ({e})", file=sys.stderr)
        return 2

    # Guard against a vacuous pass: if a cap metric is absent from EVERY file (a ccsh
    # version / parser change, or a typo'd metric name), cap_check would skip all files
    # and the gate would silently pass. Fail loudly instead — the map still has files.
    for m in (metric, fmetric):
        if files and not any(a.get(m) is not None for a in files.values()):
            print(f"::error::metric '{m}' is absent from every file in the map — "
                  f"ccsh version/parser mismatch? Refusing to pass vacuously.", file=sys.stderr)
            return 2

    violations, hints = [], []

    # 1. Per-file aggregate complexity (sum of function complexity). Stops a file
    # becoming a monolith — but is satisfiable by splitting a file, since the sum
    # just moves with the code.
    v, h = cap_check(files, metric, default_cap, baseline, "complexity", cfg_path)
    violations += v
    hints += h

    # 1b. Per-FUNCTION complexity — the overall-complexity control that canNOT be
    # gamed by moving code between files: a function keeps its complexity wherever it
    # lives, so relocating it never lowers this number. Passing requires actually
    # simplifying the function (or extracting cohesive sub-functions).
    v, h = cap_check(files, fmetric, fdefault, fbaseline, "function-complexity", cfg_path)
    violations += v
    hints += h

    # 2. Hotspots (complex AND under-tested). Files without coverage data are skipped
    # (can't assess — e.g. cmd tools not exercised by unit tests).
    for rel, attrs in sorted(files.items()):
        val = attrs.get(metric)
        cov = attrs.get("line_coverage")
        if val is None or cov is None:
            continue
        if val >= min_cx and cov < min_cov and rel not in allow:
            violations.append(f"hotspot: {rel} complexity {val:.0f} >= {min_cx} AND coverage {cov:.1f}% < {min_cov}%")
    for rel in sorted(allow):
        attrs = files.get(rel)
        if attrs is None:
            hints.append(f"allowlist: {rel} is not in the map (renamed/deleted?) — remove it from hotspot.allow")
            continue
        cov, val = attrs.get("line_coverage"), attrs.get(metric)
        if val is not None and cov is not None and not (val >= min_cx and cov < min_cov):
            hints.append(f"allowlist: {rel} is no longer a hotspot — remove it from hotspot.allow")

    for h in hints:
        print(f"::notice::CodeCharta ratchet — {h}")
    if violations:
        print(f"\n❌ CodeCharta ratchet: {len(violations)} regression(s):")
        for v in violations:
            print(f"  - {v}")
        print(f"\nAdd tests / simplify the file, or (with justification) adjust {cfg_path}.")
        return 1
    print(f"✅ CodeCharta ratchet OK — {len(files)} files within complexity caps + hotspot gate.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
