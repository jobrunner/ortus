---
name: build-ortus-package
description: >-
  Build a "normal" ortus vector data package — a single GeoPackage (.gpkg) source
  that ortus discovers, indexes, and serves on the generic point-in-polygon query
  path (GET /api/v1/query). Use when packaging vector data (administrative areas,
  soil types, climate zones, any polygon/point/line layers) into a source ortus can
  load. Covers the layer/column/SRID/CRS contract, spatial indexing, source-ID
  rules, licensing metadata, and placement/config so the package loads cleanly.
  Not for the gazetteer dataset (use build-gazetteer-package) or rasters (use
  build-geotiff-package).
---

# Build a normal (vector) ortus package

A normal ortus source is a **single OGC GeoPackage file** (`.gpkg`) dropped into the
storage path. ortus discovers it by extension, opens it read-write once to build
R-tree spatial indexes, then serves point queries against its layers. There is **no
per-source config or manifest** — the GeoPackage's own metadata tables are the contract.

Adapter: `internal/adapters/geopackage/repository.go`. Registry:
`internal/application/registry.go`. Supported extensions:
`internal/domain/sourceid.go` (`.gpkg` vector, `.zip` raster).

## When to use / not use

- **Use** to turn vector data (polygons/points/lines) into a queryable ortus source:
  reproject to a supported CRS, load into a GeoPackage, add license metadata, name
  the file correctly, drop it in the storage path.
- **Not** for the gazetteer (fixed dual-layer schema + sidecars → `build-gazetteer-package`)
  or rasters/GeoTIFF (→ `build-geotiff-package`).

## The contract ortus expects

A valid **OGC GeoPackage** with the standard metadata tables:
- `gpkg_contents` — layer registry (`table_name`, `description`, bbox, `data_type='features'`)
- `gpkg_geometry_columns` — `column_name`, `geometry_type_name`, `srs_id`
- `gpkg_spatial_ref_sys` — CRS definitions

ortus reads layers via `gpkg_contents JOIN gpkg_geometry_columns WHERE data_type='features'`.
Each layer contributes: name, description, geometry column, geometry type
(`POINT | LINESTRING | POLYGON | MULTIPOINT | MULTILINESTRING | MULTIPOLYGON`), SRID, bbox.

**CRS / SRID.** Queries arrive in WGS84 (EPSG:4326); the query service transforms the
point into each layer's SRID before matching, so a layer may be stored in any SRID
that `gpkg_spatial_ref_sys` defines. Prefer EPSG:4326 for simplicity. Every geometry
column must declare a valid `srs_id`.

**Spatial index.** ortus creates `rtree_<layer>_<geomcol>` at load if absent (it opens
the file read-write for this one-time step; data is never modified). You can pre-build
the index at packaging time so load is instant and read-only mounts work — GDAL's
`-lco SPATIAL_INDEX=YES` does this. A source is **Ready** only when every layer has an index.

**Geometry encoding** is standard GeoPackage binary; ortus reads it via SpatiaLite
`CastAutomagic`. No special encoding needed — anything `ogr2ogr -f GPKG` produces works.

## Build steps

Assuming `gdal` (ogr2ogr, ogrinfo) is installed. Generic recipe:

```bash
# 1. Reproject / convert source vector into a GeoPackage layer with a spatial index.
#    -nln sets the layer (table) name; repeat -update -append for more layers.
ogr2ogr -f GPKG my-dataset.gpkg source.shp \
  -t_srs EPSG:4326 \
  -nln regions \
  -lco SPATIAL_INDEX=YES

# add a second layer into the same file
ogr2ogr -f GPKG -update -append my-dataset.gpkg other.geojson \
  -t_srs EPSG:4326 -nln points -lco SPATIAL_INDEX=YES

# 2. Inspect what ortus will see.
ogrinfo -so my-dataset.gpkg                 # list layers
ogrinfo -so my-dataset.gpkg regions         # geometry type, SRID, feature count, extent
```

A ready-to-adapt helper is bundled at `scripts/build-geopackage.sh`.

**Keep query results lean.** Every non-geometry column is returned as a feature
property on a match, so drop columns you don't want exposed (`ogr2ogr -select ...`).
Geometry is only returned when `query.with_geometry` is on.

## Source ID and file naming

The **source ID is the filename stem** (`my-dataset.gpkg` → `my-dataset`) via
`domain.DeriveSourceID`. Rules:
- IDs must be **globally unique** across all files in storage. Two files that derive
  the same ID (e.g. `foo.gpkg` and `foo.zip`) collide — the second is rejected with
  `ErrSourceIDCollision`. Rename one.
- Use a stable, descriptive, kebab-case stem; it appears in `GET /api/v1/query/{sourceId}`
  and in the sources listing. Encode a reference period if the data is time-bound
  (`soil-2020`), never "latest".

## Licensing metadata (recommended)

ortus surfaces per-source license/attribution in its sources API. Embed it in the
GeoPackage's `gpkg_metadata` table so it travels with the file:

```bash
sqlite3 my-dataset.gpkg "INSERT OR REPLACE INTO gpkg_metadata
  (id, md_scope, md_standard_uri, mime_type, metadata) VALUES
  (1, 'dataset', 'http://schema.org', 'text/plain',
   'Source: … | License: CC-BY-4.0 | Attribution: …');"
```

## Placement & config

Drop the file into the storage path. Minimal `config.yaml`:

```yaml
server:
  port: 8080
storage:
  type: local          # or s3 | azure | http
  local_path: ./data   # ortus loads every .gpkg / .zip here
query:
  timeout: 30s
  max_features: 1000
  with_geometry: false
logging:
  level: info
  format: json
```

```bash
cp my-dataset.gpkg ./data/       # local: the watcher hot-loads it within ~500ms
```

- **Local storage** has a directory watcher (`internal/adapters/watcher`): create/modify
  reloads the source, delete unloads it. Remote storage (S3/Azure/HTTP) loads on
  startup and on the `sync.interval` schedule (`sync.enabled: true`).
- Run: `make build && ./ortus --config config.yaml` (or `--storage-path ./data`).
- Config also via flags (`--port`, `--storage-path`) and `ORTUS_`-prefixed env vars
  (`ORTUS_QUERY_TIMEOUT=60s`). See `internal/config/config.go` and `docs/reference/configuration.md`.

## Verify before shipping

1. `ogrinfo -so my-dataset.gpkg <layer>` — geometry type + SRID as intended, feature
   count non-zero, extent sane.
2. Confirm the rtree table exists: `ogrinfo my-dataset.gpkg -sql "SELECT name FROM sqlite_master WHERE name LIKE 'rtree_%'"`.
3. Load it: start ortus pointing at the storage path, check `GET /api/v1/sources`
   lists the source as ready, then `GET /api/v1/query?lon=..&lat=..` (or
   `/query/{sourceId}`) returns the expected feature properties for a known point.
4. `GET /health/ready` is 200 once the initial load pass completes.
