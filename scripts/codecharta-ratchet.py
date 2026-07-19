#!/usr/bin/env python3
"""CodeCharta ratchet gate.

CodeCharta is a visualizer; it has no built-in "fail when worse". This turns its
merged map into two ratchets over metrics that aren't already gated elsewhere
(coverage has its own floors in the Test job; mutation is a separate gate):

  1. Complexity cap — no file may exceed its cap. Files listed in the baseline are
     capped at their recorded value (so they can't grow); everything else is
     capped at default_cap. Ratchet: lower the numbers as code is simplified.
  2. Hotspot gate — a file that is both complex (complexity >= min_complexity) and
     under-tested (line_coverage < min_coverage) fails, unless it is grandfathered
     in `allow`. This blocks NEW complex-and-untested files; shrink `allow` as the
     existing ones get tests.

Usage: codecharta-ratchet.py <map.cc.json[.gz]> [config.json]
Exit code 0 = within ratchet, 1 = a metric regressed (with a per-file report).
"""
import gzip
import json
import sys


def load_json(path):
    opener = gzip.open if path.endswith(".gz") else open
    with opener(path, "rt", encoding="utf-8") as fh:
        return json.load(fh)


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
    doc = load_json(map_path)
    cfg = load_json(cfg_path)
    nodes = doc.get("nodes")
    if not nodes:
        nodes = (doc.get("data") or {}).get("nodes")
    if not nodes:
        print(f"::error::{map_path}: no nodes in map — cannot run the ratchet", file=sys.stderr)
        return 2
    files = dict(leaves(nodes[0], []))

    cx = cfg["complexity"]
    metric, default_cap, baseline = cx["metric"], cx["default_cap"], cx["baseline"]
    hs = cfg["hotspot"]
    min_cx, min_cov, allow = hs["min_complexity"], hs["min_coverage"], set(hs["allow"])

    violations, hints = [], []

    # 1. Complexity caps.
    for rel, attrs in sorted(files.items()):
        val = attrs.get(metric)
        if val is None:
            continue
        cap = baseline.get(rel, default_cap)
        if val > cap:
            where = "baseline" if rel in baseline else f"default_cap {default_cap}"
            violations.append(f"complexity: {rel} = {val:.0f} > {cap} ({where})")
        elif rel in baseline and val < cap:
            hints.append(f"ratchet down: {rel} complexity {cap} -> {val:.0f} in {cfg_path}")

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
        attrs = files.get(rel, {})
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
