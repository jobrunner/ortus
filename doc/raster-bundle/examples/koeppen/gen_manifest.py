#!/usr/bin/env python3
"""Generate an ortus raster bundle manifest from the official Köppen-Geiger legend.txt.

The legend.txt shipped with the Beck et al. dataset has one class per line, e.g.:

    1:  Af  Tropical, rainforest                    [0 0 255]
    2:  Am  Tropical, monsoon                       [0 120 255]

This script turns it into the inline `mapping:` block of ortus-raster.yaml, adding the
authoritative per-class RGB as a `color` hex property. Strings are quoted so YAML never
reinterprets a code like "NO"/"ON" as a boolean ("Norway problem"). Stdlib only.

Usage:
    python3 gen_manifest.py legend.txt > ortus-raster.yaml
"""
import re
import sys

GROUPS = {"A": "Tropical", "B": "Arid", "C": "Temperate", "D": "Cold", "E": "Polar"}

LINE = re.compile(
    r"^\s*(?P<value>\d+)\s*:\s*"          # 1:
    r"(?P<code>[A-Za-z]+)\s+"             # Af
    r"(?P<desc>.+?)\s*"                   # Tropical, rainforest
    r"\[\s*(?P<r>\d+)\s+(?P<g>\d+)\s+(?P<b>\d+)\s*\]\s*$"  # [0 0 255]
)

HEADER = """\
schema_version: 1
id: koeppen-geiger-present
name: Köppen-Geiger 1980–2016 (V3)
description: >-
  Present-day Köppen-Geiger climate classification at ~1 km resolution
  for the 1980–2016 period.
license:
  name: CC-BY-4.0
  attribution: Beck et al. (2018), Scientific Data 5:180214
  url: https://www.gloh2o.org/koppen/
crs: EPSG:4326
layers:
  - id: present
    file: koeppen.cog.tif
    band: 1
    nodata: 0
    sampling: nearest
    mapping:
"""


def yaml_str(s: str) -> str:
    """Double-quote a string for YAML, escaping backslashes and quotes."""
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main() -> int:
    if len(sys.argv) != 2:
        sys.stderr.write("usage: gen_manifest.py legend.txt > ortus-raster.yaml\n")
        return 2

    rows = []
    with open(sys.argv[1], encoding="utf-8") as fh:
        for raw in fh:
            m = LINE.match(raw)
            if not m:
                continue  # skip headers / blank / comment lines
            value = int(m["value"])
            code = m["code"]
            desc = m["desc"].strip()
            group = GROUPS.get(code[0].upper(), "Unknown")
            color = "#{:02X}{:02X}{:02X}".format(int(m["r"]), int(m["g"]), int(m["b"]))
            rows.append((value, code, desc, group, color))

    if not rows:
        sys.stderr.write("error: no legend rows parsed — check the legend.txt format\n")
        return 1

    seen = set()
    for value, *_ in rows:
        if value in seen:
            sys.stderr.write(f"error: duplicate pixel value {value} in legend\n")
            return 1
        seen.add(value)

    out = [HEADER]
    width = max(len(str(v)) for v, *_ in rows)
    for value, code, desc, group, color in sorted(rows):
        key = f"{value}:".ljust(width + 1)
        out.append(
            f"      {key} {{ code: {yaml_str(code)}, "
            f"description: {yaml_str(desc)}, "
            f"group: {yaml_str(group)}, "
            f"color: {yaml_str(color)} }}\n"
        )
    sys.stdout.write("".join(out))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
