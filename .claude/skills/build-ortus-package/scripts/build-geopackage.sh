#!/usr/bin/env bash
#
# Build a normal ortus vector package: one GeoPackage (.gpkg) with spatially
# indexed layers in a supported CRS, ready to drop into the ortus storage path.
#
# ortus discovers the file by extension, derives the source ID from the filename
# stem (must be globally unique), and serves point-in-polygon queries against its
# layers. There is no per-source manifest — the GeoPackage metadata IS the contract.
#
# Requirements: gdal (ogr2ogr, ogrinfo). sqlite3 optional (license metadata).
#
# Usage:
#   build-geopackage.sh -o soil-2020.gpkg -t EPSG:4326 \
#       regions=source.shp points=other.geojson
#
#   Each positional arg is <layername>=<source>. The first becomes the primary
#   layer; the rest are appended into the same GeoPackage. -o's stem is the ortus
#   source ID — keep it stable, kebab-case, and time-stamped if the data is dated.
#
set -euo pipefail

OUT=""
TSRS="EPSG:4326"          # target CRS; ortus transforms WGS84 queries into it
SELECT=""                 # optional: comma-separated columns to keep (drop the rest)

usage() { sed -n '2,20p' "$0"; exit 1; }

while getopts "o:t:s:h" opt; do
  case "$opt" in
    o) OUT="$OPTARG" ;;
    t) TSRS="$OPTARG" ;;
    s) SELECT="$OPTARG" ;;
    h|*) usage ;;
  esac
done
shift $((OPTIND - 1))

[ -n "$OUT" ] || { echo "error: -o <output.gpkg> required"; usage; }
[ "$#" -ge 1 ] || { echo "error: at least one <layer>=<source> required"; usage; }
command -v ogr2ogr >/dev/null || { echo "error: ogr2ogr (GDAL) not found"; exit 1; }

sel_args=()
[ -n "$SELECT" ] && sel_args=(-select "$SELECT")

rm -f "$OUT"
first=1
for pair in "$@"; do
  layer="${pair%%=*}"
  src="${pair#*=}"
  [ "$layer" != "$pair" ] || { echo "error: expected <layer>=<source>, got '$pair'"; exit 1; }
  [ -e "$src" ] || { echo "error: source not found: $src"; exit 1; }

  echo ">> layer '$layer'  <-  $src  (-> $TSRS)"
  if [ "$first" = 1 ]; then
    ogr2ogr -f GPKG "$OUT" "$src" \
      -t_srs "$TSRS" -nln "$layer" -lco SPATIAL_INDEX=YES "${sel_args[@]}"
    first=0
  else
    ogr2ogr -f GPKG -update -append "$OUT" "$src" \
      -t_srs "$TSRS" -nln "$layer" -lco SPATIAL_INDEX=YES "${sel_args[@]}"
  fi
done

echo ">> layers in $OUT:"
ogrinfo -so "$OUT" | sed -n '/^[0-9]*:/p'

echo ">> rtree indexes:"
ogrinfo "$OUT" -sql "SELECT name FROM sqlite_master WHERE name LIKE 'rtree_%'" 2>/dev/null \
  | sed -n 's/^  name (String) = /   /p'

echo ">> done: $OUT"
echo "   source id will be: $(basename "${OUT%.gpkg}")   (must be unique in the storage path)"
echo "   drop it into the ortus storage path / bucket."
