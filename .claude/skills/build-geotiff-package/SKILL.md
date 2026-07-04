---
name: build-geotiff-package
description: >-
  Build an ortus raster package — a .zip bundle containing an ortus-raster.yaml
  manifest plus one or more Cloud Optimized GeoTIFFs (LZW) and a pixel-value→
  properties mapping. Use when packaging categorical/coded raster data (climate
  classes, land cover, soil classes, any GeoTIFF whose integer pixel values map to
  attributes) into a source ortus serves on the query path. Covers CRS
  normalization, COG conversion (LZW — DEFLATE is unsupported), manifest schema,
  value mapping, bundle layout, source-ID rules, and validation. Not for vector
  GeoPackages (use build-ortus-package) or the gazetteer (use build-gazetteer-package).
---

# Build a GeoTIFF (raster) package for ortus

An ortus raster source is a **`.zip` bundle** with a fixed-name manifest
(`ortus-raster.yaml`) at the archive root, one or more Cloud Optimized GeoTIFFs,
and a mapping from integer pixel values to feature properties. ortus samples the
pixel under a query coordinate (nearest-neighbour) and returns the mapped properties.

Adapter: `internal/adapters/raster/repository.go`. Manifest validator:
`internal/adapters/raster/manifest.go`. Schema (authoritative):
`reference/ortus-raster.schema.json`. Full spec: `docs/reference/raster-bundle.md`.
Worked example: `docs/tutorials/koeppen/`.

## When to use / not use

- **Use** for categorical rasters whose integer pixel values decode to attributes
  (Köppen climate class, CORINE land cover, soil class, hazard zone, …).
- **Not** for continuous rasters that need interpolation (only nearest-neighbour
  sampling is supported), vector data (`build-ortus-package`), or the gazetteer.

## Bundle layout

```
<id>.zip
├── ortus-raster.yaml       # REQUIRED, fixed name, at ZIP root
├── <layer>.cog.tif         # Cloud Optimized GeoTIFF (LZW), one per layer
└── mapping.json            # OPTIONAL sidecar for large value→properties tables
```

- `ortus-raster.yaml` MUST be named exactly that and live at the root (not a subdir).
- COG paths in the manifest are relative (no leading `/`, no `..`).
- The **ZIP filename stem MUST equal the manifest `id`** (collision-prevention;
  the id is also the ortus source ID and must be globally unique).

## Manifest contract (`ortus-raster.yaml`)

```yaml
schema_version: 1                              # REQUIRED (const 1)
id: koeppen-geiger-1980-2016                    # REQUIRED, kebab-case, == zip stem; encode the reference PERIOD, never "present"/"latest"
name: "Köppen-Geiger climate classification 1980–2016 (Beck et al. 2018, V1)"  # REQUIRED
description: "…"                                # optional
license:                                        # REQUIRED
  name: "CC-BY-4.0"                             # REQUIRED (SPDX id or name)
  attribution: "Beck et al. (2018), Scientific Data 5:180214"   # optional but expected for CC-BY
  url: "https://www.gloh2o.org/koppen/"         # optional
crs: EPSG:4326                                  # REQUIRED (EPSG:<code>); all COGs must already be in this CRS — ortus does NOT reproject
layers:                                         # REQUIRED, >=1
  - id: classification                          # REQUIRED, kebab-case, unique in bundle
    file: koeppen.cog.tif                        # REQUIRED, relative path to the COG
    band: 1                                     # optional, 1-based (default 1)
    nodata: 0                                   # optional, pixel value treated as no-match
    sampling: nearest                           # optional, only "nearest" supported
    mapping:                                    # EXACTLY ONE OF mapping | value_mapping
      "1": { code: "Af", description: "Tropical, rainforest", group: "Tropical" }
      "2": { code: "Am", description: "Tropical, monsoon",    group: "Tropical" }
      # …integer keys as strings; values are FLAT objects of scalars only
```

For large tables use a sidecar instead of inline `mapping`:

```yaml
    value_mapping: mapping.json     # { "1": {code:"Af",…}, "2": {…}, … }
```

**Mapping rules:**
- Keys are integer pixel values **as strings**, pattern `^-?(0|[1-9][0-9]*)$`
  (`"0"`, `"255"`, `"-1"` valid; `"01"`, `"1.5"` invalid).
- Values are **flat objects of scalars** (string/number/boolean/null) — no nesting.
- A pixel value with no mapping entry is a **hard query error** — map every value
  that occurs in the raster (or set it as `nodata`).
- **YAML "Norway problem":** quote string codes — bare `NO`, `no`, `yes`, `on`
  parse as booleans. Write `"NO"`.

## Build steps

Requirements: `gdal` (gdalinfo, gdalwarp, gdal_translate), `python3`, `zip`.
Adapt the bundled `scripts/build-raster-bundle.sh`; the steps are:

```bash
# 1. Inspect source CRS.
gdalinfo -json source.tif | python3 -c 'import sys,json;print(json.load(sys.stdin).get("coordinateSystem",{}).get("epsg"))'

# 2. Reproject to the canonical CRS IF needed. -r near ALWAYS for categorical data
#    (never interpolate class codes). No-op if already in the target CRS.
gdalwarp -t_srs EPSG:4326 -r near -overwrite source.tif warped.tif

# 3. Write a Cloud Optimized GeoTIFF with LZW.
#    LZW is MANDATORY: the Go reader (tingold/gocog) reads LZW/uncompressed tiles
#    but trips over GDAL's DEFLATE tiles. See docs/explanation/decisions/0013.
gdal_translate -of COG -a_srs EPSG:4326 \
  -co COMPRESS=LZW -co BLOCKSIZE=512 \
  -co OVERVIEW_RESAMPLING=NEAREST -co RESAMPLING=NEAREST \
  warped.tif layer.cog.tif

# 4. Author ortus-raster.yaml (inline mapping or value_mapping sidecar).
#    scripts/gen_manifest.py builds it from a legend.txt (value<TAB>label lines).

# 5. Validate against the schema BEFORE shipping (fail here, not at ortus ingest):
check-jsonschema --schemafile reference/ortus-raster.schema.json ortus-raster.yaml
#    (or python jsonschema + PyYAML; ortus validates against the same schema at load)

# 6. Zip with the manifest at the root.
zip -r <id>.zip ortus-raster.yaml layer.cog.tif [mapping.json]
```

Bundled helpers:
- `scripts/build-raster-bundle.sh` — generalized end-to-end pipeline (steps 1–6).
- `scripts/gen_manifest.py` — generate the manifest (inline mapping) from a
  `legend.txt`; from the Köppen tutorial, reusable for any coded raster.

## COG requirements (recap)

- **Compression: LZW** (or none). **Not DEFLATE.**
- Internal tiling + overviews, header-first (what `-of COG` produces).
- One CRS, matching the manifest `crs` — ortus does not reproject at query time.
- `band` (1-based) must be ≤ the COG's band count.

## How ortus loads & queries it

On load, ortus unzips to a temp dir, validates the manifest against the embedded
schema, checks `id` == zip stem, parses `crs` → SRID, opens each COG, and resolves
its mapping. Raster sources are **Ready immediately** (no index-build step). At
query time it transforms the WGS84 point into the layer CRS, samples the pixel
(nearest), applies `nodata`, and returns the mapped properties — or no match if the
point is outside the raster / on a nodata pixel.

## Placement & config

Same storage/config as any source (no raster-specific config):

```yaml
storage:
  type: local
  local_path: ./data
```

```bash
cp koeppen-geiger-1980-2016.zip ./data/    # watcher hot-loads local files (~500ms)
```

Query: `GET /api/v1/query?lon=..&lat=..` (all sources) or `/query/{id}`.

## Verify before shipping

1. `gdalinfo layer.cog.tif` — CRS matches manifest, `LAYOUT=COG`, compression LZW.
2. Schema-validate `ortus-raster.yaml` (step 5) — must pass.
3. Confirm every pixel value in the raster has a mapping entry (or is `nodata`):
   `gdalinfo -hist layer.cog.tif` lists occurring values; cross-check against the mapping keys.
4. Load it in ortus: `GET /api/v1/sources` shows the source ready; query a known
   point and confirm the decoded properties; query outside the extent → no match.
