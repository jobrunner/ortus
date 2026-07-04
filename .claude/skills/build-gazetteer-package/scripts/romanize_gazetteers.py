#!/usr/bin/env python3
"""Upgrade the machine-transliterated Arabic/Hebrew rows with authoritative gazetteer
romanizations (the gns-bgn / geonames steps of the cascade in docs/reference/romanization.md).

Runs AFTER `make romanize`. For every row whose name came from the machine last-resort
(translit-ar-bgn / translit-he-ungegn) — or from a previous gazetteer run, so this is
re-derivable and idempotent — it looks the native name up in NGA GNS, then GeoNames, matched
by same country + nearest coordinate. On a hit, `name` becomes the gazetteer roman form and
`name_source` becomes gns-bgn / geonames; on a miss it stays the machine transliteration
(recomputed from name_native). Downloads are cached under temp/gazetteers/ (offline re-run).

Usage:
  python3 scripts/romanize_gazetteers.py --dry-run   # report hit rates, write nothing
  python3 scripts/romanize_gazetteers.py --apply     # write name / name_source
"""
import argparse, collections, os, sqlite3, subprocess, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import gazetteers as gz
from romanize import script_of_name, machine_translit

G = "output/osm-admin-places.gpkg"
GAZETTEER_SOURCES = ("translit-ar-bgn", "translit-he-ungegn", "gns-bgn", "geonames")

# eligible rows + a representative coordinate (point for places, ST_PointOnSurface for polygons)
EXPORT_SQL = """
SELECT 'places', fid, name_native, country_iso,
       ST_Y(GeomFromGPB(geom)), ST_X(GeomFromGPB(geom))
  FROM places
 WHERE name_source IN ({q}) AND name_native IS NOT NULL AND name_native <> ''
UNION ALL
SELECT 'admin_levels', fid, name_native, country_iso,
       ST_Y(ST_PointOnSurface(GeomFromGPB(geom))), ST_X(ST_PointOnSurface(GeomFromGPB(geom)))
  FROM admin_levels
 WHERE name_source IN ({q}) AND name_native IS NOT NULL AND name_native <> '';
""".format(q=",".join(f"'{s}'" for s in GAZETTEER_SOURCES))


def export_rows(gpkg):
    """(layer, fid, name_native, cc, lat, lon) for every gazetteer-eligible row, via spatialite."""
    out = subprocess.run(["spatialite", gpkg, EXPORT_SQL],
                         capture_output=True, text=True, check=True).stdout
    rows = []
    for line in out.splitlines():
        p = line.split("|")
        if len(p) != 6:
            continue
        layer, fid, native, cc, lat, lon = p
        try:
            rows.append((layer, int(fid), native, cc, float(lat), float(lon)))
        except ValueError:
            continue
    return rows


def indexes_for(cc, cache, log):
    """(gns_index, geonames_index) for a country; either may be None if that source has no data."""
    gns = geo = None
    try:
        gns = gz.build_gns_index(gz.fetch_gns(cc, cache=cache, log=log))
    except Exception as e:
        log(f"  ! GNS {cc} unavailable: {str(e)[:80]}")
    try:
        geo = gz.build_geonames_index(gz.fetch_geonames(cc, cache=cache, log=log))
    except Exception as e:
        log(f"  ! GeoNames {cc} unavailable: {str(e)[:80]}")
    return gns, geo


def resolve(rows, cache, log):
    """-> (updates, stats). Cascade per row: gns-bgn -> geonames -> machine fallback."""
    updates = []
    stats = collections.Counter()
    by_cc = collections.defaultdict(list)
    for r in rows:
        by_cc[r[3]].append(r)
    for cc in sorted(by_cc):
        log(f"[{cc}] {len(by_cc[cc])} eligible rows")
        gns, geo = indexes_for(cc, cache, log)
        for layer, fid, native, _cc, lat, lon in by_cc[cc]:
            roman = source = None
            if gns and (hit := gns.lookup(native, lat, lon)):
                roman, source = hit, "gns-bgn"
            elif geo and (hit := geo.lookup(native, lat, lon)):
                roman, source = hit, "geonames"
            else:
                roman, source = machine_translit(native, cc, script_of_name(native))
            stats[source] += 1
            updates.append((layer, fid, roman, source))
    return updates, stats


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--gpkg", default=G)
    ap.add_argument("--cache", default=gz.CACHE)
    a = ap.parse_args()
    if not (a.apply or a.dry_run):
        ap.print_help(); return

    rows = export_rows(a.gpkg)
    print(f"gazetteer-eligible rows: {len(rows)}")
    updates, stats = resolve(rows, a.cache, print)
    print("\nresolved name_source distribution:")
    for k, v in stats.most_common():
        print(f"  {k:20} {v}")
    upgraded = stats["gns-bgn"] + stats["geonames"]
    print(f"upgraded to a gazetteer source: {upgraded}/{len(rows)} "
          f"({100*upgraded/len(rows):.1f}%)" if rows else "no rows")

    if a.dry_run:
        print("\n(dry-run: no writes)")
        return

    con = sqlite3.connect(a.gpkg)
    # attribute-only updates (geometry untouched -> RTree stays valid); drop its triggers like
    # the romanize target does, to avoid touching them.
    for t in ("insert", "update", "update1", "update2", "update3", "update4",
              "update5", "update6", "update7", "delete"):
        for layer in ("places", "admin_levels"):
            con.execute(f"DROP TRIGGER IF EXISTS rtree_{layer}_geom_{t}")
    for layer in ("places", "admin_levels"):
        batch = [(roman, source, fid) for lyr, fid, roman, source in updates if lyr == layer]
        con.executemany(f"UPDATE {layer} SET name=?, name_source=? WHERE fid=?", batch)
    con.commit()
    con.close()
    print("\nApplied.")


if __name__ == "__main__":
    main()
