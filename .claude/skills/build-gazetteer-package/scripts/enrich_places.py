#!/usr/bin/env python3
"""Enrich the `places` layer with the OSM prominence signals that make a bearing
("Peilung") anchor meaningful: `population`, `capital`, `wikidata`.

These tags live on the place nodes but are dropped at the ogr2ogr SELECT of the base
build. They are re-read here directly from the filtered place PBFs (temp/*-places.osm.pbf,
same vintage as the build) and joined onto `places` by `osm_id` — a fast, additive,
idempotent post-processing step (no osmium/ogr2ogr full rebuild).

  python3 scripts/enrich_places.py --apply                 # add + fill the three columns
  python3 scripts/enrich_places.py --apply --geonames      # + backfill population from GeoNames
  python3 scripts/enrich_places.py --check                 # report fill rates, assert sanity

Columns added to `places` (all nullable, ADD COLUMN IF NOT EXISTS):
  population INTEGER  -- OSM `population`, parsed to a non-negative integer (NULL if absent/unparseable)
  capital    TEXT     -- OSM `capital` verbatim rank value (e.g. '2','4','6','8','yes'); the
                         admin rank of the unit this place is the seat of. Consumer maps it to a bonus.
  wikidata   TEXT     -- OSM `wikidata` QID; its presence is a notability proxy.

Ordering: run after `link-hierarchy` (needs final `places` rows / osm_id). Independent of
romanize. See PLAN-bearing-salience.md and docs/reference/geopackage-schema.md.
"""
import argparse, collections, glob, json, re, sqlite3, subprocess, sys

G = "output/osm-admin-places.gpkg"     # default; override with --gpkg
PBF_GLOB = "temp/*-places.osm.pbf"     # filtered place PBFs from the base build
_DIGITS = re.compile(r"\d[\d\s.,]*")


def parse_population(raw):
    """OSM population is free-text ('1234', '1 234', '1,234', '~5000', '3000-4000').
    Take the first integer-looking run; return a non-negative int or None."""
    if not raw:
        return None
    m = _DIGITS.search(str(raw))
    if not m:
        return None
    digits = re.sub(r"[^\d]", "", m.group(0))
    if not digits:
        return None
    try:
        v = int(digits)
    except ValueError:
        return None
    return v if 0 <= v < 100_000_000 else None   # guard against absurd values


def extract_from_pbfs(pbf_glob=PBF_GLOB, log=print):
    """osm_id -> (population, capital, wikidata) across all place PBFs, keeping the
    richest record where extracts overlap. osm_id matches the gpkg (numeric, no prefix)."""
    out = {}
    files = sorted(glob.glob(pbf_glob))
    if not files:
        log(f"WARNING: no PBFs match {pbf_glob!r} — nothing to enrich from")
    for path in files:
        log(f"  reading {path}")
        proc = subprocess.Popen(
            ["osmium", "export", path, "-f", "geojsonseq", "--add-unique-id=type_id"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        for line in proc.stdout:
            line = line.strip().strip("\x1e").strip()
            if not line:
                continue
            try:
                f = json.loads(line)
            except json.JSONDecodeError:
                continue
            oid = f.get("id")
            if not oid:
                continue
            oid = oid[1:] if oid[:1] in "nwr" else oid   # 'n27564946' -> '27564946'
            p = f.get("properties", {})
            pop = parse_population(p.get("population"))
            cap = (p.get("capital") or "").strip()
            wd = (p.get("wikidata") or "").strip()
            prev = out.get(oid)
            if prev is None:
                out[oid] = [pop, cap, wd]
            else:
                if pop is not None:
                    prev[0] = pop if prev[0] is None else max(prev[0], pop)
                if cap and not prev[1]:
                    prev[1] = cap
                if wd and not prev[2]:
                    prev[2] = wd
        proc.wait()
    return out


def ensure_columns(con):
    cols = {r[1] for r in con.execute("PRAGMA table_info(places)")}
    if "population" not in cols:
        con.execute("ALTER TABLE places ADD COLUMN population INTEGER")
    if "capital" not in cols:
        con.execute("ALTER TABLE places ADD COLUMN capital TEXT")
    if "wikidata" not in cols:
        con.execute("ALTER TABLE places ADD COLUMN wikidata TEXT")


def apply(con, gpkg, use_geonames=False):
    ensure_columns(con)
    enrich = extract_from_pbfs()
    print(f"extracted {len(enrich)} enrichment records from PBFs")

    # UPDATE by osm_id. Only rows present in the map are touched; the rest stay NULL
    # (and are candidates for the GeoNames backfill).
    updates = []
    for fid, osm_id in con.execute("SELECT fid, osm_id FROM places"):
        rec = enrich.get(str(osm_id))
        if rec is None:
            continue
        pop, cap, wd = rec
        updates.append((pop, cap or None, wd or None, fid))
    # COALESCE population: a re-run where OSM has no population must not wipe a value a
    # prior --geonames backfill supplied (keeps the documented idempotency). capital and
    # wikidata are OSM-authoritative, so they refresh outright.
    con.executemany(
        "UPDATE places SET population=COALESCE(?, population), capital=?, wikidata=? WHERE fid=?", updates)
    con.commit()
    print(f"applied OSM tags to {len(updates)} places")

    if use_geonames:
        from_geonames = backfill_population_geonames(con, gpkg)
        print(f"GeoNames backfilled population for {from_geonames} places")

    report(con)


def backfill_population_geonames(con, gpkg, cache=None, log=print):
    """Fill places.population where NULL from the GeoNames per-country dumps (CC BY 4.0, cached
    under temp/gazetteers/). Scoped to the MENA countries romanize_gazetteers already handles
    (gz.ISO2_GENC3) — that is the sparse tail; other countries keep OSM population + class
    fallback (downloading every European dump would be disproportionate). Matched by folded
    non-Latin name + nearest coordinate, exactly like the romanization gazetteer cascade."""
    import gazetteers as gz
    cache = cache or gz.CACHE
    countries = set(gz.ISO2_GENC3)

    # NULL-population places with a native (non-Latin) name + representative coordinate, via
    # the spatialite CLI (same approach as romanize_gazetteers.export_rows).
    # name_native goes LAST so the pipe-delimited CLI output survives a '|' inside the
    # name: splitting with maxsplit=4 keeps everything after the 4th '|' as the name.
    sql = (
        "SELECT fid, country_iso, "
        "ST_Y(GeomFromGPB(geom)), ST_X(GeomFromGPB(geom)), name_native "
        "FROM places WHERE population IS NULL AND name_native IS NOT NULL AND name_native<>'';")
    out = subprocess.run(["spatialite", gpkg, sql], capture_output=True, text=True, check=True).stdout
    by_cc = collections.defaultdict(list)
    for line in out.splitlines():
        p = line.split("|", 4)
        if len(p) != 5:
            continue
        fid, cc, lat, lon, native = p
        if cc not in countries:
            continue
        try:
            by_cc[cc].append((int(fid), native, float(lat), float(lon)))
        except ValueError:
            continue

    updates = []
    skipped = sorted(countries - set(by_cc))
    for cc in sorted(by_cc):
        try:
            idx = gz.build_geonames_population_index(gz.fetch_geonames(cc, cache=cache, log=log))
        except Exception as e:
            log(f"  ! GeoNames {cc} unavailable: {str(e)[:80]}")
            continue
        hits = 0
        for fid, native, lat, lon in by_cc[cc]:
            # tight radius (~5 km): a same-name GeoNames feature farther away is more likely a
            # different place, and an inflated population would wrongly promote a village anchor
            pop = idx.lookup(native, lat, lon, max_deg=0.05)
            # Same sanity clamp as the OSM path (parse_population): reject absurd/negative
            # values so a bad GeoNames row can't promote a village to the top anchor.
            if pop and 0 <= int(pop) < 100_000_000:
                updates.append((int(pop), fid))
                hits += 1
        log(f"  [{cc}] backfilled {hits}/{len(by_cc[cc])} NULL-population places")
    con.executemany("UPDATE places SET population=? WHERE fid=?", updates)
    con.commit()
    if skipped:
        log(f"  (no NULL-population native-named places in: {', '.join(skipped)})")
    return len(updates)


def report(con):
    total = con.execute("SELECT count(*) FROM places").fetchone()[0]
    print(f"\nplaces: {total}")
    if total == 0:
        print("  (empty places layer — nothing to report)")
        return
    for col in ("population", "capital", "wikidata"):
        n = con.execute(
            f"SELECT count(*) FROM places WHERE {col} IS NOT NULL AND {col}<>''").fetchone()[0]
        print(f"  {col:11} {n:7d} ({100*n/total:5.1f}%)")
    print("  by class (population fill):")
    for cls, tot, hav in con.execute(
            "SELECT place, count(*), sum(population IS NOT NULL) FROM places GROUP BY place ORDER BY 2 DESC"):
        print(f"    {cls:9} {hav or 0:7d}/{tot:<7d} ({100*(hav or 0)/tot:5.1f}%)")


def check(con):
    """Assert sanity + report fill rates (used by `make verify`). Exit 1 on violation."""
    cols = {r[1] for r in con.execute("PRAGMA table_info(places)")}
    missing = [c for c in ("population", "capital", "wikidata") if c not in cols]
    bad_pop = 0
    if not missing:
        bad_pop = con.execute(
            "SELECT count(*) FROM places WHERE population IS NOT NULL AND "
            "(typeof(population)<>'integer' OR population<0)").fetchone()[0]
        report(con)
    else:
        print(f"missing columns: {missing}")
    print(f"\nnegative/non-integer population (must be 0): {bad_pop}")
    fail = bool(missing) or bad_pop
    print("CHECK:", "FAIL" if fail else "OK")
    return 1 if fail else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--geonames", action="store_true",
                    help="also backfill population from GeoNames where OSM has none")
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--gpkg", default=G, help="path to the GeoPackage")
    a = ap.parse_args()
    sys.path.insert(0, "scripts")
    con = sqlite3.connect(a.gpkg)
    try:
        if a.check:
            sys.exit(check(con))
        elif a.apply:
            apply(con, a.gpkg, use_geonames=a.geonames)
        else:
            ap.print_help()
    finally:
        con.close()  # runs even on the --check sys.exit / any exception


if __name__ == "__main__":
    main()
