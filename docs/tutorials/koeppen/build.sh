#!/usr/bin/env bash
#
# Reference pipeline: build an ortus raster bundle for the Köppen-Geiger climate
# classification over the 1980–2016 reference period (Beck et al. 2018, V1). This is
# the per-dataset pipeline that absorbs the upstream chaos and emits the one canonical
# bundle ortus consumes. (For V3 / 1991–2020, change SRC_URL + SRC_ID/SRC_NAME below;
# the steps are identical.)
#
# Requirements: gdal (gdalwarp, gdalinfo, gdal_translate), python3, zip. A JSON-Schema
# validator is optional — step 5 uses check-jsonschema if present, else python
# jsonschema+PyYAML, else skips (ortus validates against the same schema at ingest).
#
# What it does:
#   1. download + unzip the GeoTIFF + legend.txt
#   2. reproject to the canonical CRS (no-op for Köppen — already EPSG:4326)
#   3. write a Cloud Optimized GeoTIFF (LZW)
#   4. generate ortus-raster.yaml (inline mapping) from legend.txt
#   5. validate against the schema if a validator is available
#   6. zip into the bundle
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCHEMA="$HERE/../../ortus-raster.schema.json"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- 0. config ---------------------------------------------------------------
# Beck et al. (2018) V1 archive (~68 MB) — contains the present-day 1 km map and
# legend.txt. (For V3, use https://figshare.com/ndownloader/files/61012822, a much
# larger multi-period/scenario archive; the build steps below are identical.)
SRC_URL="${KOEPPEN_URL:-https://ndownloader.figshare.com/files/12407516}"
CANONICAL_CRS="EPSG:4326"

# Identity — encode the reference PERIOD, never "present"/"latest" (a Köppen
# "present-day" map is a classification over a fixed period). Beck 2018 V1 =
# 1980–2016; V3 = 1991–2020. The id MUST equal the bundle filename stem.
SRC_ID="koeppen-geiger-1980-2016"
SRC_NAME="Köppen-Geiger climate classification 1980–2016 (Beck et al. 2018, V1)"
SRC_DESC="Köppen-Geiger climate classification at ~1 km, computed over the 1980–2016 reference period (Beck et al. 2018, V1). 'Present-day' here means this fixed reference period, not the current year."
OUT_BUNDLE="$HERE/$SRC_ID.zip"

# Present-day map at 0.0083° (~1 km) inside the archive.
SRC_RASTER_GLOB="*present_0p0083.tif"
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
SRC_CRS="$(gdalinfo -json "$RASTER" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("coordinateSystem",{}).get("epsg") or "")' || true)"
WARPED="$WORK/warped.tif"
if [ "$SRC_CRS" = "4326" ] || [ -z "$SRC_CRS" ]; then
  # Already geographic WGS84 — no reprojection needed. The Köppen source is
  # WGS84 but carries no explicit EPSG code, so we stamp EPSG:4326 on the COG
  # step below (-a_srs) rather than reproject.
  cp "$RASTER" "$WARPED"
else
  # Different projection (e.g. ESDAC EPSG:3035): reproject. -r near because the
  # data is categorical and must never be interpolated.
  gdalwarp -t_srs "$CANONICAL_CRS" -r near -overwrite "$RASTER" "$WARPED"
fi

# --- 3. Cloud Optimized GeoTIFF ----------------------------------------------
echo ">> writing COG"
COG="$WORK/koeppen.cog.tif"
# LZW (not DEFLATE) is mandated for bundle COGs: the Go reader (tingold/gocog,
# see docs/explanation/decisions/0013) reads LZW/uncompressed tiles correctly but trips over GDAL's
# DEFLATE tiles. LZW keeps the COG compressed and lossless.
gdal_translate -of COG \
  -a_srs "$CANONICAL_CRS" \
  -co COMPRESS=LZW \
  -co BLOCKSIZE=512 \
  -co OVERVIEW_RESAMPLING=NEAREST \
  -co RESAMPLING=NEAREST \
  "$WARPED" "$COG"

# --- 4. generate manifest from legend.txt ------------------------------------
echo ">> generating manifest"
MANIFEST="$WORK/ortus-raster.yaml"
python3 "$HERE/gen_manifest.py" "$LEGEND" \
  --id "$SRC_ID" --name "$SRC_NAME" --description "$SRC_DESC" > "$MANIFEST"

# --- 5. validate (fail the build here) ---------------------------------------
# Pre-validate against the schema so a bad manifest fails the build, not ortus.
# If no validator is installed, skip — ortus validates against the same embedded
# schema at ingest time anyway.
if command -v check-jsonschema >/dev/null 2>&1; then
  echo ">> validating manifest against schema (check-jsonschema)"
  check-jsonschema --schemafile "$SCHEMA" "$MANIFEST"
elif command -v python3 >/dev/null 2>&1 && python3 -c 'import jsonschema, yaml' 2>/dev/null; then
  echo ">> validating manifest against schema (python jsonschema)"
  python3 - "$SCHEMA" "$MANIFEST" <<'PY'
import json, sys, yaml
from jsonschema import Draft202012Validator
schema = json.load(open(sys.argv[1]))
doc = yaml.safe_load(open(sys.argv[2]))
for layer in doc.get("layers", []):
    if "mapping" in layer:
        layer["mapping"] = {str(k): v for k, v in layer["mapping"].items()}
Draft202012Validator(schema).validate(doc)
print("   manifest is valid")
PY
else
  echo ">> (no JSON-Schema validator found; skipping — ortus validates at ingest)"
fi

# --- 6. zip the bundle -------------------------------------------------------
echo ">> packaging bundle"
STAGE="$WORK/bundle"
mkdir -p "$STAGE"
cp "$MANIFEST" "$STAGE/ortus-raster.yaml"
cp "$COG"      "$STAGE/koeppen.cog.tif"
( cd "$STAGE" && zip -q -r "$OUT_BUNDLE" . )

echo ">> done: $OUT_BUNDLE"
echo "   drop this into the ortus storage path / bucket."
