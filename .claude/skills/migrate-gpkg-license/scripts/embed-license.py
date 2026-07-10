#!/usr/bin/env python3
"""Embed (or update) the ortus dataset-metadata JSON license row in a GeoPackage.

Usage:
  embed-license.py --gpkg PATH --name NAME --url URL --attribution TEXT
                   [--description TEXT] [--dry-run]

Writes the row ortus reads for license/attribution:
  mime_type       = application/json
  md_standard_uri = https://ortus.dev/schema/dataset-metadata.json
  metadata        = {"license":{"name","url","attribution"},"description"?}

Additive + idempotent: existing gpkg_metadata rows are preserved; if an ortus
row already exists it is UPDATEd, otherwise a new row is INSERTed together with a
matching gpkg_metadata_reference row (scope 'geopackage'). --dry-run prints the
JSON without touching the file. --name/--url may be empty strings (e.g. a dataset
with no formal license — put the full terms + citation in --attribution).
"""
import argparse
import json
import os
import sqlite3
import sys

ORTUS_URI = "https://ortus.dev/schema/dataset-metadata.json"

META_DDL = """CREATE TABLE IF NOT EXISTS gpkg_metadata (
  id INTEGER CONSTRAINT m_pk PRIMARY KEY ASC NOT NULL,
  md_scope TEXT NOT NULL DEFAULT 'dataset',
  md_standard_uri TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT 'text/xml',
  metadata TEXT NOT NULL DEFAULT '')"""
REF_DDL = """CREATE TABLE IF NOT EXISTS gpkg_metadata_reference (
  reference_scope TEXT NOT NULL,
  table_name TEXT,
  column_name TEXT,
  row_id_value INTEGER,
  timestamp DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  md_file_id INTEGER NOT NULL,
  md_parent_id INTEGER)"""


def build_doc(args):
    doc = {"license": {"name": args.name, "url": args.url, "attribution": args.attribution}}
    if args.description:
        doc["description"] = args.description
    return doc


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--gpkg", required=True)
    ap.add_argument("--name", required=True, help="license name/SPDX id ('' if none)")
    ap.add_argument("--url", required=True, help="license URL ('' if none)")
    ap.add_argument("--attribution", required=True, help="attribution / citation text")
    ap.add_argument("--description", default="",
                    help="optional dataset description; if omitted, an existing "
                         "ortus row's description is preserved")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    doc = build_doc(args)
    payload = json.dumps(doc, ensure_ascii=False, separators=(",", ":"))

    if args.dry_run:
        print("DRY-RUN — would write (mime=application/json, uri=" + ORTUS_URI + "):")
        print(payload)
        return 0

    if not os.path.exists(args.gpkg):
        print(f"ERROR: {args.gpkg} does not exist", file=sys.stderr)
        return 2

    con = sqlite3.connect(args.gpkg)
    try:
        cur = con.cursor()
        # Refuse to mutate anything that is not a GeoPackage — gpkg_contents is
        # the OGC-required registry table, so its absence means wrong file/type.
        cur.execute("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='gpkg_contents'")
        if cur.fetchone()[0] == 0:
            print(f"ERROR: {args.gpkg} is not a GeoPackage (no gpkg_contents) — refusing to write", file=sys.stderr)
            return 2
        cur.execute(META_DDL)
        cur.execute(REF_DDL)
        # id DESC so the first row we see is the one ortus effectively uses (it
        # scans ORDER BY id and the last match wins) — keeps duplicate handling
        # and description-preservation deterministic.
        cur.execute("SELECT id, COALESCE(metadata,'') FROM gpkg_metadata WHERE md_standard_uri=? ORDER BY id DESC", (ORTUS_URI,))
        existing = cur.fetchall()
        ids = [r[0] for r in existing]
        if ids:
            # Preserve an existing description when --description was not supplied,
            # so re-running only to fix the license stays truly additive and does
            # not silently drop the description.
            if not args.description:
                for _, meta in existing:
                    try:
                        prev = json.loads(meta)
                    except (json.JSONDecodeError, TypeError):
                        continue
                    prior = prev.get("description") if isinstance(prev, dict) else None
                    if isinstance(prior, str) and prior.strip():
                        doc["description"] = prior
                        payload = json.dumps(doc, ensure_ascii=False, separators=(",", ":"))
                        break
            # Update ALL matching rows, not just the first: ortus reads by id order
            # (last wins), so leaving a stale duplicate would let the wrong row win.
            cur.execute(
                "UPDATE gpkg_metadata SET mime_type='application/json', md_scope='dataset', metadata=? WHERE md_standard_uri=?",
                (payload, ORTUS_URI))
            action = f"UPDATED {len(ids)} ortus row(s) id={ids}"
        else:
            cur.execute(
                "INSERT INTO gpkg_metadata (md_scope, md_standard_uri, mime_type, metadata) "
                "VALUES ('dataset', ?, 'application/json', ?)", (ORTUS_URI, payload))
            new_id = cur.lastrowid
            cur.execute(
                "INSERT INTO gpkg_metadata_reference "
                "(reference_scope, table_name, column_name, row_id_value, md_file_id, md_parent_id) "
                "VALUES ('geopackage', NULL, NULL, NULL, ?, NULL)", (new_id,))
            action = f"INSERTED ortus row id={new_id} (+ reference)"
        con.commit()
        # verify readback against the row ortus effectively uses (last by id)
        cur.execute("SELECT metadata FROM gpkg_metadata WHERE md_standard_uri=? ORDER BY id DESC LIMIT 1", (ORTUS_URI,))
        got = cur.fetchone()
        ok = got is not None and json.loads(got[0]) == doc
        print(f"{action} | readback OK={ok} | {os.path.basename(args.gpkg)}")
        return 0 if ok else 1
    finally:
        con.close()


if __name__ == "__main__":
    sys.exit(main())
