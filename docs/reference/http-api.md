# HTTP API

All API endpoints are prefixed with `/api/v1`. Health endpoints live at the root.
The full OpenAPI 3.0 spec is served at `GET /openapi.json`, with Swagger UI at
`/docs` (and `/swagger`). When `server.frontend_enabled` is on, a small query
frontend is served at `GET /`.

**Error responses.** Every error (any non-2xx) uses the same envelope:

```json
{ "error": "Bad Request", "message": "coordinates required: use lon/lat or x/y" }
```

where `error` is the HTTP status text and `message` is a human-readable detail.

## Query endpoints

### Query all sources

```text
GET /api/v1/query?lon={longitude}&lat={latitude}
GET /api/v1/query?x={x}&y={y}&srid={srid}
```

Query every loaded source for features containing the coordinate.

**Parameters**

- `lon` / `lat` — WGS84 coordinates (SRID 4326)
- `x` / `y` — coordinates in the SRID given by `srid`
- `srid` — coordinate SRID (default 4326)
- `properties` — comma-separated list of properties to return

```bash
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52"
curl "http://localhost:8080/api/v1/query?x=389283&y=5819450&srid=25832"
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52&properties=name,population"
```

**Response**

```json
{
  "coordinate": { "x": 13.405, "y": 52.52, "srid": 4326 },
  "results": [
    {
      "source_id": "districts",
      "source_name": "districts.gpkg",
      "features": [
        { "id": 42, "layer": "districts", "properties": { "name": "Mitte", "population": 384172 } }
      ],
      "feature_count": 1,
      "query_time_ms": 5
    }
  ],
  "total_features": 1,
  "processing_time_ms": 12
}
```

Each result carries its source's `license` (name/url/attribution) when the
GeoPackage ships that metadata.

**Gazetteer enrichment (on by default).** When the [gazetteer feature](configuration.md)
is enabled, the query response additionally carries a `gazetteer` block with the
administrative hierarchy, bearing, elevation (when a DEM is configured), name-source
explanations and the dataset attribution — so a client gets everything to process
the result in one call. It is
the same structure as the [gazetteer endpoint](#gazetteer-endpoint) (minus
`coordinate`). Opt out per request with `?with-gazetteer=0` (or `false`/`no`/`off`)
to skip the extra spatial work; enrichment is best-effort and is omitted (never
errors the query) if it fails.

```jsonc
{
  "coordinate": { "x": 13.405, "y": 52.52, "srid": 4326 },
  "results": [ /* … as above, incl. per-source license … */ ],
  "total_features": 1,
  "processing_time_ms": 12,
  "gazetteer": {
    "admin": { "country_iso": "DE", "hierarchy": [ /* … */ ] },
    "bearing": { /* … */ },
    "elevation": { /* … or null when no DEM configured … */ },
    "sources": [ { "code": "latin-osm", "short": "…", "long": "…", "standard": "" } ],
    "license": { "name": "ODbL-1.0", "url": "…", "attribution": "© OpenStreetMap contributors …" }
  }
}
```

### Query a specific source

```text
GET /api/v1/query/{sourceId}?lon={longitude}&lat={latitude}
```

```bash
curl "http://localhost:8080/api/v1/query/districts?lon=13.405&lat=52.52"
```

## Gazetteer endpoint

Only registered when the [gazetteer feature](configuration.md) is enabled
(`gazetteer.enabled: true`); otherwise the route returns `404`.

```text
GET /api/v1/gazetteer?lon={longitude}&lat={latitude}
GET /api/v1/gazetteer?x={x}&y={y}&srid={srid}
```

Reverse-geocode a coordinate to its administrative hierarchy (`admin`), compute
a bearing to the most salient nearby place (`bearing`, e.g. "4 km E Würzburg"),
and — when a DEM is configured — report the height above sea level at the point
(`elevation`). Each part is `null` when it has no result — no admin coverage, no
anchor within reach, or no elevation configured. The dataset is WGS84; a non-4326
`srid` is rejected.

**"in X" vs "prope X".** The bearing distinguishes being *inside* a place from
being *near* it by **administrative containment**, not distance: when the query
point lies within the anchor's own admin unit, `bearing.inside` is `true` and the
label is `"in Würzburg"` (this holds even far from a large city's center node).
Near but outside → `"prope Würzburg"` (`inside: false`); otherwise a directional
`"4 km E Würzburg"`. The label prefixes follow specimen-label convention: Latin
`in` and `prope` (the established Latin locality term for "near"; abbr. *pr.*). A
client can treat `inside: true` as "the point is in the settlement" (e.g. drop the
bearing from a label — the find is *in* Würzburg).

**Response**

```json
{
  "coordinate": { "x": 9.93, "y": 49.79, "srid": 4326 },
  "admin": {
    "country_iso": "DE",
    "hierarchy": [
      { "level": 8, "name": "Würzburg", "name_native": "", "name_source": "latin-osm",
        "equivalent": "municipality", "local_term": "Kreisfreie Stadt",
        "equivalent_description": "Local municipal authority (city/town/commune)." }
    ]
  },
  "bearing": {
    "reference": "Würzburg", "name_native": "", "name_source": "latin-osm",
    "class": "city", "distance_km": 4.0, "azimuth": 90.0, "compass": "E",
    "label": "4 km E Würzburg", "inside": false
  },
  "elevation": {
    "meters": 177.0, "accuracy_m": 4.0, "accuracy_basis": "GLO-30 LE90 (absolute)",
    "horizontal_accuracy_m": 6.0, "vertical_datum": "EGM2008", "sea_level": false,
    "surface_model": "DSM",
    "source": { "name": "Copernicus DEM GLO-30", "url": "…", "attribution": "© DLR/Airbus/ESA …" }
  },
  "sources": [
    { "code": "latin-osm", "short": "OSM name (already Latin)",
      "long": "Taken verbatim from the OpenStreetMap name tag; already Latin script, no transliteration applied.",
      "standard": "" }
  ],
  "license": {
    "name": "ODbL-1.0",
    "url": "https://opendatacommons.org/licenses/odbl/1-0/",
    "attribution": "© OpenStreetMap contributors (ODbL 1.0); Natural Earth (public domain); GeoNames (CC BY 4.0); NGA GNS (public domain)"
  }
}
```

Each admin unit and the bearing anchor carry the romanized `name`, the
original-script `name_native` (empty when the name is already Latin), and a
`name_source` provenance code. Admin units also carry the country-specific
`local_term` and the generic `equivalent_description` for their tier. The
response-wide `sources` block describes each distinct `name_source` code once
(`code`, `short`, `long`, `standard`) rather than repeating it per record; it is
`[]` when no codes are present. Enrichment of `sources` requires
`gazetteer.name_source_manifest_path` to be configured — without it each record
still carries its raw `name_source` code but the `sources` entries have empty
descriptions. The `license` block is the dataset-wide attribution (from the
`license:` section of `ortus-gazetteer.yaml`); it is omitted when the manifest
sets no license. This is the same block that appears under `gazetteer` in the
[`/query`](#query-all-sources) response.

The `elevation` block is present only when a DEM is configured
(`gazetteer.elevation.source_id`); it is `null` otherwise. It reports the height
above sea level (`meters`) at the query point, `vertical_datum` (e.g. `EGM2008`),
the vertical `accuracy_m` with its `accuracy_basis` (a dataset constant, or a
per-point value when an accuracy layer such as a Height Error Mask is configured),
`horizontal_accuracy_m`, `surface_model` (e.g. `DSM`), and `sea_level: true` with
`meters: 0` where no DEM tile covers the point (ocean / outside coverage). The DEM
`source` (name/url/attribution) is carried separately from the gazetteer `license`
because it is a distinct dataset — both attributions must be displayed.

## Source management

```text
GET /api/v1/sources                      # list all sources
GET /api/v1/sources/{sourceId}           # source details
GET /api/v1/sources/{sourceId}/layers    # layers of a source
```

`GET /api/v1/sources` returns `{ sources: [...], count }`, each source with
`id`, `name`, `path`, `size`, `layer_count`, `indexed`, `ready`, `loaded_at`,
`last_queried`, and `license` (name/url/attribution) when the package carries
one — omitted otherwise:

```json
{
  "sources": [
    { "id": "districts", "name": "districts.gpkg", "path": "districts.gpkg",
      "size": 1048576, "layer_count": 1, "indexed": true, "ready": true,
      "loaded_at": "2026-07-06T12:00:00Z", "last_queried": "2026-07-06T12:05:00Z",
      "license": { "name": "CC-BY-4.0", "url": "https://creativecommons.org/licenses/by/4.0/",
        "attribution": "© Example Data Provider" } }
  ],
  "count": 1
}
```

A source's license travels inside the package: for GeoPackages it is the
`gpkg_metadata` row with `mime_type='application/json'` **and**
`md_standard_uri='https://ortus.dev/schema/dataset-metadata.json'`, holding a
`license` object (JSON under any other URI is ignored); for raster bundles it is
the `license:` block of the manifest. A package without a license loads but logs
a warning and shows no attribution.

`GET /api/v1/sources/{sourceId}` returns a single source object (same fields, not
wrapped). `GET /api/v1/sources/{sourceId}/layers` returns
`{ source_id, layers: [...], count }`, each layer with `name`, `description`,
`geometry_type`, `geometry_column`, `srid`, `has_index`, `feature_count`, and an
optional `extent` (`min_x`/`min_y`/`max_x`/`max_y`).

## Sync endpoint

```text
POST /api/v1/sync
```

Manually trigger a sync with remote storage (see
[Sync sources from remote storage](../how-to/sync-remote-storage.md)). Rate
limited to **one trigger per 30 seconds**.

```json
{ "sources_added": 2, "sources_removed": 1, "sources_total": 5,
  "synced_at": "2025-12-22T12:00:00Z", "next_scheduled_at": "2025-12-22T13:00:00Z" }
```

Within the 30-second cooldown it returns `429` with `Retry-After: 30`. Only
available when sync is enabled on a remote storage backend.

## Health endpoints

```bash
curl "http://localhost:8080/health"        # detailed, per-source status
curl "http://localhost:8080/health/live"   # liveness
curl "http://localhost:8080/health/ready"  # readiness
```

`GET /health` returns `{ status: "ok"|"unhealthy", ready, sources_loaded,
sources_ready, sources: [...], components: {...} }` (HTTP `200` when healthy,
`503` otherwise). `GET /health/live` returns `{ "status": "ok" }` (or
`"unhealthy"`); `GET /health/ready` returns `{ "status": "ok" }` (or
`"not ready"`).

**Readiness semantics:** `/health/ready` reports **not ready only during the
initial load** (sources downloading/indexing), so clients retry while data comes
online. Once the initial pass completes it reports ready — including with **zero
sources** ("ready, no data today"). It does **not** flip back to not-ready when
new sources arrive later via sync. `/health` lists per-source status
(`loading`/`indexing`/`ready`/`error`). Set `ORTUS_SERVER_READY_WHEN_EMPTY=false`
to additionally require at least one ready source.

Recommended Kubernetes probes: a **startupProbe** on `/health/ready` (generous
`failureThreshold` to cover the initial load), `readinessProbe` →
`/health/ready`, `livenessProbe` → `/health/live`.
