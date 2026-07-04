#!/usr/bin/env bash
#
# Build an ortus raster bundle from a single categorical GeoTIFF: normalize CRS,
# write a Cloud Optimized GeoTIFF (LZW), generate + validate ortus-raster.yaml, and
# zip <id>.zip ready to drop into the ortus storage path.
#
# Generalized from docs/tutorials/koeppen/build.sh. For multi-layer bundles, run the
# COG step per layer and author the manifest by hand (see the skill's SKILL.md).
#
# Requirements: gdal (gdalinfo, gdalwarp, gdal_translate), python3, zip. A JSON-Schema
# validator is optional (check-jsonschema, or python jsonschema+PyYAML) — ortus
# validates against the same embedded schema at ingest.
#
# Usage:
#   build-raster-bundle.sh \
#     --src source.tif --legend legend.txt \
#     --id koeppen-geiger-1980-2016 \
#     --name "Köppen-Geiger 1980–2016" \
#     [--desc "…"] [--crs EPSG:4326] [--band 1] [--nodata 0] \
#     [--out DIR]
#
#   legend.txt: one "value<whitespace>label" per line (fed to gen_manifest.py).
#   --id MUST equal the output bundle's filename stem (ortus enforces this).
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCHEMA="$HERE/../reference/ortus-raster.schema.json"

SRC="" LEGEND="" ID="" NAME="" DESC="" CRS="EPSG:4326" BAND="1" NODATA="" OUTDIR="."
while [ "$#" -gt 0 ]; do
  case "$1" in
    --src)    SRC="$2"; shift 2 ;;
    --legend) LEGEND="$2"; shift 2 ;;
    --id)     ID="$2"; shift 2 ;;
    --name)   NAME="$2"; shift 2 ;;
    --desc)   DESC="$2"; shift 2 ;;
    --crs)    CRS="$2"; shift 2 ;;
    --band)   BAND="$2"; shift 2 ;;
    --nodata) NODATA="$2"; shift 2 ;;
    --out)    OUTDIR="$2"; shift 2 ;;
    -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1"; exit 1 ;;
  esac
done

[ -n "$SRC" ] && [ -n "$ID" ] && [ -n "$NAME" ] || { echo "error: --src, --id, --name required"; exit 1; }
[ -e "$SRC" ] || { echo "error: source not found: $SRC"; exit 1; }
command -v gdal_translate >/dev/null || { echo "error: GDAL not found"; exit 1; }

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
OUT_BUNDLE="$OUTDIR/$ID.zip"
COG_NAME="${ID}.cog.tif"

# --- 1. reproject to canonical CRS (no-op if already there) -------------------
echo ">> normalizing CRS to $CRS"
SRC_EPSG="$(gdalinfo -json "$SRC" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("coordinateSystem",{}).get("epsg") or "")' || true)"
WARPED="$WORK/warped.tif"
WANT_EPSG="${CRS#EPSG:}"
if [ "$SRC_EPSG" = "$WANT_EPSG" ] || [ -z "$SRC_EPSG" ]; then
  cp "$SRC" "$WARPED"           # already canonical (or unstamped — -a_srs stamps it below)
else
  gdalwarp -t_srs "$CRS" -r near -overwrite "$SRC" "$WARPED"   # -r near: never interpolate class codes
fi

# --- 2. Cloud Optimized GeoTIFF (LZW — DEFLATE is unsupported by the Go reader) --
echo ">> writing COG ($COG_NAME)"
COG="$WORK/$COG_NAME"
gdal_translate -of COG -a_srs "$CRS" \
  -co COMPRESS=LZW -co BLOCKSIZE=512 \
  -co OVERVIEW_RESAMPLING=NEAREST -co RESAMPLING=NEAREST \
  "$WARPED" "$COG"

# --- 3. manifest --------------------------------------------------------------
echo ">> generating ortus-raster.yaml"
MANIFEST="$WORK/ortus-raster.yaml"
gen_args=(--id "$ID" --name "$NAME")
[ -n "$DESC" ] && gen_args+=(--description "$DESC")
if [ -n "$LEGEND" ] && [ -e "$LEGEND" ]; then
  python3 "$HERE/gen_manifest.py" "$LEGEND" "${gen_args[@]}" > "$MANIFEST"
else
  echo "   no --legend: writing a manifest stub — fill in layers[].mapping before shipping"
  {
    echo "schema_version: 1"
    echo "id: $ID"
    echo "name: \"$NAME\""
    [ -n "$DESC" ] && echo "description: \"$DESC\""
    echo "license:"
    echo "  name: \"REPLACE-ME\""
    echo "crs: $CRS"
    echo "layers:"
    echo "  - id: ${ID}"
    echo "    file: $COG_NAME"
    echo "    band: $BAND"
    [ -n "$NODATA" ] && echo "    nodata: $NODATA"
    echo "    sampling: nearest"
    echo "    mapping:"
    echo "      \"0\": { code: \"REPLACE-ME\" }"
  } > "$MANIFEST"
fi

# --- 4. validate against the schema (fail here, not at ortus ingest) ----------
if command -v check-jsonschema >/dev/null 2>&1; then
  echo ">> validating manifest (check-jsonschema)"
  check-jsonschema --schemafile "$SCHEMA" "$MANIFEST"
elif python3 -c 'import jsonschema, yaml' 2>/dev/null; then
  echo ">> validating manifest (python jsonschema)"
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

# --- 5. package ---------------------------------------------------------------
echo ">> packaging bundle"
STAGE="$WORK/bundle"; mkdir -p "$STAGE"
cp "$MANIFEST" "$STAGE/ortus-raster.yaml"
cp "$COG"      "$STAGE/$COG_NAME"
mkdir -p "$OUTDIR"
rm -f "$OUT_BUNDLE"
( cd "$STAGE" && zip -q -r "$OUT_BUNDLE" . )

echo ">> done: $OUT_BUNDLE"
echo "   source id: $ID (must equal the bundle stem; unique in the storage path)"
echo "   drop it into the ortus storage path / bucket."
