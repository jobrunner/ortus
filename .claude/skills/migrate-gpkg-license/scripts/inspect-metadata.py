#!/usr/bin/env python3
"""Inspect the gpkg_metadata of a GeoPackage and report its ortus-license status.

Usage: inspect-metadata.py <path.gpkg>

Prints every gpkg_metadata row (scope / mime / standard-uri / full text) so the
license/attribution can be extracted, and a machine-readable STATUS line the
skill branches on:

  STATUS: MIGRATED          — an ortus-contract row already exists (and parses)
  STATUS: NEEDS_MIGRATION   — gpkg_metadata exists but has no ortus row
  STATUS: NO_METADATA       — no gpkg_metadata table at all
  STATUS: NOT_A_GPKG        — file missing or not a SQLite/GeoPackage

Read-only: never writes to the file.
"""
import json
import os
import sqlite3
import sys

ORTUS_URI = "https://ortus.dev/schema/dataset-metadata.json"


def main(path):
    if not os.path.exists(path):
        print(f"STATUS: NOT_A_GPKG ({path} does not exist)")
        return 2
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    except sqlite3.Error as e:
        print(f"STATUS: NOT_A_GPKG (cannot open: {e})")
        return 2
    try:
        cur = con.cursor()
        # gpkg_contents is the OGC-required registry table; its absence means this
        # SQLite file is not a GeoPackage (so NOT_A_GPKG, not merely NO_METADATA).
        cur.execute("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='gpkg_contents'")
        if cur.fetchone()[0] == 0:
            print("STATUS: NOT_A_GPKG (no gpkg_contents table — not a GeoPackage)")
            return 2
        cur.execute("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='gpkg_metadata'")
        if cur.fetchone()[0] == 0:
            print("STATUS: NO_METADATA (no gpkg_metadata table)")
            return 0

        cur.execute("SELECT id, md_scope, mime_type, md_standard_uri, metadata FROM gpkg_metadata ORDER BY id")
        rows = cur.fetchall()
        print(f"gpkg_metadata rows: {len(rows)}")
        ortus_row = None
        for rid, scope, mime, uri, meta in rows:
            print("-" * 72)
            print(f"id={rid}  scope={scope}  mime={mime}")
            print(f"md_standard_uri: {uri}")
            print(f"metadata:\n{meta}")
            if mime == "application/json" and uri == ORTUS_URI:
                ortus_row = (rid, meta)
        print("-" * 72)

        if ortus_row:
            rid, meta = ortus_row
            try:
                doc = json.loads(meta)
            except json.JSONDecodeError as e:
                print(f"ortus row id={rid} is present but MALFORMED JSON: {e}")
                print("STATUS: NEEDS_MIGRATION (ortus row is malformed)")
                return 0
            lic = doc.get("license") or {}
            has_license = any((lic.get(k) or "").strip() for k in ("name", "url", "attribution"))
            print(f"ortus row id={rid} parses; license={json.dumps(lic, ensure_ascii=False)}")
            if has_license:
                print("STATUS: MIGRATED")
            else:
                # A JSON row exists but carries no usable license → ortus still shows
                # nothing, so this is not done.
                print("STATUS: NEEDS_MIGRATION (ortus row present but license is empty)")
        else:
            print("STATUS: NEEDS_MIGRATION")
        return 0
    finally:
        con.close()


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))
