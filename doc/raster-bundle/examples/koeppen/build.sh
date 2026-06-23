#!/usr/bin/env bash
#
# Reference pipeline: build an ortus raster bundle for the present-day Köppen-Geiger
# classification (Beck et al. V3). This is the per-dataset pipeline that absorbs the
# upstream chaos and emits the one canonical bundle ortus consumes.
#
# Requirements: gdal (gdalwarp, gdalinfo, gdal_translate), python3, zip, and a JSON
# Schema validator (we use `check-jsonschema`; swap for your tool of choice).
#
# What it does:
#   1. download + unzip the V3 GeoTIFFs and legend.txt
#   2. reproject to the canonical CRS (no-op for Köppen — already EPSG:4326)
#   3. write a Cloud Optimized GeoTIFF
#   4. generate ortus-raster.yaml (inline mapping) from legend.txt
#   5. VALIDATE against the schema  — the build fails here, never ortus
#   6. zip into the bundle
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCHEMA="$HERE/../../ortus-raster.schema.json"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- 0. config ---------------------------------------------------------------
# V3 distribution; pick the resolution you need (0p00833333 ~= 1 km).
SRC_URL="${KOEPPEN_URL:-https://figshare.com/ndownloader/files/12407516}"  # adjust to the V3 1km zip
CANONICAL_CRS="EPSG:4326"
OUT_BUNDLE="$HERE/koeppen-geiger-present.zip"

# The raster filename inside the upstream archive (1980-2016, ~1km). Adjust if needed.
SRC_RASTER_GLOB="*1980*2016*.tif"
SRC_LEGEND="legend.txt"

# --- 1. download + unzip -----------------------------------------------------
echo ">> downloading source"
curl -fsSL "$SRC_URL" -o "$WORK/src.zip"
unzip -q -o "$WORK/src.zip" -d "$WORK/src"

RASTER="$(find "$WORK/src" -iname "$SRC_RASTER_GLOB" | head -n1)"
LEGEND="$(find "$WORK/src" -iname "$SRC_LEGEND" | head -n1)"
[ -n "$RASTER" ] || { echo "error: source raster not found ($SRC_RASTER_GLOB)"; exit 1; }
[ -n "$LEGEND" ] || { echo "error: legend.txt not found"; exit 1; }
echo "   raster: $RASTER"
echo "   legend: $LEGEND"

# --- 2. reproject to canonical CRS (no-op if already there) ------------------
echo ">> normalizing CRS to $CANONICAL_CRS"
SRC_CRS="$(gdalinfo -json "$RASTER" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("coordinateSystem",{}).get("epsg",""))' || true)"
WARPED="$WORK/warped.tif"
if [ "$SRC_CRS" = "4326" ]; then
  cp "$RASTER" "$WARPED"
else
  # -r near: categorical data, never interpolate
  gdalwarp -t_srs "$CANONICAL_CRS" -r near -overwrite "$RASTER" "$WARPED"
fi

# --- 3. Cloud Optimized GeoTIFF ----------------------------------------------
echo ">> writing COG"
COG="$WORK/koeppen.cog.tif"
gdal_translate -of COG \
  -co COMPRESS=DEFLATE \
  -co BLOCKSIZE=512 \
  -co OVERVIEW_RESAMPLING=NEAREST \
  -co RESAMPLING=NEAREST \
  "$WARPED" "$COG"

# --- 4. generate manifest from legend.txt ------------------------------------
echo ">> generating manifest"
MANIFEST="$WORK/ortus-raster.yaml"
python3 "$HERE/gen_manifest.py" "$LEGEND" > "$MANIFEST"

# --- 5. validate (fail the build here) ---------------------------------------
echo ">> validating manifest against schema"
check-jsonschema --schemafile "$SCHEMA" "$MANIFEST"

# --- 6. zip the bundle -------------------------------------------------------
echo ">> packaging bundle"
STAGE="$WORK/bundle"
mkdir -p "$STAGE"
cp "$MANIFEST" "$STAGE/ortus-raster.yaml"
cp "$COG"      "$STAGE/koeppen.cog.tif"
( cd "$STAGE" && zip -q -r "$OUT_BUNDLE" . )

echo ">> done: $OUT_BUNDLE"
echo "   drop this into the ortus storage path / bucket."
