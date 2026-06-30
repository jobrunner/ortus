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

### Query a specific source

```text
GET /api/v1/query/{sourceId}?lon={longitude}&lat={latitude}
```

```bash
curl "http://localhost:8080/api/v1/query/districts?lon=13.405&lat=52.52"
```

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
