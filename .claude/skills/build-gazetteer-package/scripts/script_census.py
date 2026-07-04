#!/usr/bin/env python3
"""Census: classify every `name` in the GeoPackage by Unicode script, so we can size
the romanization work per script (and per country). Read-only."""
import sqlite3, collections, sys

G = "output/osm-admin-places.gpkg"

def script_of_char(ch):
    o = ord(ch)
    if (0x41 <= o <= 0x24F) or (0x1E00 <= o <= 0x1EFF): return "Latin"
    if (0x370 <= o <= 0x3FF) or (0x1F00 <= o <= 0x1FFF): return "Greek"
    if (0x400 <= o <= 0x52F): return "Cyrillic"
    if (0x530 <= o <= 0x58F): return "Armenian"
    if (0x10A0 <= o <= 0x10FF): return "Georgian"
    if (0x590 <= o <= 0x5FF): return "Hebrew"
    if (0x600 <= o <= 0x6FF) or (0x750 <= o <= 0x77F) or (0x8A0 <= o <= 0x8FF) \
       or (0xFB50 <= o <= 0xFDFF) or (0xFE70 <= o <= 0xFEFF): return "Arabic"
    if (0x4E00 <= o <= 0x9FFF) or (0x3400 <= o <= 0x4DBF): return "Han"
    if (0x3040 <= o <= 0x30FF): return "Kana"
    if (0xAC00 <= o <= 0xD7AF) or (0x1100 <= o <= 0x11FF): return "Hangul"
    return None  # digits, punct, spaces, symbols

def script_of_name(s):
    if not s: return "EMPTY"
    counts = collections.Counter()
    for ch in s:
        sc = script_of_char(ch)
        if sc: counts[sc] += 1
    if not counts: return "NONALPHA"
    return counts.most_common(1)[0][0]

con = sqlite3.connect(G)
overall = collections.Counter()
per_layer = {"places": collections.Counter(), "admin_levels": collections.Counter()}
per_cc_script = collections.defaultdict(collections.Counter)  # country_iso -> script -> n

for layer in ("places", "admin_levels"):
    for name, cc in con.execute(f"SELECT name, country_iso FROM {layer}"):
        sc = script_of_name(name)
        per_layer[layer][sc] += 1
        overall[sc] += 1
        if sc not in ("Latin", "EMPTY", "NONALPHA"):
            per_cc_script[cc or "??"][sc] += 1
con.close()

total = sum(overall.values())
nonlatin = sum(v for k, v in overall.items() if k not in ("Latin", "EMPTY", "NONALPHA"))

print(f"=== Overall ({total} rows, both layers) ===")
for sc, n in overall.most_common():
    print(f"  {sc:10} {n:8}  {100*n/total:5.1f}%")
print(f"\n  -> need romanization (non-Latin): {nonlatin} ({100*nonlatin/total:.1f}%)")

for layer in ("places", "admin_levels"):
    t = sum(per_layer[layer].values())
    nl = sum(v for k, v in per_layer[layer].items() if k not in ("Latin", "EMPTY", "NONALPHA"))
    print(f"\n=== {layer} ({t} rows) — non-Latin: {nl} ({100*nl/t:.1f}%) ===")
    for sc, n in per_layer[layer].most_common():
        print(f"  {sc:10} {n:8}  {100*n/t:5.1f}%")

print("\n=== Non-Latin by country (top per script) ===")
by_script = collections.defaultdict(list)
for cc, scmap in per_cc_script.items():
    for sc, n in scmap.items():
        by_script[sc].append((n, cc))
for sc in sorted(by_script):
    tops = sorted(by_script[sc], reverse=True)[:8]
    tot = sum(n for n, _ in by_script[sc])
    print(f"  {sc:9} (total {tot}): " + ", ".join(f"{cc}:{n}" for n, cc in tops))
