# Ortus

Ortus is a Go-based REST service for point queries on GeoPackage files. It allows you to query geographic features that contain a given coordinate using spatial SQLite (SpatiaLite) queries.

## Features

- **Point Queries**: Find all features containing a coordinate using ST_Contains
- **Multiple GeoPackages**: Query across multiple GeoPackage files simultaneously
- **Coordinate Transformation**: Automatic projection to layer SRID
- **Hot-Reload**: Automatic detection of new/removed GeoPackages
- **Object Storage**: Load GeoPackages from S3, Azure Blob, or HTTP sources
- **Remote Storage Sync**: Periodic synchronization with S3/Azure to detect and load new GeoPackages
- **TLS/HTTPS**: Optional HTTPS with Let's Encrypt via CertMagic
- **CORS**: Configurable Cross-Origin Resource Sharing for web frontends
- **Prometheus Metrics**: Built-in metrics endpoint for monitoring
- **Rate Limiting**: Configurable request rate limiting

## Installation

### From Source

```bash
git clone https://github.com/jobrunner/ortus.git
cd ortus
make build
```

### Docker

```bash
docker pull ghcr.io/jobrunner/ortus:latest
```

## Quick Start

```bash
# Start server with local GeoPackage directory
./ortus --storage-path=./data

# With custom port and CORS
./ortus --port=8080 --cors=https://example.com,*.myapp.com
```

## Configuration

Ortus can be configured via CLI flags, environment variables, or a config file.

**Priority**: CLI Flags > Environment Variables > Config File > Defaults

### CLI Flags

```bash
./ortus [flags]

Flags:
      --config string         Config file path (default: ./config.yaml)
      --host string           HTTP server host (default "0.0.0.0")
      --port int              HTTP server port (default 8080)
      --storage-type string   Storage type: local, s3, azure, http (default "local")
      --storage-path string   Local storage path for GeoPackages (default "./data")
      --cors strings          Allowed CORS origins (e.g., https://example.com,*.sub.domain.tld)
      --tls                   Enable TLS
      --tls-domains strings   TLS domains for Let's Encrypt
      --tls-email string      Email for Let's Encrypt
      --log-level string      Log level: debug, info, warn, error (default "info")
  -h, --help                  Show help
```

### Environment Variables

All configuration options can be set via environment variables with the `ORTUS_` prefix:

| Variable | Default | Description |
|----------|---------|-------------|
| `ORTUS_SERVER_HOST` | `0.0.0.0` | HTTP server host |
| `ORTUS_SERVER_PORT` | `8080` | HTTP server port |
| `ORTUS_STORAGE_TYPE` | `local` | Storage type (local/s3/azure/http) |
| `ORTUS_STORAGE_LOCAL_PATH` | `./data` | Path to GeoPackage directory |
| `ORTUS_SERVER_CORS_ALLOWED_ORIGINS` | `[]` | Allowed CORS origins (comma-separated) |
| `ORTUS_LOGGING_LEVEL` | `info` | Log level (debug/info/warn/error) |
| `ORTUS_LOGGING_FORMAT` | `json` | Log format (json/text) |
| `ORTUS_TLS_ENABLED` | `false` | Enable TLS |
| `ORTUS_METRICS_ENABLED` | `true` | Enable Prometheus metrics |
| `ORTUS_SERVER_READY_WHEN_EMPTY` | `true` | Report ready with zero loaded sources (after initial load) |
| `ORTUS_SYNC_ENABLED` | `false` | Enable periodic remote storage sync |
| `ORTUS_SYNC_INTERVAL` | `1h` | Sync interval (e.g., 30m, 1h, 24h) |

### Config File

Create a `config.yaml` in the working directory or specify with `--config`:

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  cors:
    allowed_origins:
      - "https://example.com"
      - "*.myapp.com"

storage:
  type: local
  local_path: ./data

logging:
  level: "info"
  format: "json"

metrics:
  enabled: true
  path: "/metrics"
```

## API Endpoints

All API endpoints are prefixed with `/api/v1`.

### Query Endpoints

#### Query All Packages

```
GET /api/v1/query?lon={longitude}&lat={latitude}
GET /api/v1/query?x={x}&y={y}&srid={srid}
```

Query all loaded sources for features containing the given coordinate.

**Parameters:**
- `lon` / `lat`: WGS84 coordinates (SRID 4326)
- `x` / `y`: Coordinates in specified SRID
- `srid`: Source coordinate SRID (default: 4326)
- `properties`: Comma-separated list of properties to return

**Example:**

```bash
# Query with WGS84 coordinates (lon/lat)
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52"

# Query with specific SRID
curl "http://localhost:8080/api/v1/query?x=389283&y=5819450&srid=25832"

# Query with property filter
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52&properties=name,population"
```

**Response:**

```json
{
  "coordinate": {
    "x": 13.405,
    "y": 52.52,
    "srid": 4326
  },
  "results": [
    {
      "source_id": "districts",
      "source_name": "districts.gpkg",
      "features": [
        {
          "id": 42,
          "layer": "districts",
          "properties": {
            "name": "Mitte",
            "population": 384172
          }
        }
      ],
      "feature_count": 1,
      "query_time_ms": 5
    }
  ],
  "total_features": 1,
  "processing_time_ms": 12
}
```

#### Query Specific Source

```
GET /api/v1/query/{sourceId}?lon={longitude}&lat={latitude}
```

Query a specific source by its ID.

```bash
curl "http://localhost:8080/api/v1/query/districts?lon=13.405&lat=52.52"
```

### Source Management

#### List All Sources

```
GET /api/v1/sources
```

```bash
curl "http://localhost:8080/api/v1/sources"
```

**Response:**

```json
{
  "sources": [
    {
      "id": "districts",
      "name": "districts.gpkg",
      "path": "/data/districts.gpkg",
      "size": 1048576,
      "layer_count": 2,
      "indexed": true,
      "ready": true
    }
  ],
  "count": 1
}
```

#### Get Source Details

```
GET /api/v1/sources/{sourceId}
```

```bash
curl "http://localhost:8080/api/v1/sources/districts"
```

#### Get Source Layers

```
GET /api/v1/sources/{sourceId}/layers
```

```bash
curl "http://localhost:8080/api/v1/sources/districts/layers"
```

**Response:**

```json
{
  "source_id": "districts",
  "layers": [
    {
      "name": "districts",
      "description": "Administrative districts",
      "geometry_type": "MULTIPOLYGON",
      "geometry_column": "geom",
      "srid": 4326,
      "has_index": true,
      "feature_count": 12,
      "extent": {
        "min_x": 13.088,
        "min_y": 52.338,
        "max_x": 13.761,
        "max_y": 52.675
      }
    }
  ],
  "count": 1
}
```

### Sync Endpoint

#### Trigger Sync

```
POST /api/v1/sync
```

Manually trigger a sync with remote storage. Rate limited to 2 requests per minute.

```bash
curl -X POST "http://localhost:8080/api/v1/sync"
```

**Response (200 OK):**

```json
{
  "sources_added": 2,
  "sources_removed": 1,
  "sources_total": 5,
  "synced_at": "2025-12-22T12:00:00Z",
  "next_scheduled_at": "2025-12-22T13:00:00Z"
}
```

**Response (429 Too Many Requests):**

```
Retry-After: 30
Rate limit exceeded
```

> **Note:** This endpoint is only available when sync is enabled and using remote storage.

### Health Endpoints

Health endpoints are available at root level (not under `/api/v1`):

```bash
# Detailed health status
curl "http://localhost:8080/health"

# Kubernetes liveness probe
curl "http://localhost:8080/health/live"

# Kubernetes readiness probe
curl "http://localhost:8080/health/ready"
```

**Readiness semantics:** `/health/ready` reports **not ready only during the
initial load** (while sources are being downloaded/indexed) so clients retry
while data is being brought online. Once the initial load pass completes it
reports ready — including with **zero sources** ("ready, no data today"). It does
**not** flip back to not-ready when new sources arrive later via sync, so the
instance keeps serving the sources it already has. `/health` lists per-source
status (`loading`/`indexing`/`ready`/`error`) so a client can tell that one
specific source is still coming online. Set `ORTUS_SERVER_READY_WHEN_EMPTY=false`
to additionally require at least one ready source.

Recommended Kubernetes probes: a **startupProbe** on `/health/ready` (generous
`failureThreshold` to cover the initial load), `readinessProbe` → `/health/ready`,
`livenessProbe` → `/health/live`.

### API Documentation

#### Swagger UI

Interactive API documentation is available via Swagger UI:

```
http://localhost:8080/docs
http://localhost:8080/swagger
```

Open one of these URLs in your browser to explore and test the API interactively.

#### OpenAPI Specification

The OpenAPI 3.0 specification is available as JSON:

```bash
curl "http://localhost:8080/openapi.json"
```

The specification includes all endpoints, request/response schemas, and examples.

## Docker Usage

### Docker Run

```bash
docker run -d \
  -p 8080:8080 \
  -v /path/to/geopackages:/data \
  -e ORTUS_STORAGE_LOCAL_PATH=/data \
  ghcr.io/jobrunner/ortus:latest
```

### Docker Compose

```yaml
version: '3.8'
services:
  ortus:
    image: ghcr.io/jobrunner/ortus:latest
    ports:
      - "8080:8080"
      - "9090:9090"  # Metrics
    volumes:
      # Must be writable: Ortus creates R-tree spatial indexes inside the
      # GeoPackage files on first load, and SQLite needs to write a journal
      # file alongside the database. Do not mount this read-only.
      - ./data:/data
    environment:
      ORTUS_STORAGE_LOCAL_PATH: /data
      ORTUS_LOGGING_LEVEL: info
      ORTUS_SERVER_CORS_ALLOWED_ORIGINS: "https://example.com,*.myapp.com"
```

## Object Storage

### AWS S3

```yaml
storage:
  type: s3
  s3:
    bucket: my-geopackages
    region: eu-central-1
    prefix: gpkg/
```

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
./ortus --storage-type=s3
```

### Azure Blob Storage

```yaml
storage:
  type: azure
  azure:
    container: geopackages
    account_name: mystorageaccount
```

### HTTP Download

```yaml
storage:
  type: http
  http:
    base_url: "https://data.example.com/gpkg/"
    index_file: "index.txt"
```

## Remote Storage Sync

When using remote storage (S3/Azure/HTTP), Ortus can periodically check for new GeoPackages and automatically download and load them. This is useful for scenarios where GeoPackages are added to the remote storage after the container has started.

### Configuration

```yaml
sync:
  enabled: true       # Enable periodic sync
  interval: "1h"      # Sync interval (e.g., "30m", "1h", "24h")
```

Or via environment variables:

```bash
ORTUS_SYNC_ENABLED=true
ORTUS_SYNC_INTERVAL=1h
```

### Manual Sync via API

You can trigger an immediate sync using the API endpoint:

```bash
curl -X POST "http://localhost:8080/api/v1/sync"
```

**Response:**

```json
{
  "sources_added": 2,
  "sources_removed": 1,
  "sources_total": 5,
  "synced_at": "2025-12-22T12:00:00Z",
  "next_scheduled_at": "2025-12-22T13:00:00Z"
}
```

The sync operation both adds new packages and removes packages that no longer exist in remote storage. The API endpoint is rate-limited to 2 requests per minute (30 second cooldown). If exceeded, a `429 Too Many Requests` response is returned with a `Retry-After: 30` header.

> **Note:** Sync is only available for remote storage types (s3, azure, http), not for local storage. For local storage, use the hot-reload feature which automatically detects file changes.

## TLS / HTTPS

Enable automatic TLS with Let's Encrypt:

```bash
./ortus \
  --tls \
  --tls-domains=ortus.example.com \
  --tls-email=admin@example.com
```

Or via config file:

```yaml
tls:
  enabled: true
  domains:
    - ortus.example.com
  email: admin@example.com
  cache_dir: ./.certmagic
```

## Metrics

Prometheus metrics are available at `/metrics` (default port 9090 in Docker, same port in standalone).

```bash
curl "http://localhost:8080/metrics"
```

Internally the metrics are produced via the OpenTelemetry meter API.
By default they are exported only as Prometheus scrape format. To
additionally push them to an OTLP collector (e.g. the same one tracing
uses) enable `metrics.otlp.enabled`; the endpoint falls back to
`tracing.endpoint` when not set explicitly. The HTTP request metrics
(`ortus_http_requests_total`, `ortus_http_request_duration_seconds`)
use the matched gorilla/mux route template as the `path` label, so
dynamic segments like `{sourceId}` collapse to a single bounded label
combination.

## MCP (AI integration)

Ortus ships an in-process Model Context Protocol server so AI agents
(Claude Desktop, Claude Code, custom MCP clients) can both **observe**
the service (traces, active spans, health) and **use** it (point
queries, package metadata). Two transports: streamable-HTTP for remote
agents (`mcp.enabled: true` in `config.yaml`) and stdio for Claude
Desktop (`./ortus mcp` subcommand). See [doc/MCP.md](doc/MCP.md) for the
tool catalogue, auth model, and Claude Desktop setup.

## Tracing

Ortus emits OpenTelemetry traces and additionally retains the most recent
traces in memory for direct inspection by the upcoming MCP server. The
in-memory buffer keeps two FIFO pools — successful traces and error traces —
each up to `tracing.buffer_size` (default 256), so worst-case retention is
2× that value. Enable via `--tracing` or `ORTUS_TRACING_ENABLED=true`, point
at a collector with `--tracing-endpoint=host:port`, and see
[doc/TRACING.md](doc/TRACING.md) for the full configuration surface
(transport, sampling, headers, resource attributes) and the list of
instrumented spans.

## Architecture

Ortus follows the Hexagonal Architecture (Ports & Adapters) pattern. See [ARCHITECTURE.md](doc/ARCHITECTURE.md) for details.

## License

MIT License
