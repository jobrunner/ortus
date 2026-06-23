# Ortus Raster Bundle

A **raster bundle** is the canonical, self-describing container that lets ortus serve
point queries against GeoTIFF data the same way it serves GeoPackages: *coordinate in →
Sachdaten + license out*.

GeoPackage is convenient because it is a container with a manifest (`gpkg_contents`).
GeoTIFF has no such thing — datasets ship as one file or many, with legend files named
differently every time, in varying CRS. A raster bundle gives raster the missing
container + manifest, so **everything messy is solved upstream in the per-dataset
pipeline** and ortus only ever sees one well-defined shape.

## What you drop into storage

A single ZIP, e.g. `koeppen-geiger-present.zip`:

```
koeppen-geiger-present.zip
├── ortus-raster.yaml      # the manifest — fixed name, ZIP root, the contract
├── koeppen.cog.tif        # normalized Cloud Optimized GeoTIFF
└── mapping.json           # OPTIONAL sidecar, only for large tables
```

The existing storage sync (`internal/application/registry.go`) already downloads remote
objects into the local cache, so a ZIP fits the current model with no streaming changes.

## The manifest

`ortus-raster.yaml` is validated against [`ortus-raster.schema.json`](./ortus-raster.schema.json)
(JSON Schema 2020-12; YAML is parsed then validated as JSON).

```yaml
schema_version: 1
id: koeppen-geiger-present          # stable source id, kebab-case, globally unique
name: Köppen-Geiger 1980–2016 (V3)
description: Present-day Köppen-Geiger classification, 1 km.
license:
  name: CC-BY-4.0
  attribution: Beck et al. (2018), Scientific Data
  url: https://www.gloh2o.org/koppen/
crs: EPSG:4326                      # canonical — pipeline already reprojected to this
layers:
  - id: present
    file: koeppen.cog.tif           # relative to ZIP root
    band: 1
    nodata: 0
    sampling: nearest               # categorical → never interpolate
    mapping:                        # inline; integer pixel value → properties
      1: { code: "Af", description: "Tropical, rainforest", group: "Tropical" }
      2: { code: "Am", description: "Tropical, monsoon",    group: "Tropical" }
      # …
```

### Field reference

| Field | Required | Notes |
|---|---|---|
| `schema_version` | yes | `1`. |
| `id` | yes | Kebab-case, becomes the ortus source id, must be unique across bundles. |
| `name` | yes | Human-readable. |
| `description` | no | Free text. |
| `license.name` | yes | SPDX id or license name. |
| `license.attribution` / `.url` | no | Carried into every `QueryResult`. |
| `crs` | yes | `EPSG:<n>`. All rasters in the bundle are already in this CRS. |
| `layers[]` | yes (≥1) | One COG per layer (time slice / scenario / pre-mosaicked tiles). |
| `layers[].id` | yes | Kebab-case, unique within the bundle. |
| `layers[].file` | yes | Relative `.tif`/`.tiff`, no leading slash, no `..`. |
| `layers[].band` | no (def. 1) | 1-based band index. |
| `layers[].nodata` | no | Sample == nodata → no match (not an error). |
| `layers[].sampling` | no (def. `nearest`) | `nearest` for categorical; `bilinear` only for continuous data. |
| `layers[].mapping` **xor** `layers[].value_mapping` | yes | Exactly one. Inline table, or sidecar pointer. |

### The value → attribute mapping

The pixel value is an integer; the mapping turns it into `Feature.Properties`:

- **Keys** are integer pixel values. In YAML/JSON object keys are strings, so the schema
  requires them to match `^-?(0|[1-9][0-9]*)$`. The ingest validator casts them to int.
- **Values** are flat objects of **scalars only** (string/number/bool/null). The object
  is copied verbatim into `Feature.Properties`.
- A sampled value with **no mapping entry** is a hard ingest/query error surfaced clearly
  — it means the legend and the raster disagree, which you want to catch, not hide.

**Inline (`mapping`)** for small categorical tables (Köppen ~30 classes, most ESDAC
attributes). **Sidecar (`value_mapping: mapping.json`)** only when the table is large or
machine-generated. Both are UTF-8 by spec — no CSV, no encoding ambiguity, no delimiter
guessing.

> **YAML "Norway problem":** unquoted `no`, `yes`, `on`, `NO` parse as booleans, and
> `1.0`/leading-zero tokens as numbers. Always **quote string codes** (`"Af"`, `"NO"`) in
> the pipeline output. The validator additionally re-casts mapping keys to integers.

## Ingest contract (why it is "fehlerfrei")

Registration validates first and is all-or-nothing. A bundle is either fully live or not
registered at all — never half-loaded:

1. Detect `*.zip` in storage → treat as raster bundle.
2. Unzip into the local cache.
3. Read `ortus-raster.yaml`; **validate against the schema**; reject on any extra/unknown
   field (`additionalProperties: false`), missing required field, or both/neither of
   `mapping`/`value_mapping`.
4. Verify every referenced `file` exists, is a valid COG, is in the declared `crs`, and
   that the requested `band` exists; load each `value_mapping` sidecar.
5. Enforce: unique layer ids; integer mapping keys; sampling=`nearest` for categorical.
6. Any failure → reject with an explicit, actionable error; the source stays unregistered.

## Query contract

Sample `band` at the query coordinate using `sampling`, get the integer value, look it up
in the mapping → `Properties`. Attach `license`. The result is the **same `QueryResult`
shape** as a GeoPackage, so vector and raster sources merge into one response for a point.

## Pipeline (per-dataset repo)

Identical division of labour to the GeoPackage pipelines. The pipeline absorbs the chaos
and always emits the canonical bundle:

```
raw GeoTIFF(s) + legend.txt (named anything, any CRS)
  → gdalwarp        reproject to canonical CRS (no-op if already there)
  → gdal_translate  / rio cogeo → Cloud Optimized GeoTIFF
  → parse legend    → emit mapping (inline YAML or JSON sidecar), UTF-8, codes quoted
  → write ortus-raster.yaml
  → validate against the schema   ← fail the build here, never at ortus
  → zip
```

See [`examples/koeppen/`](./examples/koeppen/) for a runnable reference implementation.

## What COG does and does not give you

COG is a normal GeoTIFF with internal tiling + overviews + header-first layout, enabling
partial reads (HTTP range requests) and efficient local random access (read only the tile
containing the pixel). COG standardizes **access**, not **meaning** — it does not define
what value `7` means, how many files a dataset has, or licensing. That is exactly what the
bundle manifest adds on top.

**Compression: use `LZW`.** Bundle COGs must be written with `COMPRESS=LZW` (or none).
The Go reader ortus uses (`tingold/gocog`, see [ADR-0013](../adr/0013-cog-reader-library.md))
reads LZW/uncompressed tiles correctly but currently fails on GDAL's `DEFLATE` tiles. LZW
stays lossless and compressed.
