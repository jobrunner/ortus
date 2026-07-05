# HTTP API

All API endpoints are prefixed with `/api/v1`. Health endpoints live at the root.
The full OpenAPI 3.0 spec is served at `GET /openapi.json`, with Swagger UI at
`/docs` (and `/swagger`).

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
administrative hierarchy, bearing, name-source explanations and the dataset
attribution — so a client gets everything to process the result in one call. It is
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

Reverse-geocode a coordinate to its administrative hierarchy (`admin`) and compute
a bearing to the most salient nearby place (`bearing`, e.g. "4 km E Würzburg").
Either part is `null` when it has no result — no admin coverage, or no anchor
within reach. The dataset is WGS84; a non-4326 `srid` is rejected.

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
    "label": "4 km E Würzburg"
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

## Source management

```text
GET /api/v1/sources                      # list all sources
GET /api/v1/sources/{sourceId}           # source details
GET /api/v1/sources/{sourceId}/layers    # layers of a source
```

`GET /api/v1/sources` returns id, name, path, size, layer count, and `ready`
status per source. `…/layers` returns geometry type/column, SRID, index status,
feature count, and extent per layer.

## Sync endpoint

```text
POST /api/v1/sync
```

Manually trigger a sync with remote storage (see
[Sync sources from remote storage](../how-to/sync-remote-storage.md)). Rate
limited to 2 requests/minute.

```json
{ "sources_added": 2, "sources_removed": 1, "sources_total": 5,
  "synced_at": "2025-12-22T12:00:00Z", "next_scheduled_at": "2025-12-22T13:00:00Z" }
```

Over the limit returns `429` with `Retry-After: 30`. Only available when sync is
enabled on a remote storage backend.

## Health endpoints

```bash
curl "http://localhost:8080/health"        # detailed, per-source status
curl "http://localhost:8080/health/live"   # liveness
curl "http://localhost:8080/health/ready"  # readiness
```

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
