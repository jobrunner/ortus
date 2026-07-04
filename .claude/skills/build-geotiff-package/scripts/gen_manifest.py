#!/usr/bin/env python3
"""Generate an ortus raster bundle manifest from a Köppen-Geiger legend.txt.

The legend.txt shipped with the Beck et al. datasets has one class per line, e.g.:

    1:  Af  Tropical, rainforest                    [0 0 255]
    2:  Am  Tropical, monsoon                       [0 120 255]

This turns it into the inline `mapping:` block of ortus-raster.yaml, adding the
authoritative per-class RGB as a `color` hex property. Strings are quoted so YAML
never reinterprets a code like "NO"/"ON" as a boolean ("Norway problem"). Stdlib only.

Identity note — avoid the "latest" trap: a Köppen "present-day" map is a
classification over a fixed *reference period*, not "now". Encode that period in
the source --id (e.g. koeppen-geiger-1980-2016 for Beck et al. 2018 V1, or
koeppen-geiger-1991-2020 for V3) so a future dataset never silently becomes
"latest". The --id MUST equal the bundle filename stem.

Usage:
    python3 gen_manifest.py legend.txt \\
        --id koeppen-geiger-1980-2016 \\
        --name "Köppen-Geiger climate classification 1980–2016 (Beck et al. 2018, V1)" \\
        > ortus-raster.yaml
"""
import argparse
import re
import sys

GROUPS = {"A": "Tropical", "B": "Arid", "C": "Temperate", "D": "Cold", "E": "Polar"}

LINE = re.compile(
    r"^\s*(?P<value>\d+)\s*:\s*"          # 1:
    r"(?P<code>[A-Za-z]+)\s+"             # Af
    r"(?P<desc>.+?)\s*"                   # Tropical, rainforest
    r"\[\s*(?P<r>\d+)\s+(?P<g>\d+)\s+(?P<b>\d+)\s*\]\s*$"  # [0 0 255]
)


def yaml_str(s: str) -> str:
    """Double-quote a string for YAML, escaping backslashes and quotes."""
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def build_header(a: argparse.Namespace) -> str:
    lines = [
        "schema_version: 1",
        f"id: {a.id}",
        f"name: {yaml_str(a.name)}",
    ]
    if a.description:
        lines.append(f"description: {yaml_str(a.description)}")
    lines += [
        "license:",
        f"  name: {yaml_str(a.license)}",
        f"  attribution: {yaml_str(a.attribution)}",
        f"  url: {yaml_str(a.license_url)}",
        f"crs: {a.crs}",
        "layers:",
        f"  - id: {a.layer_id}",
        f"    file: {a.cog}",
        f"    band: {a.band}",
        f"    nodata: {a.nodata}",
        "    sampling: nearest",
        "    mapping:",
    ]
    return "\n".join(lines) + "\n"


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("legend", help="path to legend.txt")
    p.add_argument("--id", required=True,
                   help="source id (kebab-case); MUST equal the bundle filename stem. "
                        "Encode the reference period, not 'present'/'latest'.")
    p.add_argument("--name", required=True, help="human-readable name")
    p.add_argument("--description", default="", help="optional longer description")
    p.add_argument("--layer-id", default="classification", help="layer id within the bundle")
    p.add_argument("--cog", default="koeppen.cog.tif", help="COG filename inside the bundle")
    p.add_argument("--crs", default="EPSG:4326", help="EPSG:<code> the COG is in")
    p.add_argument("--band", type=int, default=1, help="1-based band index the mapping applies to")
    p.add_argument("--nodata", type=int, default=0, help="no-class sentinel pixel value")
    p.add_argument("--license", default="CC-BY-4.0")
    p.add_argument("--attribution", default="Beck et al. (2018), Scientific Data 5:180214")
    p.add_argument("--license-url", dest="license_url", default="https://www.gloh2o.org/koppen/")
    return p.parse_args()


def main() -> int:
    a = parse_args()

    rows = []
    with open(a.legend, encoding="utf-8") as fh:
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

    out = [build_header(a)]
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
