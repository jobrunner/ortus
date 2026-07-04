#!/usr/bin/env python3
"""Build admin_levels_west_palearctic.yaml + validation_report.md from the
workflow's structured per-country results.

Input : temp/wf_result.json  — a JSON array of country objects with shape
        {iso, country_name, wiki_key, partial_coverage, levels:[{level,name,
         equivalent,present_in_data,source}], notes:[...]}
Output: admin_levels_west_palearctic.yaml, validation_report.md (repo root)

No third-party deps: YAML is emitted manually (and can be validated separately
with `uv run --with pyyaml python -c "import yaml; yaml.safe_load(open(...))"`).
"""
from __future__ import annotations
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RESULT = ROOT / "temp" / "wf_result.json"
YAML_OUT = ROOT / "admin_levels_west_palearctic.yaml"
REPORT_OUT = ROOT / "validation_report.md"

# Canonical vocabulary referenced by each level's `equivalent`.
EQUIVALENT_LEVELS = [
    ("country", "Sovereign state (admin_level 2)"),
    ("state", "First-order subdivision (Land, oblast, comunidad autónoma, ...)"),
    ("region", "Administrative region / mid-tier grouping (e.g. Regierungsbezirk, NUTS region)"),
    ("province", "Province / department tier"),
    ("county", "County or district tier"),
    ("district", "District / subdistrict"),
    ("municipality", "Municipality / commune (basic local government unit)"),
    ("borough", "Sub-municipal borough / urban district"),
    ("parish", "Civil parish"),
    ("submunicipality", "Other sub-municipal unit (quarter, ward, locality)"),
    ("other", "Does not map to a standard tier (see notes)"),
]
VALID_EQUIV = {k for k, _ in EQUIVALENT_LEVELS}


def dq(s) -> str:
    """Emit a safe double-quoted YAML scalar."""
    if s is None:
        return '""'
    s = str(s).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ").strip()
    return f'"{s}"'


def main() -> int:
    if not RESULT.exists():
        print(f"ERROR: {RESULT} not found. Write the workflow result there first.", file=sys.stderr)
        return 1
    data = json.loads(RESULT.read_text())
    if not isinstance(data, list) or not data:
        print("ERROR: result JSON is not a non-empty array.", file=sys.stderr)
        return 1

    countries = sorted(data, key=lambda c: c["iso"])

    # ---- emit YAML ----
    out = []
    out.append("# OSM admin_level reference for the Western Palearctic")
    out.append("# Generated from: OSM Wiki Template:Admin level (verbatim country table)")
    out.append("#   + empirical admin_level inventory of output/osm-admin-places.gpkg.")
    out.append("# Every level row is backed by an OSM Wiki entry and/or real OSM features.")
    out.append("# partial_coverage=true means the extract does not fully represent this")
    out.append("#   country's admin hierarchy; the reason (regional sliver / missing L2 polygon /")
    out.append("#   missing sub-units / political partition) is in each country's notes.")
    out.append("# See validation_report.md for method, sources and caveats.")
    out.append("")
    out.append("version: 1")
    out.append("")
    out.append("equivalent_levels:")
    for key, desc in EQUIVALENT_LEVELS:
        out.append(f"  {key}:")
        out.append(f"    description: {dq(desc)}")
    out.append("")
    out.append("countries:")
    out.append("")

    for c in countries:
        out.append(f"  {c['iso']}:")
        out.append(f"    name: {dq(c['country_name'])}")
        out.append(f"    partial_coverage: {'true' if c.get('partial_coverage') else 'false'}")
        out.append("    levels:")
        for lv in sorted(c["levels"], key=lambda x: x["level"]):
            out.append(f"      {int(lv['level'])}:")
            out.append(f"        name: {dq(lv['name'])}")
            out.append(f"        equivalent: {lv['equivalent']}")
            out.append(f"        present_in_data: {'true' if lv.get('present_in_data') else 'false'}")
            out.append(f"        source: {dq(lv['source'])}")
        notes = c.get("notes") or []
        if notes:
            out.append("    notes:")
            for n in notes:
                out.append(f"      - {dq(n)}")
        out.append("")

    YAML_OUT.write_text("\n".join(out) + "\n")

    # ---- validation checks ----
    checks = []
    def chk(name, ok, detail=""):
        checks.append((name, ok, detail))

    isos = [c["iso"] for c in countries]
    chk("countries are unique", len(isos) == len(set(isos)), f"{len(isos)} entries")
    chk("sorted alphabetically by ISO", isos == sorted(isos))

    dup_levels = []
    empty_names = []
    bad_equiv = []
    missing_fields = []
    for c in countries:
        seen = set()
        for lv in c["levels"]:
            if lv["level"] in seen:
                dup_levels.append(f"{c['iso']} L{lv['level']}")
            seen.add(lv["level"])
            if not (lv.get("name") or "").strip():
                empty_names.append(f"{c['iso']} L{lv['level']}")
            if lv.get("equivalent") not in VALID_EQUIV:
                bad_equiv.append(f"{c['iso']} L{lv['level']}={lv.get('equivalent')}")
            if not lv.get("source"):
                missing_fields.append(f"{c['iso']} L{lv['level']} (no source)")
    chk("no duplicate admin_level within a country", not dup_levels, ", ".join(dup_levels))
    chk("every level has a non-empty name", not empty_names, ", ".join(empty_names))
    chk("every equivalent is from the canonical set", not bad_equiv, ", ".join(bad_equiv))
    chk("every level has name + equivalent + source", not missing_fields, ", ".join(missing_fields))

    # ---- aggregates for the report ----
    partial = [c for c in countries if c.get("partial_coverage")]
    undocumented = [c for c in countries if c.get("wiki_key") == "none"]
    def has_l2(c):
        return any(lv["level"] == 2 and lv.get("present_in_data") for lv in c["levels"])
    missing_l2 = [c for c in countries if not has_l2(c)]
    data_not_wiki = []   # levels present in data but no wiki source
    wiki_not_data = []   # levels from wiki, absent in data
    for c in countries:
        for lv in c["levels"]:
            src = (lv.get("source") or "").lower()
            in_data = lv.get("present_in_data")
            wiki_sourced = "wiki" in src
            if in_data and not wiki_sourced:
                data_not_wiki.append(f"{c['iso']} L{lv['level']} ({lv['name']})")
            if (not in_data) and wiki_sourced:
                wiki_not_data.append(f"{c['iso']} L{lv['level']} ({lv['name']})")

    total_levels = sum(len(c["levels"]) for c in countries)

    # ---- emit report ----
    r = []
    r.append("# Validation report — OSM admin_level reference (Western Palearctic)")
    r.append("")
    r.append(f"- Countries documented: **{len(countries)}**")
    r.append(f"- Total level definitions: **{total_levels}**")
    r.append("")
    r.append("## Sources")
    r.append("")
    r.append("1. **OSM Wiki — `Template:Admin level`** (the verbatim per-country `admin_level` "
             "table transcluded by [`Tag:boundary=administrative`]"
             "(https://wiki.openstreetmap.org/wiki/Tag:boundary%3Dadministrative)). "
             "Fetched as raw wikitext; not a lossy summary.")
    r.append("2. **Empirical inventory** of `output/osm-admin-places.gpkg` (`admin_levels` layer): "
             "which `admin_level` values actually occur per country, with feature counts and "
             "example names. This is real OSM data from the Geofabrik-based build.")
    r.append("")
    r.append("## Method")
    r.append("")
    r.append("Per country, one research agent + one adversarial verification agent, each grounded "
             "ONLY in the two sources above. A level is included if it is present in the data "
             "**or** defined (non-`n/a`) in the wiki. `equivalent` is a semantic classification "
             "(by meaning, not by number) into the canonical vocabulary in `equivalent_levels`.")
    r.append("")
    r.append("## Validation checks")
    r.append("")
    for name, ok, detail in checks:
        mark = "✅" if ok else "❌"
        line = f"- {mark} {name}"
        if detail and not ok:
            line += f" — {detail}"
        elif detail and ok:
            line += f" — {detail}"
        r.append(line)
    r.append("")
    r.append("## Detected peculiarities")
    r.append("")
    r.append(f"### Incomplete hierarchy coverage — `partial_coverage: true` ({len(partial)})")
    r.append("The extract does not fully represent the country's admin hierarchy. The reason "
             "varies and is stated in each country's `notes`: only part of the territory is "
             "in-region (e.g. AM, AZ, IR, RU, SY, YE, NE), the top-level `admin_level=2` boundary "
             "relation did not import (e.g. FR, ES, HR, NL, NO), some sub-units are missing "
             "(e.g. LI — 8 of 11 municipalities), or the country is politically partitioned "
             "(CY, IL, PS). The wiki column still reflects the full national scheme.")
    r.append("")
    for c in partial:
        r.append(f"- **{c['iso']}** {c['country_name']}")
    r.append("")
    r.append(f"### Missing `admin_level=2` country polygon in the data ({len(missing_l2)})")
    r.append("These countries have **no** OSM `admin_level=2` feature in the `admin_levels` layer "
             "— either a regional sliver (country boundary out of region) or the known OSM issue "
             "that large/complex country relations fail to import. Country attribution in the "
             "reverse-geocoder is unaffected (it uses Natural Earth + coverage fills), but an "
             "`admin_level`-only lookup would not find a level-2 polygon here.")
    r.append("")
    for c in missing_l2:
        r.append(f"- **{c['iso']}** {c['country_name']}")
    r.append("")
    r.append(f"### Territories without a dedicated OSM Wiki entry ({len(undocumented)})")
    r.append("Classified from empirical data + parent-country scheme; flagged in their `notes`.")
    r.append("")
    for c in undocumented:
        r.append(f"- **{c['iso']}** {c['country_name']}")
    r.append("")
    r.append(f"### Levels present in data but not wiki-documented ({len(data_not_wiki)})")
    r.append("Backed by OSM features only (still source-verifiable per the quality bar).")
    r.append("")
    for x in data_not_wiki:
        r.append(f"- {x}")
    r.append("")
    r.append(f"### Levels defined by the wiki but absent in our extract ({len(wiki_not_data)})")
    r.append("")
    for x in wiki_not_data:
        r.append(f"- {x}")
    r.append("")
    r.append("## Countries flagged for manual review")
    r.append("")
    r.append("Anything with non-trivial `notes` (proposed status, embedded/disputed territory, "
             "ambiguous hierarchy) is worth a human glance:")
    r.append("")
    flagged = [c for c in countries if len(c.get("notes") or []) > 0]
    for c in flagged:
        r.append(f"- **{c['iso']}** {c['country_name']} — {len(c['notes'])} note(s)")
    r.append("")

    REPORT_OUT.write_text("\n".join(r) + "\n")

    failed = [name for name, ok, _ in checks if not ok]
    print(f"Wrote {YAML_OUT.name} ({len(countries)} countries, {total_levels} levels)")
    print(f"Wrote {REPORT_OUT.name}")
    if failed:
        print(f"VALIDATION FAILURES: {failed}", file=sys.stderr)
        return 2
    print("All validation checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
