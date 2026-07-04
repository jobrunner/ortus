#!/usr/bin/env bash
# Full reproducible rebuild of output/osm-admin-places.gpkg from FRESH sources.
#
# NOTE: this is a bundled reference copy. It drives the osm-data Makefile
# (`make`, `make normalize-schema`, …), so it MUST be run from an osm-data
# checkout (copy it to `$OSM_DATA/scripts/` and run it there). Running it from
# this skill directory will fail — there is no Makefile here.
#
# Pulls every OSM extract at the current "latest" (one consistent vintage),
# builds the base, adds the extra countries, normalizes the schema, embeds the
# license metadata, records provenance (timestamps + SHA-256) and runs the QA
# harness. Run from the repo root:  bash scripts/rebuild-all.sh
#
# NOTE: close the GeoPackage in QGIS / any GIS first — an open reader blocks the
# SQLite writers (especially on external/network drives).
set -u
cd "$(dirname "$0")/.."

LOG() { printf '\n========== %s | %s ==========\n' "$(date -u +%H:%M:%S)" "$*"; }

# Extra countries to add() after the base build, by Geofabrik region.
# (europe-latest + morocco/algeria/tunisia are handled by the Makefile itself.)
EUROPE_EXTRAS="turkey"
AFRICA_EXTRAS="egypt libya canary-islands"
ASIA_EXTRAS="gcc-states iraq israel-and-palestine jordan"

LOG "0. Back up current GeoPackage (safety net before clean)"
if [ -f output/osm-admin-places.gpkg ]; then
  cp -f output/osm-admin-places.gpkg ./osm-admin-places.gpkg.prev && echo "backed up -> ./osm-admin-places.gpkg.prev"
fi

LOG "1. Download europe-latest (root prerequisite, large)"
# Always fetch fresh — do NOT use curl -C - here: resuming onto a stale/differently
# sized europe-latest.osm.pbf corrupts the file (appends new bytes to old content).
rm -f europe-latest.osm.pbf
curl -fL --retry 3 -o europe-latest.osm.pbf https://download.geofabrik.de/europe-latest.osm.pbf
sz=$(stat -f%z europe-latest.osm.pbf 2>/dev/null || stat -c%s europe-latest.osm.pbf 2>/dev/null || echo 0)
if ! osmium fileinfo europe-latest.osm.pbf >/dev/null 2>&1 || [ "${sz:-0}" -lt 3000000000 ]; then
  echo "FATAL: europe pbf invalid or too small (size=${sz})"; exit 1
fi
echo "europe-latest OK ($((sz/1024/1024)) MB)"

LOG "2. make clean (remove old output + temp)"
make clean

LOG "3. Base build (europe + morocco/algeria/tunisia + Natural Earth) — long"
make || { echo "FATAL: base build (make) failed — aborting before add-country"; exit 1; }

add_country() { # $1=region  $2=name
  local f="temp/$2-latest.osm.pbf"
  echo "--- add $2 ($1) ---"
  curl -fL --retry 3 -o "$f" "https://download.geofabrik.de/$1/$2-latest.osm.pbf" \
    || { echo "WARN: download $2 failed, skipping"; return; }
  if ! osmium fileinfo "$f" >/dev/null 2>&1; then
    echo "WARN: $2 is not a valid PBF (got $(file -b "$f")), skipping"; return
  fi
  make add-country PBF="$f" || echo "WARN: add-country $2 failed"
}

LOG "4. Add extra countries"
for c in $EUROPE_EXTRAS; do add_country europe "$c"; done
for c in $AFRICA_EXTRAS; do add_country africa "$c"; done
for c in $ASIA_EXTRAS;   do add_country asia   "$c"; done

LOG "5. normalize-schema (unify attributes + gapless coverage)"
make normalize-schema || { echo "FATAL: normalize-schema failed"; exit 1; }

LOG "6. link-hierarchy (place<->admin FK chain; drop redundant place country names)"
make link-hierarchy || { echo "FATAL: link-hierarchy failed"; exit 1; }

LOG "7. srid-metadata (native SpatiaLite spatial_ref_sys for consumers)"
make srid-metadata || { echo "FATAL: srid-metadata failed"; exit 1; }

LOG "8. romanize (non-Latin names -> Latin name + name_native + name_source)"
make romanize || { echo "FATAL: romanize failed"; exit 1; }

LOG "9. romanize-gazetteers (upgrade machine Arabic/Hebrew names with GNS + GeoNames; network)"
make romanize-gazetteers || { echo "FATAL: romanize-gazetteers failed"; exit 1; }

LOG "10. metadata (embed license + attribution)"
make metadata

LOG "11. provenance (record timestamps + SHA-256)"
make provenance

LOG "12. verify (QA harness)"
make verify

LOG "REBUILD COMPLETE"
