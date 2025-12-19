# Ortus

Ortus is a Go-based REST service for point queries on GeoPackage files. It allows you to query geographic features that contain a given coordinate using spatial SQLite (SpatiaLite) queries.

## Features

- **Point Queries**: Find all features containing a coordinate using ST_Contains
- **Multiple GeoPackages**: Query across multiple GeoPackage files simultaneously
- **Coordinate Transformation**: Automatic projection to layer SRID
- **Hot-Reload**: Automatic detection of new/removed GeoPackages
- **Object Storage**: Load GeoPackages from S3, Azure Blob, or HTTP sources
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
      --metrics               Enable Prometheus metrics (default true)
  -h, --help                  Show help
```

### Environment Variables

All configuration options can be set via environment variables with the `ORTUS_` prefix:

| Variable | Default | Description |
|----------|---------|-------------|
| `ORTUS_HOST` | `0.0.0.0` | HTTP server host |
| `ORTUS_PORT` | `8080` | HTTP server port |
| `ORTUS_STORAGE_TYPE` | `local` | Storage type (local/s3/azure/http) |
| `ORTUS_STORAGE_LOCAL_PATH` | `./data` | Path to GeoPackage directory |
| `ORTUS_SERVER_CORS_ALLOWED_ORIGINS` | `[]` | Allowed CORS origins (comma-separated) |
| `ORTUS_LOG_LEVEL` | `info` | Log level |
| `ORTUS_TLS_ENABLED` | `false` | Enable TLS |
| `ORTUS_METRICS_ENABLED` | `true` | Enable Prometheus metrics |

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

Query all loaded GeoPackages for features containing the given coordinate.

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
      "package_id": "districts",
      "package_name": "districts.gpkg",
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

#### Query Specific Package

```
GET /api/v1/query/{packageId}?lon={longitude}&lat={latitude}
```

Query a specific GeoPackage by its ID.

```bash
curl "http://localhost:8080/api/v1/query/districts?lon=13.405&lat=52.52"
```

### Package Management

#### List All Packages

```
GET /api/v1/packages
```

```bash
curl "http://localhost:8080/api/v1/packages"
```

**Response:**

```json
{
  "packages": [
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

#### Get Package Details

```
GET /api/v1/packages/{packageId}
```

```bash
curl "http://localhost:8080/api/v1/packages/districts"
```

#### Get Package Layers

```
GET /api/v1/packages/{packageId}/layers
```

```bash
curl "http://localhost:8080/api/v1/packages/districts/layers"
```

**Response:**

```json
{
  "package_id": "districts",
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
      - ./data:/data:ro
    environment:
      ORTUS_STORAGE_LOCAL_PATH: /data
      ORTUS_LOG_LEVEL: info
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
./ortus serve --storage-type=s3
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

## TLS / HTTPS

Enable automatic TLS with Let's Encrypt:

```bash
./ortus serve \
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

## Architecture

Ortus follows the Hexagonal Architecture (Ports & Adapters) pattern. See [ARCHITECTURE.md](doc/ARCHITECTURE.md) for details.

## License

MIT License
